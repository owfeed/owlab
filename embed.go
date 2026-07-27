// Package owlab carries the build context that owlab ships with.
//
// The Dockerfile and the rootfs overlay are embedded in the binary rather
// than read from disk, because owlab is installed as a single file by brew,
// scoop, winget or a release download. A developer who runs `owlab up` in
// their own LuCI package has no copy of this repository, and should not need
// one.
package owlab

import (
	"embed"
	"io/fs"
)

//go:embed all:images
var imagesFS embed.FS

// BuildContext is the docker build context: the Dockerfile at its root and
// the rootfs-extra overlay beside it.
//
// all: is required — the overlay contains /etc/uci-defaults files, and
// go:embed silently skips names starting with _ or . without it.
func BuildContext() fs.FS {
	sub, err := fs.Sub(imagesFS, "images")
	if err != nil {
		// Only reachable if the embed directive above stops matching, which
		// is a build-time mistake, not a runtime condition.
		panic(err)
	}
	return sub
}
