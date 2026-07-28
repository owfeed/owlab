// Package upstream asks a distribution's download server what it has
// published.
//
// It exists so that a pinned release is a decision someone made rather than a
// number nobody has looked at since. owlab pins hard — a rootfs and its feed
// have to name the same point release or every install fails — and the cost of
// pinning is that the pin goes stale silently. Reading the server is the only
// way to know.
package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VizzleTF/owlab/internal/config"
)

// pointRelease matches the href of a release directory: `href="25.12.4/"`.
//
// The href rather than the link text, because the text is what a page theme
// decides to render and the href is what the server actually serves. Release
// candidates carry a suffix (`24.10.0-rc1/`) and are deliberately not matched:
// a dev router should track what users are running.
var pointRelease = regexp.MustCompile(`href="(\d+\.\d+\.\d+)/"`)

// Releases lists every point release a distribution publishes, newest first.
func Releases(ctx context.Context, d config.Distro) ([]string, error) {
	spec, ok := config.LookupDistro(d)
	if !ok {
		return nil, fmt.Errorf("unknown distro %q", d)
	}
	url := spec.DownloadHost + "/" + spec.ReleasesPath + "/"

	body, err := get(ctx, url)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []string
	for _, m := range pointRelease.FindAllStringSubmatch(string(body), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no releases found in the listing", url)
	}
	Sort(out)
	return out, nil
}

// Branch is the major.minor part of a release: "25.12.4" -> "25.12".
func Branch(release string) string {
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return release
	}
	return parts[0] + "." + parts[1]
}

// Sort orders releases newest first, numerically.
//
// Not lexically: "24.10.10" sorts before "24.10.9" as a string, which would
// pick the wrong release the first time a branch reaches double digits — a
// bug that would sit dormant for a year and then quietly downgrade everyone.
func Sort(releases []string) {
	sort.Slice(releases, func(i, j int) bool {
		return compare(releases[i], releases[j]) > 0
	})
}

func compare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// InBranch returns the releases belonging to one branch, newest first.
func InBranch(releases []string, branch string) []string {
	var out []string
	for _, r := range releases {
		if Branch(r) == branch {
			out = append(out, r)
		}
	}
	Sort(out)
	return out
}

// Newest returns up to n releases from a branch, newest first.
func Newest(releases []string, branch string, n int) []string {
	in := InBranch(releases, branch)
	if n > 0 && len(in) > n {
		in = in[:n]
	}
	return in
}

// Published reports whether a release actually has artifacts for a router's
// target.
//
// Asked because the two facts are separate: a point release appears in the
// listing when it is tagged, and a given target's images land when that
// target's build finishes. Building the matrix from the listing alone produces
// a job that fails on a 404 twenty minutes in.
func Published(ctx context.Context, r *config.Router, release string) bool {
	probe := r.WithRelease(release)
	return head(ctx, probe.RootfsTarballURL())
}

// Update is what a pinned release could become.
type Update struct {
	Router  string
	Distro  config.Distro
	Branch  string
	Pinned  string
	Newest  string
	Behind  int
	Missing bool
}

// Check compares every router's pinned release against what its distribution
// publishes on the same branch.
//
// Concurrent per distribution, because the answer for every OpenWrt router
// comes out of one listing and fetching it once per router would be four
// requests for one fact.
func Check(ctx context.Context, cfg *config.Config) ([]Update, error) {
	listings := map[config.Distro][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	distros := map[config.Distro]bool{}
	for i := range cfg.Routers {
		distros[cfg.Routers[i].Distro] = true
	}
	for d := range distros {
		wg.Add(1)
		go func(d config.Distro) {
			defer wg.Done()
			rel, err := Releases(ctx, d)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			listings[d] = rel
		}(d)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	var out []Update
	for i := range cfg.Routers {
		r := &cfg.Routers[i]
		branch := Branch(r.Release)
		in := InBranch(listings[r.Distro], branch)
		if len(in) == 0 {
			out = append(out, Update{
				Router: r.ID, Distro: r.Distro, Branch: branch,
				Pinned: r.Release, Missing: true,
			})
			continue
		}
		behind := 0
		for _, rel := range in {
			if compare(rel, r.Release) > 0 {
				behind++
			}
		}
		out = append(out, Update{
			Router: r.ID, Distro: r.Distro, Branch: branch,
			Pinned: r.Release, Newest: in[0], Behind: behind,
		})
	}
	return out, nil
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func head(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// HasContainerImage reports whether the upstream container image for a
// router's release actually exists.
//
// The two are published on different schedules. A point release lands on the
// download server when it is built; the matching openwrt/rootfs tag appears
// when somebody's job gets round to it, and sometimes days later. Assuming the
// image exists because the release does produces
//
//	failed to resolve source metadata for docker.io/openwrt/rootfs:aarch64_generic-25.12.5:
//	not found
//
// on a release that is perfectly buildable — owlab can unpack the tarball
// itself, which is the path ImmortalWrt takes for every image already.
//
// Only a definitive 404 counts. A network failure or a rate limit leaves the
// answer as "yes", because falling back to a 250 MB tarball download every
// time Docker Hub is slow would be a worse trade than one clear build error.
func HasContainerImage(ctx context.Context, r *config.Router) bool {
	image := r.BaseImage()
	if image == "" {
		return false
	}
	repo, tag, ok := strings.Cut(image, ":")
	if !ok {
		return true
	}
	// The Hub API rather than the registry, because the registry wants a token
	// even for a public read and this needs no credentials at all.
	url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags/%s", repo, tag)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return true
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	return resp.StatusCode != http.StatusNotFound
}
