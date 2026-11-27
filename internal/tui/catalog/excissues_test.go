package catalog

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// issueSpan is one projected record of the exceptions view: the fields its
// query keeps, with one OneAgent-style Java exception event.
func issueSpan(n int, start, service, typ, message, stack string) map[string]any {
	return map[string]any{
		"start_time":            start,
		"span.id":               fmt.Sprintf("%016x", n),
		"trace.id":              fmt.Sprintf("%032x", n),
		"span.name":             "POST /api/cart",
		"endpoint.name":         "/api/cart",
		"dt.service.name":       service,
		"service.name":          nil,
		"dt.smartscape.service": "SERVICE-0000000000000A01",
		"span.events": []any{map[string]any{
			"span_event.name":       "exception",
			"exception.type":        typ,
			"exception.message":     message,
			"exception.stack_trace": stack,
		}},
	}
}

const (
	cartLockedStack = "com.example.shop.cart.CartService.addItem (CartService.java:120)\n" +
		"com.example.shop.web.CartController.post (CartController.java:48)"
	// The same bug after a deploy moved both lines.
	cartLockedStackMoved = "com.example.shop.cart.CartService.addItem (CartService.java:131)\n" +
		"com.example.shop.web.CartController.post (CartController.java:52)"
	ordersStack = "com.example.shop.db.Orders.load (Orders.java:10)"
)

func issuesOf(t *testing.T, rows []map[string]any) []ExceptionIssue {
	t.Helper()
	out := make([]ExceptionIssue, len(rows))
	for i, row := range rows {
		issue, ok := IssueOf(row)
		if !ok {
			t.Fatalf("row %d is not an issue row: %v", i, row)
		}
		out[i] = issue
	}
	return out
}

func TestExceptionsQueryShape(t *testing.T) {
	tf := Timeframe{Label: "2h"}
	dql := exceptionsSpec.Sample.Project(exceptionsSpec.Query(Scope{Timeframe: tf}))
	want := "fetch spans, from:now() - 2h\n" +
		"| filter iAny(span.events[][span_event.name] == \"exception\")\n" +
		"| sort start_time desc\n" +
		"| limit 1000\n" +
		"| fields start_time, span.id, trace.id, span.name, endpoint.name, dt.service.name, service.name, dt.smartscape.service, span.events"
	if dql != want {
		t.Errorf("unscoped query =\n%s\nwant\n%s", dql, want)
	}

	svc := Entity{ID: "SERVICE-0000000000000A01", Name: "cart", Type: "SERVICE"}
	scoped := exceptionsSpec.Query(Scope{Timeframe: tf, Entity: &svc})
	if !strings.Contains(scoped, "\n| filter dt.smartscape.service == toSmartscapeId(\"SERVICE-0000000000000A01\")\n| filter iAny(") {
		t.Errorf("a service scope must filter before the exception filter:\n%s", scoped)
	}
	if !exceptionsSpec.CanScope(tf, svc) {
		t.Error("a service pin must scope the view")
	}
	// Spans carry no scope field for a frontend: the pin must not be claimed.
	if exceptionsSpec.CanScope(tf, Entity{ID: "FRONTEND-1", Type: "FRONTEND"}) {
		t.Error("a frontend pin cannot scope spans")
	}
}

// The limit in the query is the sample's row cap: the header's "row limit"
// compares the fetched count to it.
func TestExceptionsQueryLimitIsTheSampleCap(t *testing.T) {
	dql := exceptionsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h"}})
	if want := fmt.Sprintf("\n| limit %d", ExceptionSampleRows); !strings.HasSuffix(dql, want) {
		t.Errorf("query does not end on %q:\n%s", want, dql)
	}
	if exceptionsSpec.Sample.Rows != 1000 || exceptionsSpec.Sample.Bytes != 8*1024*1024 {
		t.Errorf("sample caps = %d rows, %d bytes, want 1000 rows and 8 MB",
			exceptionsSpec.Sample.Rows, exceptionsSpec.Sample.Bytes)
	}
}

// A facet narrows what is sampled: it must land before the sort and the
// limit, on the whole span, not on the projected fields after them.
func TestExceptionsQueryNarrowsBeforeSampling(t *testing.T) {
	dql := exceptionsSpec.Sample.Project(ComposeQuery(exceptionsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h"}}),
		[]string{"timeout"}, []Facet{{Field: "k8s.namespace.name", Value: "shop"}}, false))
	lines := strings.Split(dql, "\n")
	at := func(prefix string) int {
		for i, l := range lines {
			if strings.HasPrefix(l, prefix) {
				return i
			}
		}
		t.Fatalf("no line starts with %q in:\n%s", prefix, dql)
		return -1
	}
	facet, search := at("| filter k8s.namespace.name"), at("| search")
	sortAt, limit, fields := at("| sort"), at("| limit"), at("| fields ")
	if !(search < facet && facet < sortAt && sortAt < limit && limit < fields) || fields != len(lines)-1 {
		t.Errorf("stage order is wrong (search %d, facet %d, sort %d, limit %d, fields %d of %d):\n%s",
			search, facet, sortAt, limit, fields, len(lines), dql)
	}
}

// A sample without a projection hands its query on as composed.
func TestSampleSpecProjectsOnlyNamedFields(t *testing.T) {
	const dql = "fetch spans\n| limit 5"
	if got := (&SampleSpec{}).Project(dql); got != dql {
		t.Errorf("Project without Fields = %q, want the query unchanged", got)
	}
	if got, want := (&SampleSpec{Fields: "a, b"}).Project(dql), dql+"\n| fields a, b"; got != want {
		t.Errorf("Project = %q, want %q", got, want)
	}
}

func TestGroupExceptionIssuesGroupsByKey(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:59:58.000000000Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", cartLockedStack),
		issueSpan(2, "2026-07-15T11:59:50.000000000Z", "orders", "java.sql.SQLException", "connection refused", ordersStack),
		issueSpan(3, "2026-07-15T11:59:40.000000000Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", cartLockedStackMoved),
	}
	rows := GroupExceptionIssues(spans)
	issues := issuesOf(t, rows)
	if len(issues) != 2 {
		t.Fatalf("issues = %d, want 2: %+v", len(issues), issues)
	}

	locked := issues[0]
	if locked.Count != 2 || locked.Type != "java.lang.IllegalStateException" {
		t.Errorf("first issue = %+v, want the cart lock with 2 spans", locked)
	}
	if !strings.HasPrefix(locked.Key, "e1:") || len(locked.Key) != 15 {
		t.Errorf("key = %q, want e1: and 12 hex characters", locked.Key)
	}
	if locked.Rung != "app frames" {
		t.Errorf("rung = %q, want app frames", locked.Rung)
	}
	if want := []string{"com.example.shop.cart.CartService.addItem", "com.example.shop.web.CartController.post"}; !reflect.DeepEqual(locked.Frames, want) {
		t.Errorf("frames = %q, want %q", locked.Frames, want)
	}
	if locked.Message != "cart <n> is locked" {
		t.Errorf("message = %q, want the parameterised message", locked.Message)
	}
	if want := []string{"cart", "checkout"}; !reflect.DeepEqual(locked.Services, want) {
		t.Errorf("services = %q, want %q", locked.Services, want)
	}
	if locked.Newest != "2026-07-15T11:59:58.000000000Z" || locked.Oldest != "2026-07-15T11:59:40.000000000Z" {
		t.Errorf("newest/oldest = %q / %q", locked.Newest, locked.Oldest)
	}

	// The row stands for its newest span: enter opens that span's trace.
	if got := Str(rows[0], "trace.id"); got != fmt.Sprintf("%032x", 1) {
		t.Errorf("row trace.id = %q, want the newest member's", got)
	}
	if Str(rows[0], "span.id") != fmt.Sprintf("%016x", 1) || Str(rows[0], "start_time") != locked.Newest {
		t.Errorf("row span.id/start_time = %q / %q, want the newest member's",
			Str(rows[0], "span.id"), Str(rows[0], "start_time"))
	}
	if exceptionsSpec.Trace(rows[0]) != Str(rows[0], "trace.id") {
		t.Error("the spec's Trace must read the row's trace id")
	}

	if issues[1].Count != 1 || issues[1].Type != "java.sql.SQLException" || issues[1].Key == locked.Key {
		t.Errorf("second issue = %+v", issues[1])
	}
}

// Loudest first; among equally loud issues the one seen last; then the key,
// so two refreshes of one sample never swap rows.
func TestGroupExceptionIssuesOrder(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:01Z", "a", "TypeOld", "", ""),
		issueSpan(2, "2026-07-15T11:00:03Z", "a", "TypeNew", "", ""),
		issueSpan(3, "2026-07-15T11:00:02Z", "a", "TypeLoud", "", ""),
		issueSpan(4, "2026-07-15T11:00:00Z", "a", "TypeLoud", "", ""),
		issueSpan(5, "2026-07-15T11:00:03Z", "a", "TypeTie", "", ""),
	}
	var got []string
	keys := map[string]string{}
	for _, issue := range issuesOf(t, GroupExceptionIssues(spans)) {
		got = append(got, issue.Type)
		keys[issue.Type] = issue.Key
	}
	tied := []string{"TypeNew", "TypeTie"}
	if keys["TypeNew"] > keys["TypeTie"] {
		tied = []string{"TypeTie", "TypeNew"}
	}
	want := append(append([]string{"TypeLoud"}, tied...), "TypeOld")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// Grail trims trailing zeros of a timestamp's fraction, so the newest member
// is found by instant, not by comparing the text.
func TestGroupExceptionIssuesComparesInstants(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "", ""),
		issueSpan(2, "2026-07-15T11:00:00.5Z", "a", "T", "", ""),
		issueSpan(3, "2026-07-15T10:59:59.25Z", "a", "T", "", ""),
	}
	issue := issuesOf(t, GroupExceptionIssues(spans))[0]
	if issue.Newest != "2026-07-15T11:00:00.5Z" || issue.Oldest != "2026-07-15T10:59:59.25Z" {
		t.Errorf("newest/oldest = %q / %q", issue.Newest, issue.Oldest)
	}
}

// Every fetched span is counted in exactly one row: the header's number and
// the sum of the counts are the same number.
func TestGroupExceptionIssuesCountsEverySpan(t *testing.T) {
	silent := issueSpan(3, "2026-07-15T11:00:02Z", "a", "", "", "")
	noEvents := map[string]any{"start_time": "2026-07-15T11:00:05Z", "span.id": "s", "trace.id": "t"}
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "", ""),
		issueSpan(2, "2026-07-15T11:00:01Z", "a", "T", "", ""),
		silent, noEvents,
	}
	issues := issuesOf(t, GroupExceptionIssues(spans))
	total := 0
	for _, issue := range issues {
		total += issue.Count
	}
	if total != len(spans) {
		t.Errorf("counts add up to %d, want %d", total, len(spans))
	}
	last := issues[len(issues)-1]
	if last.Key != "" || last.Count != 2 || last.Rung != "" {
		t.Errorf("spans whose events say nothing = %+v, want one keyless row of 2", last)
	}
	// The span that names no service adds none: the keyless row ran in "a"
	// alone, not in "a" and one more.
	if len(last.Services) != 1 || last.Services[0] != "a" {
		t.Errorf("services of the keyless row = %q, want only a", last.Services)
	}
}

// Spans that started in the same instant must not take turns describing
// their group: which trace enter opens, and which message the row shows,
// would then change between two refreshes of the same data, since Grail
// returns ties in no fixed order. The lower span id describes the group.
func TestGroupExceptionIssuesBreaksATieBySpanID(t *testing.T) {
	const at = "2026-07-15T11:00:00Z"
	a := issueSpan(7, at, "a", "T", "cart 1 failed for alice", cartLockedStack)
	b := issueSpan(3, at, "a", "T", "order 2 failed for bob", cartLockedStack)
	older := issueSpan(1, "2026-07-15T10:00:00Z", "a", "T", "an older one", cartLockedStack)
	for name, spans := range map[string][]map[string]any{
		"a first": {a, b, older}, "b first": {b, a, older}, "older first": {older, b, a}, "older between": {a, older, b},
	} {
		row := GroupExceptionIssues(spans)[0]
		issue := issuesOf(t, []map[string]any{row})[0]
		if Str(row, "span.id") != Str(b, "span.id") || Str(row, "trace.id") != Str(b, "trace.id") || issue.Message != "order <n> failed for bob" {
			t.Errorf("%s: the group is described by span %s (%q), want the lower id of the tie",
				name, Str(row, "span.id"), issue.Message)
		}
	}
}

// A start time that cannot be read is no instant: it is neither the group's
// first nor its last sighting, and its span describes the group only while
// no span with a readable time does.
func TestGroupExceptionIssuesIgnoresAnUnreadableTime(t *testing.T) {
	good := issueSpan(2, "2026-07-15T11:00:00Z", "a", "T", "the readable one", cartLockedStack)
	bad := issueSpan(1, "soon", "a", "T", "the unreadable one", cartLockedStack)
	missing := issueSpan(4, "", "a", "T", "the one without", cartLockedStack)
	delete(missing, "start_time")
	for name, spans := range map[string][]map[string]any{
		"unreadable first": {bad, good, missing}, "unreadable last": {good, missing, bad},
	} {
		row := GroupExceptionIssues(spans)[0]
		issue := issuesOf(t, []map[string]any{row})[0]
		if issue.Newest != "2026-07-15T11:00:00Z" || issue.Oldest != "2026-07-15T11:00:00Z" {
			t.Errorf("%s: newest/oldest = %q / %q, want the one readable time", name, issue.Newest, issue.Oldest)
		}
		if Str(row, "span.id") != Str(good, "span.id") || issue.Message != "the readable one" || Str(row, "start_time") != "2026-07-15T11:00:00Z" {
			t.Errorf("%s: described by span %s (%q)", name, Str(row, "span.id"), issue.Message)
		}
		for _, fact := range PreviewFacts(row) {
			if fact.Label == "first seen" {
				t.Errorf("%s: the preview shows a first sighting %q beside one readable time", name, fact.Value)
			}
		}
	}
	// With no readable time at all the row claims none, and the lower span
	// id describes it.
	row := GroupExceptionIssues([]map[string]any{missing, bad})[0]
	issue := issuesOf(t, []map[string]any{row})[0]
	if issue.Newest != "" || issue.Oldest != "" || Str(row, "span.id") != Str(bad, "span.id") {
		t.Errorf("no readable time: newest/oldest = %q / %q, span %s", issue.Newest, issue.Oldest, Str(row, "span.id"))
	}
}

// A row outlives the fetched spans, so none of its strings may be the
// span's own: a record producer that cuts its strings out of one buffer
// would otherwise keep the whole response alive behind a row.
func TestGroupExceptionIssuesRowsShareNoStringWithTheSpans(t *testing.T) {
	// Every string of the two spans is cut out of one buffer, as a decoder
	// that does not copy would hand them over.
	buffer := strings.Repeat("x", 64<<10) +
		"2026-07-15T11:00:00Z|2026-07-15T10:00:00Z|span-a|span-b|trace-a|trace-b|cart|" +
		"com.example.Boom|the cart is locked|" + cartLockedStack
	cut := func(part string) string {
		at := strings.Index(buffer, part)
		return buffer[at : at+len(part)]
	}
	span := func(start, id, trace string) map[string]any {
		return map[string]any{"start_time": cut(start), "span.id": cut(id), "trace.id": cut(trace),
			"dt.service.name": cut("cart"),
			"span.events": []any{map[string]any{"span_event.name": "exception", "exception.type": cut("com.example.Boom"),
				"exception.message": cut("the cart is locked"), "exception.stack_trace": cut(cartLockedStack)}}}
	}
	row := GroupExceptionIssues([]map[string]any{
		span("2026-07-15T11:00:00Z", "span-a", "trace-a"), span("2026-07-15T10:00:00Z", "span-b", "trace-b"),
	})[0]
	issue := issuesOf(t, []map[string]any{row})[0]
	lo := uintptr(unsafe.Pointer(unsafe.StringData(buffer)))
	kept := map[string]string{
		"Newest": issue.Newest, "Oldest": issue.Oldest, "Services[0]": issue.Services[0],
		"Type": issue.Type, "EventType": issue.EventType, "Message": issue.Message,
		"start_time": Str(row, "start_time"), "span.id": Str(row, "span.id"), "trace.id": Str(row, "trace.id"),
	}
	for i, frame := range issue.Frames {
		kept[fmt.Sprintf("Frames[%d]", i)] = frame
	}
	if len(issue.Frames) == 0 {
		t.Fatal("the stack gave no frames: the case does not cover them")
	}
	for name, text := range kept {
		if text == "" {
			t.Errorf("%s is empty", name)
		}
		if at := uintptr(unsafe.Pointer(unsafe.StringData(text))); at >= lo && at < lo+uintptr(len(buffer)) {
			t.Errorf("%s points into the fetched buffer: the row keeps it alive", name)
		}
	}
}

// The cell names one service: the one most of the group's spans ran in, not
// the first by name. A span that names its service in service.name alone
// (no OneAgent) counts like any other.
func TestGroupExceptionIssuesOrdersServicesBySpans(t *testing.T) {
	otel := issueSpan(4, "2026-07-15T11:00:00Z", "", "T", "", "")
	otel["dt.service.name"], otel["service.name"] = nil, "aardvark"
	spans := []map[string]any{
		otel,
		issueSpan(1, "2026-07-15T11:00:01Z", "web", "T", "", ""),
		issueSpan(2, "2026-07-15T11:00:02Z", "web", "T", "", ""),
		issueSpan(3, "2026-07-15T11:00:03Z", "batch", "T", "", ""),
	}
	issue := issuesOf(t, GroupExceptionIssues(spans))[0]
	if want := []string{"web", "aardvark", "batch"}; !reflect.DeepEqual(issue.Services, want) {
		t.Errorf("services = %q, want %q", issue.Services, want)
	}
}

// The filter finds a row by what its preview pane shows and its cells do
// not: a service the cell hides, and the type the event recorded.
func TestExceptionIssueMatchTextNamesWhatTheCellsHide(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "m", cartLockedStack),
		issueSpan(2, "2026-07-15T11:59:57Z", "cart", "java.lang.IllegalStateException", "m", cartLockedStack),
		issueSpan(3, "2026-07-15T11:59:40Z", "checkout", "java.lang.IllegalStateException", "m", cartLockedStack),
	})
	issue := issuesOf(t, rows)[0]
	issue.EventType = "org.example.Wrapper"
	rows[0][exceptionIssueKey] = issue
	text := exceptionsSpec.Sample.Match(rows[0])
	for _, want := range []string{issue.Key, "java.lang.IllegalStateException", "org.example.Wrapper", "cart", "checkout"} {
		if !strings.Contains(text, want) {
			t.Errorf("match text %q lacks %q", text, want)
		}
	}
}

// Two sightings within one second print the same clock time: one line.
func TestPreviewFactsExceptionIssueHidesAFirstSeenThatPrintsAlike(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58.9Z", "cart", "T", "", ""),
		issueSpan(2, "2026-07-15T11:59:58.1Z", "cart", "T", "", ""),
	})
	for _, fact := range PreviewFacts(rows[0]) {
		if fact.Label == "first seen" {
			t.Errorf("first seen %q repeats last seen", fact.Value)
		}
	}
}

func TestGroupExceptionIssuesEmpty(t *testing.T) {
	if rows := GroupExceptionIssues(nil); len(rows) != 0 {
		t.Errorf("no spans grouped into %d rows", len(rows))
	}
}

// Grouping runs off the update loop on records nothing else holds yet, and
// its rows must not keep them alive: it reads, and writes nothing back.
func TestGroupExceptionIssuesLeavesTheSpansAlone(t *testing.T) {
	span := issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "m", cartLockedStack)
	keys := len(span)
	rows := GroupExceptionIssues([]map[string]any{span})
	if len(span) != keys {
		t.Errorf("grouping added keys to the span: %v", span)
	}
	if _, ok := rows[0]["span.events"]; ok {
		t.Error("an issue row carries the span's events")
	}
}

func TestIssueOfOtherRecords(t *testing.T) {
	if _, ok := IssueOf(exceptionSpan()); ok {
		t.Error("a span is not an issue row")
	}
	if _, ok := IssueOf(nil); ok {
		t.Error("nil is not an issue row")
	}
}

func TestExceptionIssueColumns(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", cartLockedStack),
		issueSpan(2, "2026-07-15T11:59:40Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", cartLockedStackMoved),
		issueSpan(3, "2026-07-15T11:59:30Z", "checkout", "", "", ""),
	})
	cell := func(row map[string]any, title string) string {
		for _, c := range exceptionsSpec.Columns {
			if c.Title == title {
				return c.Text(row)
			}
		}
		t.Fatalf("no column %q", title)
		return ""
	}
	for title, want := range map[string]string{
		"COUNT":     "2",
		"EXCEPTION": "lang.IllegalStateException",
		"MESSAGE":   "cart <n> is locked",
		"SERVICE":   "+1 cart",
		"BY":        "app",
	} {
		if got := cell(rows[0], title); got != want {
			t.Errorf("%s = %q, want %q", title, got, want)
		}
	}
	if got := cell(rows[1], "EXCEPTION"); got != "(no key)" {
		t.Errorf("keyless EXCEPTION = %q", got)
	}
	// Neither a message nor a frame: the cell is empty.
	if got := cell(rows[1], "MESSAGE"); got != "" {
		t.Errorf("keyless MESSAGE = %q", got)
	}
	// COUNT sorts as a number, SERVICE by the name and not by the "+1".
	for _, c := range exceptionsSpec.Columns {
		switch c.Title {
		case "COUNT":
			if c.Sort(rows[0]) != 2 {
				t.Errorf("COUNT sort key = %v, want 2", c.Sort(rows[0]))
			}
		case "SERVICE":
			if c.Sort(rows[0]) != "cart" || c.Sort(rows[1]) != "checkout" {
				t.Errorf("SERVICE sort keys = %v, %v", c.Sort(rows[0]), c.Sort(rows[1]))
			}
		}
	}
}

// LAST sorts by the instant, not by its text: Grail trims a fraction's
// trailing zeros, so the raw spellings would put 11:59:58.5 before 11:59:58.
func TestExceptionIssueLastSortsByInstant(t *testing.T) {
	var last Column
	for _, c := range exceptionsSpec.Columns {
		if c.Title == "LAST" {
			last = c
		}
	}
	key := func(start string) any {
		return last.Sort(GroupExceptionIssues([]map[string]any{issueSpan(1, start, "a", "T", "", "")})[0])
	}
	inOrder := []string{
		"2026-07-15T11:59:57.999999999Z",
		"2026-07-15T11:59:58Z",
		"2026-07-15T13:59:58.25+02:00", // 11:59:58.25Z, written two zones east
		"2026-07-15T11:59:58.5Z",
		"2026-07-15T12:00:00Z",
	}
	for i := 1; i < len(inOrder); i++ {
		a, aok := key(inOrder[i-1]).(time.Time)
		b, bok := key(inOrder[i]).(time.Time)
		if !aok || !bok || !a.Before(b) {
			t.Errorf("sort key of %s (%v) is not an instant before that of %s (%v)", inOrder[i-1], a, inOrder[i], b)
		}
	}
	// An unreadable time has no key: the table sinks empty keys.
	if k := key("soon"); k != nil {
		t.Errorf("sort key of an unreadable time = %v, want none", k)
	}
}

// The count of further services must survive the cell's truncation: the
// column is 12 wide, and a long first name would push a trailing count out.
func TestExceptionIssueServiceCellLeadsWithItsCount(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "checkout-service-east-01", "T", "", ""),
		issueSpan(2, "2026-07-15T11:59:57Z", "checkout-service-west-02", "T", "", ""),
		issueSpan(3, "2026-07-15T11:59:56Z", "inventory", "T", "", ""),
	})
	for _, c := range exceptionsSpec.Columns {
		if c.Title == "SERVICE" {
			if got := c.Text(rows[0]); !strings.HasPrefix(got, "+2 ") || got != "+2 checkout-service-east-01" {
				t.Errorf("SERVICE = %q, want the count of further services first", got)
			}
		}
	}
}

// A message with no frames to show falls back to the throw site, so a row
// never has an empty middle.
func TestExceptionIssueMessageColumnFallsBackToTheThrowSite(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "", cartLockedStack),
	})
	for _, c := range exceptionsSpec.Columns {
		if c.Title == "MESSAGE" {
			if got := c.Text(rows[0]); got != "at com.example.shop.cart.CartService.addItem" {
				t.Errorf("MESSAGE = %q, want the throw site", got)
			}
		}
	}
}

func TestExceptionIssueYank(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "T", "", ""),
		issueSpan(2, "2026-07-15T11:59:58Z", "cart", "", "", ""),
	})
	text, label := exceptionsSpec.Yank(rows[0])
	if !strings.HasPrefix(text, "e1:") || label != "issue key "+text {
		t.Errorf("yank = %q, %q", text, label)
	}
	if text, _ := exceptionsSpec.Yank(rows[1]); text != "" {
		t.Errorf("a keyless row yanked %q", text)
	}
}

func TestPreviewFactsExceptionIssue(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", cartLockedStack),
		issueSpan(2, "2026-07-15T11:40:00Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", cartLockedStackMoved),
	})
	if got := PreviewTitle(rows[0]); got != "java.lang.IllegalStateException" {
		t.Errorf("title = %q", got)
	}
	facts := PreviewFacts(rows[0])
	issue, _ := IssueOf(rows[0])
	want := []PreviewFact{
		{Label: "issue", Value: issue.Key},
		{Label: "in sample", Value: "2 spans"},
		{Label: "grouped by", Value: "app frames"},
		{Label: "message", Value: "cart <n> is locked", Wrap: true},
		{Label: "thrown at", Value: "com.example.shop.cart.CartService.addItem", Wrap: true},
		{Label: "then", Value: "com.example.shop.web.CartController.post", Wrap: true},
		{Label: "services", Value: "cart, checkout"},
		{Label: "last seen", Value: FormatTime("2026-07-15T11:59:58Z")},
		{Label: "first seen", Value: FormatTime("2026-07-15T11:40:00Z")},
	}
	if !reflect.DeepEqual(facts, want) {
		t.Errorf("facts =\n%+v\nwant\n%+v", facts, want)
	}
}

// One span was seen once: no first-seen line repeats the last. And where
// the span's event recorded an outer exception of the one in the key, the
// pane names both.
func TestPreviewFactsExceptionIssueOfOneWrappedSpan(t *testing.T) {
	wrapped := "java.lang.RuntimeException: request failed\n" +
		"\tat com.example.shop.web.CartController.post(CartController.java:48)\n" +
		"Caused by: java.lang.IllegalStateException: cart 4411 is locked\n" +
		"\tat com.example.shop.cart.CartService.addItem(CartService.java:120)\n" +
		"\t... 1 more"
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.RuntimeException", "request failed", wrapped),
	})
	if got := PreviewTitle(rows[0]); got != "java.lang.IllegalStateException" {
		t.Errorf("title = %q, want the root cause the key is built on", got)
	}
	facts := PreviewFacts(rows[0])
	if got := factByLabel(facts, "recorded as").Value; got != "java.lang.RuntimeException" {
		t.Errorf("recorded as = %q", got)
	}
	if got := factByLabel(facts, "in sample").Value; got != "1 span" {
		t.Errorf("in sample = %q", got)
	}
	if got := factByLabel(facts, "first seen"); got.Label != "" {
		t.Errorf("a single span has a first-seen line: %+v", got)
	}
	if got := factByLabel(facts, "last seen").Value; got == "" {
		t.Error("no last-seen line")
	}
}

// The command bar opens its first match, and equal matches rank in registry
// order. The exceptions view must not take the prefixes people already type
// for the events view.
func TestExceptionsViewLeavesEventsItsPrefixes(t *testing.T) {
	for input, want := range map[string]string{
		"e": "events", "ev": "events",
		"ex": "exceptions", "exc": "exceptions", "exception": "exceptions", "exceptions": "exceptions",
		"exe": "executions", "exec": "executions",
	} {
		if got := Match(input); len(got) == 0 || got[0].Name != want {
			t.Errorf(":%s opens %v, want %s", input, got, want)
		}
	}
}

// The preview pane names a row the way its EXCEPTION cell does: an event
// with a message and no type is an "exception", and the row of spans whose
// events recorded nothing says so instead of posing as an exception type.
func TestPreviewTitleOfRowsWithoutAType(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "", "disk full", ""),
		issueSpan(2, "2026-07-15T11:59:57Z", "cart", "", "", ""),
	})
	for i, want := range []string{"exception", "(no key)"} {
		if got := PreviewTitle(rows[i]); got != want {
			t.Errorf("row %d: title = %q, want %q", i, got, want)
		}
		if got := exceptionsSpec.Columns[2].Text(rows[i]); got != want {
			t.Errorf("row %d: EXCEPTION cell = %q, want %q", i, got, want)
		}
	}
}

// A span has no key when none of its exception events gives a type, a
// message that is non-empty once parameterised, or a frame a reader can
// use. Such spans recorded something, but nothing to group by: they are
// counted in the one keyless last row, whatever its count, and that row
// does not claim that nothing was recorded.
func TestGroupExceptionIssuesCountsEverySpanWithoutAKeyInTheLastRow(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "", ""),
		issueSpan(2, "2026-07-15T11:00:05Z", "a", "", " \t ", ""),             // a message of white space
		issueSpan(3, "2026-07-15T11:00:04Z", "b", "", "", "no frame in here"), // a stack no reader can use
		issueSpan(4, "2026-07-15T11:00:03Z", "b", "", "", ""),                 // nothing at all
	}
	rows := GroupExceptionIssues(spans)
	issues := issuesOf(t, rows)
	if len(issues) != 2 {
		t.Fatalf("issues = %+v, want one keyed and one keyless", issues)
	}
	last := issues[1]
	if issues[0].Key == "" || last.Key != "" || last.Count != 3 || last.Rung != "" {
		t.Errorf("issues = %+v, want the keyless row last with 3 spans though it is the loudest", issues)
	}
	if issues[0].Count+last.Count != len(spans) {
		t.Errorf("counts add up to %d, want %d", issues[0].Count+last.Count, len(spans))
	}
	if got := Str(rows[1], "span.id"); got != Str(spans[1], "span.id") {
		t.Errorf("the keyless row opens span %q, want its newest", got)
	}
	spec := Lookup("exceptions")
	for _, c := range spec.Columns {
		if c.Title == "EXCEPTION" {
			if got := c.Text(rows[1]); got != "(no key)" {
				t.Errorf("keyless EXCEPTION = %q", got)
			}
		}
	}
	if text, _ := spec.Yank(rows[1]); text != "" {
		t.Errorf("the keyless row yanked %q", text)
	}
}
