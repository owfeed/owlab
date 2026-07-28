// Package tarx builds the tar streams owlab pushes into routers.
//
// A tar on stdin is the one file transfer both tiers share: a container is
// reached with `docker exec -i`, a VM over ssh, and dropbear ships no
// sftp-server for scp to use. Every path that puts a file on a router — a
// synced source tree, a local .apk, the VM's rootfs overlay — ends up writing
// the same kind of stream, so the header rules live here rather than in three
// copies that could drift apart.
package tarx

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"path"
	"strings"
	"time"
)

// Writer accumulates files into one archive rooted at /.
type Writer struct {
	buf bytes.Buffer
	tw  *tar.Writer
	err error
}

// New starts an archive.
func New() *Writer {
	w := &Writer{}
	w.tw = tar.NewWriter(&w.buf)
	return w
}

// Add writes one file. The destination is absolute as the router sees it
// ("/tmp/x.apk"); the leading slash is stripped because a relative member
// unpacked with `tar -C /` lands in the same place and busybox is happier
// about it.
//
// Errors are held until Bytes, so a caller adding a tree does not have to
// check after every file.
func (w *Writer) Add(dest string, body []byte, mode fs.FileMode) {
	w.AddTime(dest, body, mode, time.Time{})
}

// AddTime is Add with an explicit modification time, for the cases where the
// source file's own mtime is worth carrying across.
func (w *Writer) AddTime(dest string, body []byte, mode fs.FileMode, mtime time.Time) {
	if w.err != nil {
		return
	}
	hdr := &tar.Header{
		Name: strings.TrimPrefix(path.Clean(dest), "/"),
		Mode: int64(mode.Perm()),
		Size: int64(len(body)),
		// GNU rather than USTAR: a LuCI tree reaches paths longer than the 100
		// bytes USTAR allows, and busybox tar reads the GNU form happily.
		Format:  tar.FormatGNU,
		ModTime: mtime,
	}
	if w.err = w.tw.WriteHeader(hdr); w.err != nil {
		return
	}
	_, w.err = w.tw.Write(body)
}

// Bytes closes the archive and returns it, or the first error seen.
func (w *Writer) Bytes() ([]byte, error) {
	if w.err != nil {
		return nil, w.err
	}
	if err := w.tw.Close(); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

// OneFile is an archive containing a single file, which is how owlab pushes a
// package or a generated config onto a router.
func OneFile(dest string, body []byte, mode fs.FileMode) ([]byte, error) {
	w := New()
	w.Add(dest, body, mode)
	return w.Bytes()
}
