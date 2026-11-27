package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// layoutProbe renders previewLayout with a body that reports the size it was
// given and a preview of n numbered lines that reports its width.
func layoutProbe(width, height, n int) (out string, bodyW, bodyH, paneW int) {
	body := func(w, h int) string {
		bodyW, bodyH = w, h
		lines := make([]string, h)
		for i := range lines {
			lines[i] = fmt.Sprintf("b%d", i)
		}
		return strings.Join(lines, "\n")
	}
	preview := func(w int) []string {
		paneW = w
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("p%d", i)
		}
		return lines
	}
	return ansi.Strip(previewLayout(true, width, height, body, preview, foldHintEnter)), bodyW, bodyH, paneW
}

func TestPreviewLayoutGates(t *testing.T) {
	cases := []struct {
		name                string
		w, h                int
		bodyW, bodyH, paneW int
	}{
		{"side at the minimum width", 110, 20, 63, 20, 44},
		{"side pane capped", 200, 20, 149, 20, 48},
		{"bottom just below the side width", 109, 30, 109, 20, 107},
		{"hidden just below the bottom height", 109, 29, 109, 29, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, bw, bh, pw := layoutProbe(c.w, c.h, 3)
			if bw != c.bodyW || bh != c.bodyH || pw != c.paneW {
				t.Errorf("body %dx%d pane %d, want body %dx%d pane %d", bw, bh, pw, c.bodyW, c.bodyH, c.paneW)
			}
		})
	}
	if previewSideOn(false, 200) || previewBottomOn(false, 80, 40) {
		t.Error("preview off must disable both panes")
	}
}

func TestPreviewLayoutSideFolds(t *testing.T) {
	out, _, _, _ := layoutProbe(120, 4, 6)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("side layout should fill exactly the height, got %d lines:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "b0") || !strings.HasSuffix(lines[0], "│ p0") {
		t.Errorf("first row = %q", lines[0])
	}
	if !strings.HasSuffix(lines[3], "│ … +3 more (enter opens)") {
		t.Errorf("overflow fold = %q", lines[3])
	}

	// A preview exactly the height folds nothing.
	out, _, _, _ = layoutProbe(120, 4, 4)
	if strings.Contains(out, "more") || !strings.HasSuffix(out, "│ p3") {
		t.Errorf("exact-height preview should not fold:\n%s", out)
	}
}

func TestPreviewLayoutBottomFolds(t *testing.T) {
	out, _, _, _ := layoutProbe(80, 30, 10)
	lines := strings.Split(out, "\n")
	// 20 body lines, the rule, 8 preview lines and the fold.
	if len(lines) != 30 {
		t.Fatalf("bottom layout = %d lines:\n%s", len(lines), out)
	}
	if lines[19] != "b19" || lines[20] != strings.Repeat("─", 80) || lines[21] != " p0" {
		t.Errorf("body/rule/preview boundary = %q %q %q", lines[19], lines[20], lines[21])
	}
	if lines[28] != " p7" || lines[29] != " … (enter opens the full record)" {
		t.Errorf("fold = %q %q", lines[28], lines[29])
	}

	out, _, _, _ = layoutProbe(80, 30, 9)
	if strings.Contains(out, "enter opens") || !strings.HasSuffix(out, " p8") {
		t.Errorf("a nine-line preview fits the panel:\n%s", out)
	}
}

func TestWrapFactLinesCapsAtFour(t *testing.T) {
	got := wrapFactLines("content", strings.Repeat("word ", 40), 12)
	if len(got) != 6 || ansi.Strip(got[5]) != "  …" {
		t.Errorf("wrapFactLines = %q", got)
	}
	got = wrapFactLines("content", "short", 12)
	if len(got) != 2 || ansi.Strip(got[1]) != "  short" {
		t.Errorf("wrapFactLines = %q", got)
	}
}
