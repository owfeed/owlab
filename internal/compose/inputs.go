package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"runtime/debug"
	"sort"

	owlab "owfeed.org/owlab"
	"owfeed.org/owlab/internal/config"
)

// InputsLabel is the image label that carries Inputs.
const InputsLabel = "org.owlab.inputs"

// Inputs is a digest of what one router's image is built from, as far as owlab
// knows it before the build: this owlab's version and VCS revision, the build
// context it carries, the build args, the platform, the release and its rootfs,
// and the out-of-feed packages' URLs.
//
// The image carries it as InputsLabel, and `owlab up --no-build` starts only an
// image whose label matches the config it reads now. Without that, an image
// loaded from a CI cache, or built before owlab.yaml changed, comes up as a
// router the config does not describe, and nothing says so.
//
// What the feeds served during the build is not in it, and cannot be: the same
// inputs built a week apart install that week's packages. Neither is the base
// image's digest, for the same reason. The build args include ROOT_PASSWORD;
// the image's own history already carries it in the clear, as every build arg
// a RUN reads.
func Inputs(cfg *config.Config, r *config.Router) string {
	h := sha256.New()
	// The revision too, not only the context: a commit that changes how owlab
	// stages or names a downloaded file changes the image with the Dockerfile
	// unchanged. Two builds of one commit with different uncommitted changes
	// share a revision, and so a digest.
	fmt.Fprintf(h, "owlab %s\n", builder())

	ctx := owlab.BuildContext()
	// WalkDir walks in lexical order, so the digest does not depend on the
	// order the embed lists the files in.
	err := fs.WalkDir(ctx, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(ctx, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "file %q %d\n", p, len(b))
		_, err = h.Write(b)
		return err
	})
	if err != nil {
		// Only reachable if the embedded context cannot be read, which is a
		// build-time mistake, as in BuildContext.
		panic(err)
	}

	args := BuildArgsFor(cfg, r)
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "arg %q %q\n", k, args[k])
	}
	fmt.Fprintf(h, "platform %q\n", r.Platform())
	// A router built from the tarball names neither its release nor its target
	// in the build args, and every router's compliance files come from its
	// release's directory, whatever base the build starts from. The release
	// itself as well: snapshot aliases share a directory.
	fmt.Fprintf(h, "rootfs %q %q\n", r.Release, r.RootfsTarballURL())
	// In order: the image installs them in the order owlab.yaml lists them.
	for _, e := range r.Extra {
		fmt.Fprintf(h, "extra %q %q\n", e.Name, e.URLFor(r.PackageManager()))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// builder names the source this binary was built from. A build from a checkout
// records the tag, or a pseudo-version naming the commit (with +dirty for
// uncommitted changes), and `go install` records the version it resolved.
func builder() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	s := info.Main.Version
	for _, kv := range info.Settings {
		if kv.Key == "vcs.revision" || kv.Key == "vcs.modified" {
			s += " " + kv.Value
		}
	}
	return s
}
