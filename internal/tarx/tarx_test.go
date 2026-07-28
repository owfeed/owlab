package tarx

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func read(t *testing.T, archive []byte) []*tar.Header {
	t.Helper()
	var out []*tar.Header
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, hdr)
	}
}

// The archive is unpacked with `tar -C /`, so members are relative: a leading
// slash would make busybox complain about stripping it on every sync.
func TestNamesAreRelativeAndClean(t *testing.T) {
	archive, err := OneFile("/tmp/thing.apk", []byte("body"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	hdrs := read(t, archive)
	if len(hdrs) != 1 {
		t.Fatalf("got %d members, want 1", len(hdrs))
	}
	if hdrs[0].Name != "tmp/thing.apk" {
		t.Errorf("Name = %q, want tmp/thing.apk", hdrs[0].Name)
	}
	if hdrs[0].Format != tar.FormatGNU {
		// A LuCI tree reaches paths longer than the 100 bytes USTAR allows.
		t.Errorf("Format = %v, want GNU", hdrs[0].Format)
	}
	if hdrs[0].Mode != 0o644 {
		t.Errorf("Mode = %o, want 644", hdrs[0].Mode)
	}
}

func TestModeAndTimeSurvive(t *testing.T) {
	when := time.Unix(1700000000, 0)
	w := New()
	w.Add("/etc/init.d/thing", []byte("#!/bin/sh"), 0o755)
	w.AddTime("/www/app.js", []byte("1"), 0o644, when)
	archive, err := w.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	hdrs := read(t, archive)
	if len(hdrs) != 2 {
		t.Fatalf("got %d members, want 2", len(hdrs))
	}
	if hdrs[0].Mode != 0o755 {
		t.Errorf("an executable lost its mode: %o", hdrs[0].Mode)
	}
	if !hdrs[1].ModTime.Equal(when) {
		t.Errorf("ModTime = %v, want %v", hdrs[1].ModTime, when)
	}
}

// Errors are held until Bytes so a caller adding a tree does not have to check
// after every file — but they must not be swallowed.
func TestLongPathsSurvive(t *testing.T) {
	long := "/usr/lib/lua/luci/" + strings.Repeat("nested/", 20) + "controller.lua"
	archive, err := OneFile(long, []byte("x"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	hdrs := read(t, archive)
	if got := "/" + hdrs[0].Name; got != long {
		t.Errorf("path was truncated:\n got %q\nwant %q", got, long)
	}
}

func TestEmptyArchiveIsValid(t *testing.T) {
	archive, err := New().Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(read(t, archive)) != 0 {
		t.Error("an empty archive reported members")
	}
}
