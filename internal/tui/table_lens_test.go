package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func lensTable() *tableView {
	spec := &catalog.Spec{
		Name:    "spans",
		Query:   func(catalog.Scope) string { return "fetch spans" },
		Columns: []catalog.Column{{Title: "NAME", Field: "name"}, {Title: "N", Field: "n", Width: 4, Right: true}},
		Lenses: []catalog.Lens{
			{Name: "all", Desc: "every span"},
			{Name: "roots", Desc: "entry spans"},
			{Name: "errors", Desc: "failed spans"},
		},
	}
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }, previewOff: true}
	return newTableView(ds, spec, catalog.Scope{Timeframe: catalog.DefaultTimeframe})
}

// collectMsgs runs cmd and every command a tea.Batch fans out to.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, collectMsgs(c)...)
	}
	return out
}

func lensStatus(t *testing.T, v *tableView, k string) string {
	t.Helper()
	cmd := v.Update(key(k))
	if cmd == nil {
		return ""
	}
	for _, m := range collectMsgs(cmd) {
		if s, ok := m.(statusMsg); ok {
			return s.text
		}
	}
	return ""
}

func TestTableLensCycling(t *testing.T) {
	v := lensTable()
	v.sortCol = 1
	v.cursor = 2
	steps := []struct {
		key  string
		lens int
		msg  string
	}{
		{"]", 1, "lens: roots — entry spans"},
		{"tab", 2, "lens: errors — failed spans"},
		{"]", 0, "lens: all — every span"},      // wraps forward
		{"[", 2, "lens: errors — failed spans"}, // wraps back
		{"shift+tab", 1, "lens: roots — entry spans"},
	}
	for _, s := range steps {
		if got := lensStatus(t, v, s.key); got != s.msg || v.scope.Lens != s.lens {
			t.Fatalf("%s: lens %d status %q, want lens %d %q", s.key, v.scope.Lens, got, s.lens, s.msg)
		}
	}
	if v.sortCol != -1 || v.cursor != 0 {
		t.Errorf("a lens switch resets sort and cursor: sort %d cursor %d", v.sortCol, v.cursor)
	}
	if v.setLens(1, false) != nil || v.setLens(3, false) != nil || v.setLens(-1, false) != nil {
		t.Error("the current or an out-of-range lens is a no-op")
	}
	if got := v.Crumb(); got != "spans·roots" {
		t.Errorf("crumb = %q", got)
	}
	v.setLens(0, false)
	if got := v.Crumb(); got != "spans" {
		t.Errorf("the default lens stays out of the crumb: %q", got)
	}

	plain := colsTable(catalog.Column{Title: "A"})
	for _, k := range []string{"]", "["} {
		if got := lensStatus(t, plain, k); got != "no lens strip on this view" {
			t.Errorf("%s without lenses: %q", k, got)
		}
	}
}

func TestTableRowCountValidity(t *testing.T) {
	v := lensTable()
	if _, ok := v.RowCount(); ok {
		t.Error("no fetch yet: count is not valid")
	}
	v.seq = 1
	v.all = []map[string]any{{"name": "a"}}
	if n, ok := v.RowCount(); n != 1 || !ok {
		t.Errorf("RowCount = %d %v", n, ok)
	}
	v.loading = true
	if _, ok := v.RowCount(); ok {
		t.Error("loading: count is not valid")
	}
}

func TestTableEmptyStates(t *testing.T) {
	v := lensTable()
	body := func() string { return ansi.Strip(v.View(100, 12)) }

	if b := body(); !strings.Contains(b, "∅ no data in timeframe (last 2h) — [/] switches lens") {
		t.Errorf("the all lens reads as data:\n%s", b)
	}
	v.scope.Lens = 2
	if b := body(); !strings.Contains(b, "∅ no errors in timeframe (last 2h) — [/] switches lens") {
		t.Errorf("a named lens names what is missing:\n%s", b)
	}
	v.all = []map[string]any{{"name": "a"}, {"name": "b"}}
	v.setFilter("zzz")
	if b := body(); !strings.Contains(b, `no rows match "zzz" (2 fetched — esc clears the filter)`) {
		t.Errorf("filter miss:\n%s", b)
	}
	v.loading = true
	if strings.Contains(body(), "no rows match") {
		t.Error("no empty state while loading")
	}

	plain := colsTable(catalog.Column{Title: "A"})
	plain.scope.Timeframe = catalog.DefaultTimeframe
	if b := ansi.Strip(plain.View(80, 10)); !strings.Contains(b, "∅ no data in timeframe (last 2h)") || strings.Contains(b, "switches lens") {
		t.Errorf("plain empty state:\n%s", b)
	}
}

func TestTableHeaderAndRows(t *testing.T) {
	v := lensTable()
	v.lensDigits = true
	v.all = []map[string]any{{"name": "alpha", "n": 3.0}, {"name": "beta", "n": 12.0}, {"name": "gamma", "n": 1.0}}
	v.applyFilter()
	v.sortCol, v.sortDesc = 1, true
	v.applyFilter()
	v.cursor = 1
	assertSnapshot(t, "table-lens-60x8", ansi.Strip(v.View(60, 8)))
	// Two visible rows: the third is cut, and no trailing newline follows
	// the last one.
	out := ansi.Strip(v.View(60, 5))
	if strings.Contains(out, "gamma") || strings.HasSuffix(out, "\n") {
		t.Errorf("visible rows must stop at the height:\n%q", out)
	}
}
