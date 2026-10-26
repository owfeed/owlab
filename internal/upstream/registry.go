package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"owfeed.org/owlab/internal/netx"
)

// ImageState is what a registry said about an image.
type ImageState int

const (
	// ImageFound: the registry has the image, or answered in a way only the
	// engine can settle.
	ImageFound ImageState = iota
	// ImageMissing: the registry answered 404 for the tag.
	ImageMissing
	// RegistryDown: the registry did not answer through netx's attempts.
	RegistryDown
)

// RegistryTransport is what ProbeImage sends its requests through, nil for
// http.DefaultTransport. A variable so tests can point it at a TLS test server.
var RegistryTransport http.RoundTripper

// ProbeImage asks the registry that serves ref whether it has that image.
//
// The registry itself, not hub.docker.com's API, which is what owlab used to
// ask: the API is a different host from the one the engine pulls from, and it
// knows only Docker Hub, while `image:` can name ghcr.io or any other registry.
// A HEAD of the manifest, which Docker Hub does not count as a pull.
//
// Only a 404 is ImageMissing, and only an outage that outlasted netx's attempts
// is RegistryDown. Anything else is ImageFound: a 401 on a private image may be
// one the engine holds credentials for, and a build error naming the image is
// clearer than a quiet switch of path. The error says what failed for the two
// states that are not a plain yes.
func ProbeImage(ctx context.Context, ref string) (ImageState, error) {
	host, repo, tag := splitRef(ref)
	manifest := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, repo, tag)
	// 30 s for each request, netx's retries included: a registry that accepts
	// connections and never answers is down, the same as one that refuses them.
	client := netx.Client(&http.Client{Transport: RegistryTransport, Timeout: 30 * time.Second})

	resp, err := headManifest(ctx, client, manifest, "")
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		// A public image still wants a token, which the registry hands to
		// anyone at the realm its challenge names.
		var token string
		if token, err = anonymousToken(ctx, client, resp.Header.Get("WWW-Authenticate"), repo); err == nil {
			resp, err = headManifest(ctx, client, manifest, token)
		}
	}
	switch {
	case err != nil:
		var oe *netx.OutageError
		if errors.As(err, &oe) {
			return RegistryDown, fmt.Errorf("no answer after %d attempts: %w", oe.Attempts, oe.Err)
		}
		if netx.Transient(err) {
			return RegistryDown, err
		}
		return ImageFound, err
	case resp.StatusCode == http.StatusNotFound:
		return ImageMissing, fmt.Errorf("HEAD %s: %s", manifest, resp.Status)
	case netx.TransientStatus(resp.StatusCode):
		return RegistryDown, fmt.Errorf("HEAD %s: %s after %d attempts", manifest, resp.Status, netx.DefaultAttempts)
	}
	return ImageFound, nil
}

// RegistryHost is the host the engine pulls ref from.
func RegistryHost(ref string) string {
	host, _, _ := splitRef(ref)
	return host
}

// splitRef reads an image reference the way the engine does: a first path
// element with a dot or a port, or "localhost", is a registry host; anything
// else is on Docker Hub, which serves its API from registry-1.docker.io and
// keeps one-word names under library/. A digest wins over a tag; no tag is
// "latest".
func splitRef(ref string) (host, repo, tag string) {
	name, digest, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	switch {
	case digest != "":
		tag = digest
	case tag == "":
		tag = "latest"
	}

	host = "registry-1.docker.io"
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, name = first, rest
	}
	if host == "docker.io" || host == "index.docker.io" {
		host = "registry-1.docker.io"
	}
	if host == "registry-1.docker.io" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	return host, name, tag
}

func headManifest(ctx context.Context, c *http.Client, manifest, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifest, nil)
	if err != nil {
		return nil, err
	}
	// Every manifest kind the engine accepts, so the answer is about the tag
	// and not about which format the registry stored it in.
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return resp, nil
}

// anonymousToken asks the realm a 401 named for a pull token on repo, with no
// credentials: what Docker Hub and ghcr.io give anyone for a public image.
func anonymousToken(ctx context.Context, c *http.Client, challenge, repo string) (string, error) {
	params := parseChallenge(challenge)
	if params["realm"] == "" {
		return "", fmt.Errorf("the registry wants credentials (%q), not an anonymous token", challenge)
	}
	u, err := url.Parse(params["realm"])
	if err != nil {
		return "", fmt.Errorf("the registry's token realm %q: %w", params["realm"], err)
	}
	q := u.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+repo+":pull")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", netx.Status(u.String(), resp)
	}
	// Docker Hub answers with both names, ghcr.io with "token" only; the
	// distribution spec allows either.
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("GET %s: reading the token: %w", u, err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

// parseChallenge reads the parameters of a Bearer challenge:
// `Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="..."`. Nil for
// any other scheme. Values are quoted and may hold commas (a scope can list
// "pull,push"), so this is not a split on ",".
func parseChallenge(h string) map[string]string {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return nil
	}
	out := map[string]string{}
	for {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			return out
		}
		key = strings.ToLower(strings.Trim(key, ", "))
		var val string
		if strings.HasPrefix(after, `"`) {
			end := strings.IndexByte(after[1:], '"')
			if end < 0 {
				return out
			}
			val, rest = after[1:1+end], after[2+end:]
		} else {
			val, rest, _ = strings.Cut(after, ",")
		}
		out[key] = val
	}
}
