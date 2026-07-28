package main

import (
	"context"
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *all {
		return a.listAllReleases(ctx, *keep)
	}

	updates, err := upstream.Check(ctx, a.cfg)
	if err != nil {
		return err
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

func (a *app) listAllReleases(ctx context.Context, keep int) error {
	seen := map[config.Distro]bool{}
	for i := range a.cfg.Routers {
		seen[a.cfg.Routers[i].Distro] = true
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
