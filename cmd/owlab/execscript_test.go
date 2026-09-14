package main

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

// One word after -- is a script the developer quoted themselves, and must reach
// the shell untouched: quoting it would turn `-- 'ps | grep uhttpd'` into a
// command named "ps | grep uhttpd".
func TestExecScriptPassesOneWordThrough(t *testing.T) {
	for _, s := range []string{
		"ps | grep -c uhttpd",
		"cat > /tmp/f",
		"uname -a",
		"echo $HOME 'x'",
		"",
	} {
		if got := execScript([]string{s}); got != s {
			t.Errorf("execScript(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestExecScriptQuotesEveryWordOfAnArgv(t *testing.T) {
	for _, tc := range []struct {
		words []string
		want  string
	}{
		// The measured bug: joined bare this was `sh -c exit 3`, exit status 0.
		{[]string{"sh", "-c", "exit 3"}, `'sh' '-c' 'exit 3'`},
		{[]string{"printf", `%s\n`, "a b"}, `'printf' '%s\n' 'a b'`},
		{[]string{"printf", "%s", ""}, `'printf' '%s' ''`},
		{[]string{"echo", "it's"}, `'echo' 'it'\''s'`},
		{[]string{"echo", "$HOME"}, `'echo' '$HOME'`},
		{[]string{"echo", `a\b`}, `'echo' 'a\b'`},
		{[]string{"ls", "*"}, `'ls' '*'`},
		{[]string{"cat", "/etc/passwd", "|", "grep", "root"}, `'cat' '/etc/passwd' '|' 'grep' 'root'`},
	} {
		if got := execScript(tc.words); got != tc.want {
			t.Errorf("execScript(%q) = %s, want %s", tc.words, got, tc.want)
		}
	}
}

// The quoting is only right if a shell reads each word back exactly. This runs
// the script under the host's sh, which is what the container (busybox ash)
// and dropbear's login shell do on the other end.
func TestExecScriptRoundTripsThroughSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	words := []string{"", "a b", "it's", "'", "$HOME", "`id`", `a\b`, "*", "|", ";", "\"", "x\ny", "--"}
	// printf '%s\0' <words…> prints each argument NUL-terminated, so the
	// words the shell saw can be compared with the words that went in.
	argv := append([]string{"printf", `%s\0`}, words...)
	var out bytes.Buffer
	cmd := exec.Command(sh, "-c", execScript(argv))
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("sh -c %s: %v", execScript(argv), err)
	}
	got := bytes.Split(bytes.TrimSuffix(out.Bytes(), []byte{0}), []byte{0})
	gotWords := make([]string, len(got))
	for i, b := range got {
		gotWords[i] = string(b)
	}
	if !reflect.DeepEqual(gotWords, words) {
		t.Errorf("sh read back %q, want %q", gotWords, words)
	}

	// And the exit status is the command's, which is how the bug was seen.
	err = exec.Command(sh, "-c", execScript([]string{"sh", "-c", "exit 3"})).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Errorf("sh -c %s: %v, want exit status 3", execScript([]string{"sh", "-c", "exit 3"}), err)
	}
}
