package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/VizzleTF/owlab/internal/config"
	"github.com/VizzleTF/owlab/internal/upstream"
)

// releases reports what the download servers publish, and how far this
// project's pins have fallen behind.
//
// owlab pins hard — a rootfs and its feed have to name the same point release
// or every install fails — and the cost of pinning is that the pin goes stale
// without saying so. This is how you find out.
func (a *app) releases(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("releases", flag.ContinueOnError)
	all := fs.Bool("all", false, "list every published release, not just this project's branches")
	keep := fs.Int("keep", 3, "how many releases per branch to list with --all")
	asJSON := fs.Bool("json", false, "write the answer as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *all {
		if *asJSON {
			return a.allReleasesJSON(ctx, *keep)
		}
		return a.listAllReleases(ctx, *keep)
	}

	// "How stale are the pins" is a question about a project, so without one
	// there is nothing to compare and the honest answer is the full list.
	if a.cfg == nil {
		if path, err := resolveConfig(a.configPath); err == nil {
			a.cfg, _ = config.Load(path)
		}
	}
	if a.cfg == nil {
		if *asJSON {
			return a.allReleasesJSON(ctx, *keep)
		}
		return a.listAllReleases(ctx, *keep)
	}

	updates, err := upstream.Check(ctx, a.cfg)
	if err != nil {
		return err
	}

	if *asJSON {
		return staleJSON(updates)
	}

	fmt.Printf("%-26s %-12s %-10s %-10s %s\n", "ROUTER", "DISTRO", "PINNED", "NEWEST", "")
	stale := 0
	for _, u := range updates {
		note := "up to date"
		switch {
		case u.Missing:
			note = "branch " + u.Branch + " is not on the download server"
		case u.Behind == 1:
			note = "1 release behind"
			stale++
		case u.Behind > 1:
			note = fmt.Sprintf("%d releases behind", u.Behind)
			stale++
		}
		fmt.Printf("%-26s %-12s %-10s %-10s %s\n", u.Router, u.Distro, u.Pinned, u.Newest, note)
	}
	if stale > 0 {
		fmt.Printf("\nEdit %s to move a pin. `owlab up --rebuild` then rebuilds against it.\n", config.FileName)
	}
	return nil
}

// staleJSON is the pin report for a script.
//
// The shape a CI job wants: one object per router, with `behind` as a number
// so `jq '[.routers[].behind] | add'` is the whole of "is anything stale". A
// weekly job that opens an issue when it is non-zero is the reason this exists.
func staleJSON(updates []upstream.Update) error {
	type row struct {
		Router  string `json:"router"`
		Distro  string `json:"distro"`
		Branch  string `json:"branch"`
		Pinned  string `json:"pinned"`
		Newest  string `json:"newest"`
		Behind  int    `json:"behind"`
		Missing bool   `json:"missing"`
	}
	rows := make([]row, 0, len(updates))
	stale := 0
	for _, u := range updates {
		if u.Behind > 0 {
			stale++
		}
		rows = append(rows, row{u.Router, string(u.Distro), u.Branch, u.Pinned, u.Newest, u.Behind, u.Missing})
	}
	doc := struct {
		Schema  string `json:"schema"`
		Stale   int    `json:"stale"`
		Routers []row  `json:"routers"`
	}{"owlab.releases/v1", stale, rows}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// allReleasesJSON is the published-release listing for a script — the form a
// build matrix is generated from, so that "the current point release of each
// branch" stops being a list somebody has to remember to edit.
func (a *app) allReleasesJSON(ctx context.Context, keep int) error {
	type branch struct {
		Branch   string   `json:"branch"`
		Latest   string   `json:"latest"`
		Releases []string `json:"releases"`
	}
	type distroDoc struct {
		Distro   string   `json:"distro"`
		Branches []branch `json:"branches"`
	}

	// Which distributions to list. A project narrows it to the ones it actually
	// uses; with no project there is nothing to narrow by, so list them all --
	// that is the answer to "what do the download servers publish".
	seen := map[config.Distro]bool{}
	if a.cfg == nil {
		for _, d := range config.KnownDistros() {
			seen[config.Distro(d)] = true
		}
	}
	if a.cfg != nil {
		for i := range a.cfg.Routers {
			seen[a.cfg.Routers[i].Distro] = true
		}
	}
	var out []distroDoc
	for _, d := range config.KnownDistros() {
		distro := config.Distro(d)
		if !seen[distro] {
			continue
		}
		rel, err := upstream.Releases(ctx, distro)
		if err != nil {
			// Reported, not fatal: one download server being unreachable should
			// not cost the answer for the other.
			fmt.Fprintf(os.Stderr, "! %s: %v\n", d, err)
			continue
		}
		doc := distroDoc{Distro: d}
		byBranch := map[string][]string{}
		for _, r := range rel {
			b := upstream.Branch(r)
			if _, ok := byBranch[b]; !ok {
				doc.Branches = append(doc.Branches, branch{Branch: b, Latest: r})
			}
			byBranch[b] = append(byBranch[b], r)
		}
		for i := range doc.Branches {
			list := byBranch[doc.Branches[i].Branch]
			if keep > 0 && len(list) > keep {
				list = list[:keep]
			}
			doc.Branches[i].Releases = list
		}
		out = append(out, doc)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Schema  string      `json:"schema"`
		Distros []distroDoc `json:"distros"`
	}{"owlab.releases.all/v1", out})
}

func (a *app) listAllReleases(ctx context.Context, keep int) error {
	// Which distributions to list. A project narrows it to the ones it actually
	// uses; with no project there is nothing to narrow by, so list them all --
	// that is the answer to "what do the download servers publish".
	seen := map[config.Distro]bool{}
	if a.cfg == nil {
		for _, d := range config.KnownDistros() {
			seen[config.Distro(d)] = true
		}
	}
	if a.cfg != nil {
		for i := range a.cfg.Routers {
			seen[a.cfg.Routers[i].Distro] = true
		}
	}
	for _, d := range config.KnownDistros() {
		distro := config.Distro(d)
		if !seen[distro] {
			continue
		}
		rel, err := upstream.Releases(ctx, distro)
		if err != nil {
			fmt.Fprintf(os.Stderr, "! %s: %v\n", d, err)
			continue
		}
		fmt.Printf("%s\n", d)
		branches := []string{}
		byBranch := map[string][]string{}
		for _, r := range rel {
			b := upstream.Branch(r)
			if _, ok := byBranch[b]; !ok {
				branches = append(branches, b)
			}
			byBranch[b] = append(byBranch[b], r)
		}
		for _, b := range branches {
			list := byBranch[b]
			if keep > 0 && len(list) > keep {
				list = list[:keep]
			}
			fmt.Printf("  %-8s %s\n", b, strings.Join(list, "  "))
		}
	}
	return nil
}
