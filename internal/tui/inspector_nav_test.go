package tui

import (
	"fmt"
	"strings"
	"testing"
)

// navInspector is a page of 30 one-line fields, an expanded 25-line block
// (stack, rows line 30–54) and one trailing field (zz, line 55), in a
// viewport 11 lines tall: ctrl+d/u move 5 lines, pgdown/pgup 10.
func navInspector(t *testing.T) *inspectorView {
	t.Helper()
	rec := map[string]any{
		"stack": strings.Repeat("frame line that is long enough to wrap around the screen ", 30),
		"zz":    "last",
	}
	for i := range 30 {
		rec[fmt.Sprintf("f%02d", i)] = fmt.Sprintf("v%d", i)
	}
	v := newInspectorView(&dataSource{}, "t", rec)
	v.View(80, 12)
	if len(v.rows) != 32 || v.rows[30].line != 30 || v.rows[30].span != 25 || v.rows[31].line != 55 || v.vp.Height != 11 {
		t.Fatalf("unexpected layout: %d rows, vp height %d", len(v.rows), v.vp.Height)
	}
	return v
}

type navStep struct {
	key            string
	cursor, offset int
}

func runNav(t *testing.T, v *inspectorView, steps []navStep) {
	t.Helper()
	for i, s := range steps {
		v.Update(key(s.key))
		if v.cursor != s.cursor || v.vp.YOffset != s.offset {
			t.Fatalf("step %d (%s): cursor=%d offset=%d, want cursor=%d offset=%d",
				i, s.key, v.cursor, v.vp.YOffset, s.cursor, s.offset)
		}
	}
}

func TestInspectorPagesByContentLines(t *testing.T) {
	runNav(t, navInspector(t), []navStep{
		{"ctrl+d", 5, 0},
		{"pgdown", 15, 5},
		{"ctrl+u", 10, 5},
		{"pgup", 0, 0},
		{"j", 1, 0},
		{"k", 0, 0},
		{"k", 0, 0},
		{"G", 31, 45},
		{"j", 31, 45},
		{"g", 0, 0},
	})
}

// A block taller than the screen scrolls line by line before the cursor
// leaves it, and a page jump over it still makes progress.
func TestInspectorScrollsThroughTallRow(t *testing.T) {
	v := navInspector(t)
	for range 29 {
		v.Update(key("j"))
	}
	if v.cursor != 29 || v.vp.YOffset != 19 {
		t.Fatalf("after 29×j: cursor=%d offset=%d", v.cursor, v.vp.YOffset)
	}
	runNav(t, v, []navStep{
		{"j", 30, 30},      // block top aligns with the screen top
		{"j", 30, 31},      // scroll inside the block
		{"pgdown", 30, 41}, // …by a page
		{"j", 30, 42},
		{"pgdown", 30, 44}, // clamped at the block bottom
		{"ctrl+d", 31, 45}, // bottom visible: the page jump leaves the block
		{"ctrl+u", 30, 30}, // no row within reach: still steps back one
		{"ctrl+d", 30, 35},
		{"k", 30, 34},
		{"pgup", 30, 30}, // clamped at the block top
		{"k", 29, 29},    // top visible: the cursor leaves the block
	})
}
