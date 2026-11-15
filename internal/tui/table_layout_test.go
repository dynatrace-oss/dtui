package tui

import (
	"slices"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func colsTable(cols ...catalog.Column) *tableView {
	return newTableView(&dataSource{}, &catalog.Spec{Name: "t", Columns: cols}, catalog.Scope{})
}

func TestColumnWidths(t *testing.T) {
	name := catalog.Column{Title: "NAME", Width: 20}
	status := catalog.Column{Title: "STATUS", Width: 10}
	flex := catalog.Column{Title: "X"}
	cases := []struct {
		name  string
		cols  []catalog.Column
		total int
		want  []int
	}{
		{"room to spare: flex shares the rest", []catalog.Column{{Title: "A", Width: 10}, flex, flex}, 50, []int{10, 18, 18}},
		{"no room: fixed floors, flex keeps 8", []catalog.Column{{Title: "A", Width: 10}, flex, flex}, 20, []int{4, 8, 8}},
		{"proportional shrink fits exactly", []catalog.Column{name, status, flex}, 30, []int{12, 6, 8}},
		{"floor overshoot is shaved off the widest", []catalog.Column{name, status, flex}, 27, []int{9, 6, 8}},
		{"declared Min is a floor", []catalog.Column{{Title: "ID", Width: 12, Min: 10}, {Title: "X", Width: 12}}, 20, []int{10, 8}},
		{"fits unshrunk", []catalog.Column{name, status}, 32, []int{20, 10}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := colsTable(c.cols...).columnWidths(c.total); !slices.Equal(got, c.want) {
				t.Errorf("columnWidths(%d) = %v, want %v", c.total, got, c.want)
			}
		})
	}
}

func TestCellHelpers(t *testing.T) {
	cases := []struct{ got, want string }{
		{flatten("a\n\tb  c"), "a b c"},
		{flatten("a  b"), "a  b"},
		{pad("abc", 5), "abc  "},
		{pad("abcdef", 4), "abc…"},
		{pad("abcd", 4), "abcd"},
		{cell("7", 3, true), "  7"},
		{cell("abcd", 3, true), "ab…"},
		{cell("abc", 3, true), "abc"},
		{cell("7", 3, false), "7  "},
		{wrap("x y", 0), "x y"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d = %q, want %q", i, c.got, c.want)
		}
	}
}

func TestTablePageSize(t *testing.T) {
	v := colsTable(catalog.Column{Title: "A"})
	v.ds.previewOff = true
	for _, c := range []struct{ h, want int }{{20, 17}, {4, 1}, {3, 10}} {
		v.height = c.h
		if got := v.pageSize(); got != c.want {
			t.Errorf("pageSize at height %d = %d, want %d", c.h, got, c.want)
		}
	}
	v.spec.Lenses = []catalog.Lens{{Name: "all"}}
	v.height = 20
	if got := v.pageSize(); got != 16 {
		t.Errorf("a lens strip takes a line: pageSize = %d", got)
	}
	v.ds.previewOff = false
	v.width, v.height = 100, 40
	if got := v.pageSize(); got != 26 {
		t.Errorf("the bottom preview takes its panel: pageSize = %d", got)
	}
}

func TestTableSortKeysReportStatus(t *testing.T) {
	v := colsTable(catalog.Column{Title: "A", Field: "a"}, catalog.Column{Title: "B", Field: "b"})
	v.all = []map[string]any{{"a": "x", "b": 2.0}, {"a": "y", "b": 1.0}}
	v.applyFilter()
	statusOf := func(k string) string {
		t.Helper()
		cmd := v.Update(key(k))
		if cmd == nil {
			t.Fatalf("%s returned no command", k)
		}
		return cmd().(statusMsg).text
	}
	for _, s := range []struct{ key, want string }{
		{"K", "no sort column (J selects one)"},
		{"J", "sort: A ↑"},
		{"K", "sort: A ↓"},
		{"J", "sort: B ↓"},
		{"J", "sort: fetch order"},
		{"J", "sort: A ↑"},
	} {
		if got := statusOf(s.key); got != s.want {
			t.Errorf("%s: status %q, want %q", s.key, got, s.want)
		}
	}
	if v.rows[0]["a"] != "x" {
		t.Errorf("ascending text sort should put x first, got %v", v.rows[0])
	}
}
