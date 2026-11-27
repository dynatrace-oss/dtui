package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func TestSortKeysCycleAndToggle(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{
		podRow("a", "ns1", "Running", 3),
		podRow("b", "ns1", "Running", 31),
		podRow("c", "ns1", "Running", 0),
	})
	tv := a.top().(*tableView)

	// J to the RST column (index 3): numeric column defaults descending.
	for range 4 {
		press(a, key("J"))
	}
	if tv.sortCol != 3 || !tv.sortDesc {
		t.Fatalf("sortCol = %d desc = %v, want 3/desc", tv.sortCol, tv.sortDesc)
	}
	if catalog.Str(tv.rows[0], "name") != "b" {
		t.Errorf("rows not sorted by restarts desc: %v", tv.rows)
	}

	// K toggles direction.
	press(a, key("K"))
	if tv.sortDesc || catalog.Str(tv.rows[0], "name") != "c" {
		t.Errorf("K should flip to ascending, first = %s", catalog.Str(tv.rows[0], "name"))
	}
}
func TestSpanLensSwitching(t *testing.T) {
	a := testApp(t, "traces")
	tv := a.top().(*tableView)
	if !strings.Contains(tv.dql, "| filter isNull(span.parent_id)") {
		t.Fatalf("traces should open on the roots lens:\n%s", tv.dql)
	}

	// ] cycles forward, [ back; the crumb names any non-default lens.
	press(a, key("]"))
	if a.top() != tv {
		t.Fatalf("] on a lensed table must switch lens, not views (top = %T)", a.top())
	}
	if !strings.Contains(tv.dql, `span.status_code == "error"`) {
		t.Errorf("lens errors not composed:\n%s", tv.dql)
	}
	if tv.Crumb() != "traces·errors" {
		t.Errorf("crumb = %q, want traces·errors", tv.Crumb())
	}
	if got := tv.columns()[2].Title; got != "ERROR" {
		t.Errorf("errors lens column[2] = %q, want ERROR (the minimal why)", got)
	}
	// The exceptions lens: a string-match filter (iterative expressions are
	// rejected inside DQL filter) and its own thrown-first columns.
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span_event.name`) {
		t.Errorf("] should advance to exceptions lens:\n%s", tv.dql)
	}
	if got := tv.columns()[1].Title; got != "EXCEPTION" {
		t.Errorf("exceptions lens column[1] = %q, want EXCEPTION", got)
	}
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span.kind == "server"`) {
		t.Errorf("] should advance to server lens:\n%s", tv.dql)
	}
	press(a, key("["))
	if !strings.Contains(tv.dql, `span_event.name`) {
		t.Errorf("[ should return to exceptions lens:\n%s", tv.dql)
	}

	// The db lens swaps in its curated statement columns.
	for range 3 { // exceptions → server → client → db
		press(a, key("]"))
	}
	if got := tv.columns()[1].Title; got != "STATEMENT" {
		t.Errorf("db lens column[1] = %q, want STATEMENT", got)
	}

	// The category lenses carry their own columns too.
	for range 2 { // db → rpc → messaging
		press(a, key("]"))
	}
	if got := tv.columns()[1].Title; got != "DESTINATION" {
		t.Errorf("messaging lens column[1] = %q, want DESTINATION", got)
	}

	// Digits never touch the lens strip: they stay global hotkeys.
	press(a, key("5"))
	if logs, ok := a.top().(*tableView); !ok || logs.spec.Name != "logs" {
		t.Fatalf("digit 5 should jump to logs, top = %v", a.top().Crumb())
	}
	press(a, key("0"))
	if _, ok := a.top().(*homeView); !ok {
		t.Fatalf("digit 0 should stay a hotkey jump to home, top = %T", a.top())
	}
}

// TestTabCyclesLensOnPlainTable: tab drives the view's primary strip — the
// lens strip on a top-level table — while digits stay global bookmarks there
// (no numbers on screen means no local claim).
func TestTabCyclesLensOnPlainTable(t *testing.T) {
	a := testApp(t, "traces")
	tv := a.top().(*tableView)
	start := tv.scope.Lens
	press(a, key("tab"))
	if tv.scope.Lens == start {
		t.Fatal("tab should advance the lens strip on a plain table")
	}
	press(a, key("shift+tab"))
	if tv.scope.Lens != start {
		t.Fatal("shift+tab should cycle the lens strip back")
	}
	press(a, key("1"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "problems" {
		t.Fatalf("digit on a top-level table must stay a global bookmark, top = %T", a.top())
	}
}
func TestAPIViewRoutesThroughSource(t *testing.T) {
	a := testApp(t, "home")
	var gotScope catalog.Scope
	a.ds.sources = map[string]Source{
		"slos": func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error) {
			gotScope = scope
			return []map[string]any{{"name": "checkout availability", "status": "SUCCESS", "sli": 99.98, "target": 99.9}}, nil
		},
	}
	tv := newTableView(a.ds, catalog.Lookup("slos"), catalog.Scope{Timeframe: a.tf})
	cmd := tv.Refresh()
	if cmd == nil {
		t.Fatal("API view Refresh returned no command")
	}
	msg := cmd()
	dm, ok := msg.(dataMsg)
	if !ok {
		t.Fatalf("source result = %T", msg)
	}
	tv.Update(dm)
	if len(tv.rows) != 1 || catalog.Str(tv.rows[0], "name") != "checkout availability" {
		t.Fatalf("rows = %v", tv.rows)
	}
	if gotScope.Timeframe.Label != a.tf.Label {
		t.Errorf("source received scope %+v", gotScope)
	}
	if echo := tv.Echo(); echo != "dtctl get slos" {
		t.Errorf("echo = %q", echo)
	}
	// Facets need fieldsSummary — refused on API views with a status message.
	if cmd := tv.handleKey(key("f")); cmd == nil {
		t.Error("facets on an API view must answer with a status")
	} else if sm, ok := cmd().(statusMsg); !ok || !sm.isErr {
		t.Errorf("facets on an API view = %v", cmd())
	}
}
func TestAPIViewMissingSourceErrors(t *testing.T) {
	a := testApp(t, "home")
	a.ds.sources = nil
	tv := newTableView(a.ds, catalog.Lookup("slos"), catalog.Scope{Timeframe: a.tf})
	msg := tv.Refresh()()
	dm, ok := msg.(dataMsg)
	if !ok || dm.err == nil || !strings.Contains(dm.err.Error(), "not wired") {
		t.Fatalf("missing source must surface an error, got %#v", msg)
	}
}

// runTick delivers one auto-refresh tick and returns what it set in motion:
// the answers of the queries it issued, undelivered, and whether the next
// tick was armed.
func runTick(t *testing.T, a *app) (answers []dataMsg, rearmed bool) {
	t.Helper()
	_, cmd := a.Update(refreshTickMsg{gen: a.refreshGen})
	var walk func(tea.Cmd)
	walk = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		case dataMsg:
			answers = append(answers, msg)
		case refreshTickMsg:
			rearmed = true
		}
	}
	walk(cmd)
	return answers, rearmed
}

// A query slower than the auto-refresh interval must still paint. A tick
// that finds the view waiting issues no second query, which would supersede
// the first one's answer on every tick and never show a result; it only
// arms the next tick.
func TestAutoRefreshWaitsForTheAnswerInFlight(t *testing.T) {
	a := testApp(t, "hosts")
	tv := a.top().(*tableView)
	seedRows(t, a, []map[string]any{{"id": "HOST-1", "name": "web-1"}})
	queries := 0
	a.ds.runFn = func(string) ([]map[string]any, error) {
		queries++
		return []map[string]any{{"id": "HOST-2", "name": "web-2"}}, nil
	}
	press(a, key("R")) // auto-refresh on

	first, rearmed := runTick(t, a)
	if len(first) != 1 || !rearmed {
		t.Fatalf("the first tick issued %d queries (rearmed %v), want one and the next tick", len(first), rearmed)
	}
	// The answer is still under way when the interval ends again.
	second, rearmed := runTick(t, a)
	if len(second) != 0 || queries != 1 {
		t.Errorf("a tick while the view waits issued a query (%d answers, %d queries in all)", len(second), queries)
	}
	if !rearmed {
		t.Error("a tick that waits must still arm the next one")
	}
	// The pause is the tick's alone: r refreshes at once, also while the
	// view waits (a query can hang for minutes before the client gives up).
	_, cmd := a.Update(key("r"))
	if cmd == nil || queries != 1 {
		t.Fatalf("r while the view waits: cmd=%v, %d queries so far", cmd != nil, queries)
	}
	byHand, ok := cmd().(dataMsg)
	if !ok || queries != 2 {
		t.Fatalf("r while the view waits issued no query (%d in all)", queries)
	}
	first[0] = byHand
	a.Update(first[0])
	if tv.loading || len(tv.rows) != 1 || catalog.Str(tv.rows[0], "id") != "HOST-2" {
		t.Errorf("the slow answer did not paint: loading=%v rows=%v", tv.loading, tv.rows)
	}
	// With the answer in, the next tick refreshes again.
	if third, _ := runTick(t, a); len(third) != 1 || queries != 3 {
		t.Errorf("the tick after the answer issued %d queries (%d in all), want one more", len(third), queries)
	}
}
