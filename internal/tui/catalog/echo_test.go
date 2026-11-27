package catalog

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestEchoCommandQuotesEveryValue(t *testing.T) {
	tests := []struct {
		operand string
		flags   []string
		want    string
	}{
		{"bp-0001", []string{"--decode-snapshots"}, `dtctl get things bp-0001 --decode-snapshots`},
		{"fetch logs", nil, `dtctl get things 'fetch logs'`},
		{`it's`, nil, `dtctl get things 'it'\''s'`},
		{"", nil, `dtctl get things ''`},
		{"a\n  b", nil, "dtctl get things 'a\n  b'"},
		{"$(id)", []string{"-S", "`id`"}, "dtctl get things '$(id)' -S '`id`'"},
		// A value that starts with a dash would be read as a flag: it goes
		// last, behind the end-of-options marker.
		{"-rf", []string{"-S", "uid-a"}, `dtctl get things -S uid-a -- -rf`},
		{"uid", []string{"-S", "-x"}, `dtctl get things uid -S -x`},
		// zsh replaces a word that starts with an equals sign by the path of
		// the command it names, so such a value is quoted.
		{"=ls", []string{"-S", "=id"}, `dtctl get things '=ls' -S '=id'`},
		{"a=b", nil, `dtctl get things 'a=b'`},
	}
	for _, tt := range tests {
		if got := EchoCommand("dtctl get things", tt.operand, tt.flags...); got != tt.want {
			t.Errorf("EchoCommand(%q, %q) =\n  %s\nwant\n  %s", tt.operand, tt.flags, got, tt.want)
		}
	}
}

// Every printable character, and the three kinds of white space a value can
// hold, comes back from a shell as it went in: alone, leading a word and
// inside one, as the operand and as a flag. A character that EchoCommand
// leaves bare and a shell gives a meaning to fails here (a glob, a tilde, a
// comment, zsh's `=cmd`).
func TestEchoCommandSurvivesAShell(t *testing.T) {
	shells := []string{}
	for _, name := range []string{"sh", "zsh"} {
		if path, err := exec.LookPath(name); err == nil {
			shells = append(shells, path)
		}
	}
	if len(shells) == 0 {
		t.Skip("no POSIX shell on PATH")
	}
	chars := []rune{'\t', '\n', '\r'}
	for c := rune(0x20); c < 0x7f; c++ {
		chars = append(chars, c)
	}
	for _, c := range chars {
		var lines, want []string
		for _, value := range []string{string(c), string(c) + "x", "x" + string(c) + "y"} {
			lines = append(lines, EchoCommand("printf '%s\\0'", value, value))
			if strings.HasPrefix(value, "-") {
				want = append(want, value, "--", value)
			} else {
				want = append(want, value, value)
			}
		}
		script := strings.Join(lines, "\n")
		for _, shell := range shells {
			out, err := exec.Command(shell, "-c", script).Output()
			if err != nil {
				t.Errorf("%s: %q does not parse: %v\n%s", shell, c, err, script)
				continue
			}
			got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s read %q back as %q\n%s", shell, want, got, script)
			}
		}
	}
}

// What a non-interactive shell reads literally is not all an interactive one
// does (history expansion on `!`, brace expansion, zsh's extended globs), so
// the bare set is also pinned by name: letters, digits and nine marks. Any
// other character puts its word in quotes.
func TestEchoCommandLeavesOnlyPlainWordsBare(t *testing.T) {
	for c := rune(0x20); c < 0x7f; c++ {
		plain := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-+:,./@%", c)
		value := "x" + string(c) + "y"
		got := EchoCommand("cmd", value)
		if bare := got == "cmd "+value; bare != plain {
			t.Errorf("EchoCommand(%q) = %s, bare=%v want %v", value, got, bare, plain)
		}
	}
}
