package tui

import (
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// issueSpanRecord is one record of the exceptions view's query: the fields
// it projects, with one OneAgent-style Java exception event.
func issueSpanRecord(n int, start, service, typ, message, stack string) map[string]any {
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
	issueCartStack = "com.example.shop.cart.CartService.addItem (CartService.java:120)\n" +
		"com.example.shop.web.CartController.post (CartController.java:48)"
	issueOrdersStack = "com.example.shop.db.Orders.load (Orders.java:10)"
)

// issueSpans is a small sample: the cart lock twice, from two services, and
// one database error between them.
func issueSpans() []map[string]any {
	return []map[string]any{
		issueSpanRecord(1, "2026-07-15T11:59:58.000000000Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", issueCartStack),
		issueSpanRecord(2, "2026-07-15T11:59:50.000000000Z", "orders", "java.sql.SQLException", "connection refused", issueOrdersStack),
		issueSpanRecord(3, "2026-07-15T11:59:40.000000000Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", issueCartStack),
	}
}

// manySpans is a sample of n spans of one issue, one second apart, newest
// first, ending at 11:59:59.
func manySpans(n int) []map[string]any {
	newest := time.Date(2026, 7, 15, 11, 59, 59, 0, time.UTC)
	spans := make([]map[string]any, n)
	for i := range spans {
		at := newest.Add(-time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		spans[i] = issueSpanRecord(i+1, at, "cart", "java.lang.IllegalStateException", "cart is locked", issueCartStack)
	}
	return spans
}

func warning(notificationType, message string) []exec.QueryNotification {
	return []exec.QueryNotification{{Severity: "WARNING", NotificationType: notificationType, Message: message}}
}

// response is what the executor hands back for a query: records, and the
// notifications in Grail's metadata.
func response(records []map[string]any, notices []exec.QueryNotification) *exec.DQLQueryResponse {
	return &exec.DQLQueryResponse{Records: records,
		Metadata: &exec.DQLMetadata{Grail: &exec.GrailMetadata{Notifications: notices}}}
}

// seedSample lands spans in the top view the way its fetch command would:
// grouped, with the facts of the sample they were grouped from.
func seedSample(t *testing.T, a *app, spans []map[string]any, notices []exec.QueryNotification) *tableView {
	t.Helper()
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Sample == nil {
		t.Fatalf("top view is %T, want a sampled table", a.top())
	}
	tv.seq++
	tv.loading = true
	rows, facts := takeSample(tv.spec.Sample, sampleWindow(tv.scope.Timeframe), spans, notices, false)
	a.Update(dataMsg{owner: tv, seq: tv.seq, dql: tv.DQL(), records: rows, sample: facts,
		notices: notices, elapsed: time.Second})
	return tv
}

// The fetch command groups: what reaches the update loop is the issue rows
// and the facts of the sample, never the spans.
func TestExceptionsViewGroupsInTheFetchCommand(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	a.ds.runFn = func(string) ([]map[string]any, error) { return issueSpans(), nil }

	msg, ok := tv.Refresh()().(dataMsg)
	if !ok {
		t.Fatal("Refresh did not issue the list query")
	}
	if msg.sample == nil || msg.sample.fetched != 3 {
		t.Fatalf("sample facts = %+v, want 3 fetched", msg.sample)
	}
	if len(msg.records) != 2 {
		t.Fatalf("the command delivered %d records, want 2 issue rows", len(msg.records))
	}
	for _, rec := range msg.records {
		if _, isIssue := catalog.IssueOf(rec); !isIssue {
			t.Fatalf("delivered record is not an issue row: %v", rec)
		}
	}

	a.Update(msg)
	if len(tv.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tv.rows))
	}
	out := screen(a)
	for _, want := range []string{"all 3 exception spans · last 2h", "lang.IllegalStateException", "cart <n> is locked", "+1 cart", "grouped by: app frames"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
}

// The projection is the last stage of the query the view runs, echoes and
// reveals: a search and a facet land before the limit, on the whole span.
func TestExceptionsViewProjectsBehindItsNarrowing(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	tv.searches = []string{"timeout"}
	tv.facets = []catalog.Facet{{Field: "k8s.namespace.name", Value: "shop"}}
	tv.Refresh()
	want := "fetch spans, from:now() - 2h\n" +
		"| search \"*timeout*\"\n" +
		"| filter iAny(span.events[][span_event.name] == \"exception\")\n" +
		"| filter k8s.namespace.name == \"shop\"\n" +
		"| sort start_time desc\n" +
		"| limit 1000\n" +
		"| fields start_time, span.id, trace.id, span.name, endpoint.name, dt.service.name, service.name, dt.smartscape.service, span.events"
	if tv.DQL() != want {
		t.Errorf("composed query =\n%s\nwant\n%s", tv.DQL(), want)
	}
}

func TestExceptionsViewAsksForBothCaps(t *testing.T) {
	d := &dataSource{segments: []exec.FilterSegmentRef{{ID: "uid-a"}}}
	opts := d.sampleOpts(catalog.Lookup("exceptions").Sample)
	if opts.MaxResultRecords != catalog.ExceptionSampleRows || opts.MaxResultBytes != catalog.ExceptionSampleBytes {
		t.Errorf("caps = %d records, %d bytes", opts.MaxResultRecords, opts.MaxResultBytes)
	}
	if len(opts.Segments) != 1 || opts.ClientContext != clientContext {
		t.Errorf("a sample query must run like any other: %+v", opts)
	}
	// No metric catalogue lookup rides on a span query.
	if opts.MetadataFields != nil {
		t.Errorf("a sample query asked for metadata %q", opts.MetadataFields)
	}
}

// The header says what the counts are counts of: how many spans arrived, how
// far back they reach and what cut them short.
func TestSampleLine(t *testing.T) {
	pinSnapshotClock(t)
	spec := catalog.Lookup("exceptions").Sample
	sizeLimit := warning("BYTE_LIMIT_ADDED", "Your result has been limited because the maximum bytes (8388608) were reached.")
	scanLimit := warning("SCAN_LIMIT_GBYTES", "Your execution was stopped after 500 gigabytes of data were scanned.")
	rowLimit := warning("RESULT_LIMIT_RECORDS", "")
	consumption := warning("QUERY_CONSUMPTION_LIMIT", "")
	sampling := warning("SAMPLING_APPLIED", "")
	for _, tc := range []struct {
		name    string
		spans   []map[string]any
		notices []exec.QueryNotification
		want    string
		warn    bool
	}{
		{"whole", issueSpans(), nil, "all 3 exception spans · last 2h", false},
		{"one", issueSpans()[:1], nil, "1 exception span · last 2h", false},
		// A full sample may be all there is: nothing says older spans exist.
		{"row limit", manySpans(catalog.ExceptionSampleRows), nil,
			"row limit: newest 1000 exception spans back to 11:43:20; any older ones are not counted · last 2h", true},
		{"one short of the limit", manySpans(catalog.ExceptionSampleRows - 1), nil,
			"all 999 exception spans · last 2h", false},
		{"row limit announced", manySpans(5), rowLimit,
			"row limit: newest 5 exception spans back to 11:59:55; any older ones are not counted · last 2h", true},
		// No scan gave up here: the result was cut and happens to be empty.
		{"row limit, nothing arrived", nil, rowLimit,
			"row limit: the result was cut and holds no exception spans · last 2h", true},
		{"size limit", manySpans(412), sizeLimit,
			"size limit: newest 412 exception spans back to 11:53:08; older ones are not counted · last 2h", true},
		{"scan limit", manySpans(5), scanLimit,
			"scan limit: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"timeout", manySpans(5), warning("FETCH_TIMEOUT", ""),
			"fetch timed out: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"scan limit, nothing found", nil, scanLimit,
			"scan limit: the scan gave up before it found exception spans; narrow the timeframe (t) · last 2h", true},
		{"timeout, nothing found", nil, warning("FETCH_TIMEOUT", ""),
			"fetch timed out: the scan gave up before it found exception spans; narrow the timeframe (t) · last 2h", true},
		{"size limit, nothing fits", nil, sizeLimit,
			"size limit: no exception span fit · last 2h", true},
		// A scan that stopped early outranks the row limit: the rows that
		// arrived are then not the newest of the timeframe.
		{"scan limit on a full sample", manySpans(catalog.ExceptionSampleRows), scanLimit,
			"scan limit: 1000 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		// Spans without a readable start time: the line claims no reach.
		{"size limit, no times", []map[string]any{{"span.id": "a"}, {"span.id": "b", "start_time": "soon"}}, sizeLimit,
			"size limit: newest 2 exception spans; older ones are not counted · last 2h", true},
		// A cut is a cut whatever severity Grail files it under.
		{"a cut filed as information", issueSpans(), []exec.QueryNotification{{Severity: "INFO", NotificationType: "SCAN_LIMIT_GBYTES"}},
			"scan limit: 3 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"a cut without a severity", issueSpans(), []exec.QueryNotification{{NotificationType: "RESULT_LIMIT_BYTES"}},
			"size limit: newest 3 exception spans back to 11:59:40; older ones are not counted · last 2h", true},
		{"execution time limit", manySpans(5), warning("FETCH_EXEC_TIME_LIMIT", ""),
			"fetch timed out: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"internal time limit, by its message", manySpans(5), warning("", "The query reached an internal time limit."),
			"fetch timed out: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		// A cause with no wording of its own claims neither "all" nor "newest".
		{"consumption limit", manySpans(5), consumption,
			"partial result: 5 exception spans of what was returned; narrow the timeframe (t) · last 2h", true},
		{"consumption limit, nothing returned", nil, consumption,
			"partial result: no exception spans in what was returned; narrow the timeframe (t) · last 2h", true},
		// It outranks a limit, which would claim the newest spans, and
		// yields to a cause that has its own wording.
		{"consumption limit on a full sample", manySpans(catalog.ExceptionSampleRows), consumption,
			"partial result: 1000 exception spans of what was returned; narrow the timeframe (t) · last 2h", true},
		{"consumption limit and a size cut", manySpans(5), append(sizeLimit, consumption...),
			"partial result: 5 exception spans of what was returned; narrow the timeframe (t) · last 2h", true},
		{"consumption limit and a scan limit", manySpans(5), append(consumption, scanLimit...),
			"scan limit: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		// A sampled read is not all of the timeframe either.
		{"sampling", issueSpans(), sampling,
			"sampled read: 3 exception spans in the part that was read · last 2h", true},
		{"sampling, one span", issueSpans()[:1], sampling,
			"sampled read: 1 exception span in the part that was read · last 2h", true},
		{"sampling, nothing read", nil, sampling,
			"sampled read: no exception spans in the part that was read · last 2h", true},
		{"sampling and a size cut", manySpans(412), append(sampling, sizeLimit...),
			"size limit: newest 412 exception spans back to 11:53:08; older ones are not counted · last 2h", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, facts := takeSample(spec, sampleWindow(catalog.Timeframe{Label: "2h"}), tc.spans, tc.notices, false)
			text, warn := facts.line()
			if text != tc.want || warn != tc.warn {
				t.Errorf("line = %q (warn %v)\nwant   %q (warn %v)", text, warn, tc.want, tc.warn)
			}
		})
	}
}

// Nothing fetched and nothing cut is an ordinary empty view: no header line
// claims a sample.
func TestSampleLineEmptyAndWhole(t *testing.T) {
	_, facts := takeSample(catalog.Lookup("exceptions").Sample, "last 2h", nil, nil, false)
	if text, _ := facts.line(); text != "" {
		t.Errorf("an empty, uncut sample wrote %q", text)
	}
	a := testApp(t, "exceptions")
	seedSample(t, a, nil, nil)
	if out := screen(a); !strings.Contains(out, "no data in timeframe (last 2h)") || strings.Contains(out, "exception spans") {
		t.Errorf("empty view:\n%s", out)
	}
}

// Whatever dtctl reads as a cut result, and a sampled read, must never show
// as the whole timeframe: not as "all N", and not as an empty timeframe.
func TestSampleNeverReadsWholeBesideACutOrSampledResult(t *testing.T) {
	for name, n := range map[string]exec.QueryNotification{
		"execution time limit":   {Severity: "WARNING", NotificationType: "FETCH_EXEC_TIME_LIMIT"},
		"consumption limit":      {Severity: "WARNING", NotificationType: "QUERY_CONSUMPTION_LIMIT"},
		"internal time limit":    {Severity: "WARNING", Message: "The query reached an internal time limit."},
		"scan limit as info":     {Severity: "INFO", NotificationType: "SCAN_LIMIT_GBYTES"},
		"scan limit, unlabelled": {NotificationType: "SCAN_LIMIT_GBYTES"},
		"sampling":               {Severity: "WARNING", NotificationType: "SAMPLING_APPLIED"},
	} {
		if name != "sampling" && !exec.ResultIsPartial(n) {
			t.Fatalf("%s: dtctl no longer reads this notification as a cut result; the case is stale", name)
		}
		for _, spans := range [][]map[string]any{issueSpans(), nil} {
			a := testApp(t, "exceptions")
			disablePreview(a)
			seedSample(t, a, spans, []exec.QueryNotification{n})
			out := screen(a)
			if strings.Contains(out, "all 3 ") || strings.Contains(out, "no data in timeframe") {
				t.Errorf("%s, %d spans: the screen claims the whole timeframe:\n%s", name, len(spans), out)
			}
			if !strings.Contains(out, "exception spans") {
				t.Errorf("%s, %d spans: no sample line:\n%s", name, len(spans), out)
			}
		}
	}
}

// The response's own sampled flag reaches the line without a notification:
// it is read in the fetch command, from the response that carried the spans.
func TestExceptionsSampledResponseIsNotCalledWhole(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	a.ds.runWith = func(string, exec.DQLExecuteOptions) (*exec.DQLQueryResponse, error) {
		resp := response(issueSpans(), nil)
		resp.Metadata.Grail.Sampled = true
		return resp, nil
	}
	a.Update(tv.Refresh()())
	if out := screen(a); !strings.Contains(out, "sampled read: 3 exception spans in the part that was read") {
		t.Errorf("a sampled response reads as whole:\n%s", out)
	}
}

func TestSampleWindowWording(t *testing.T) {
	if got := sampleWindow(catalog.Timeframe{Label: "24h"}); got != "last 24h" {
		t.Errorf("relative window = %q", got)
	}
	from := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	abs := catalog.AbsoluteTimeframe(from, from.Add(time.Hour), from.Add(2*time.Hour))
	if got := sampleWindow(abs); got != "2026-07-15 10:00→11:00 UTC" {
		t.Errorf("absolute window = %q", got)
	}
}

// The header describes the rows on screen. While a refetch for another
// timeframe is in flight those are still the old window's.
func TestSampleLineKeepsTheWindowOfItsRows(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)

	tv.SetTimeframe(catalog.Timeframe{Label: "24h", Dur: 24 * time.Hour}) // the fetch is not run
	if out := screen(a); !strings.Contains(out, "all 3 exception spans · last 2h") {
		t.Errorf("header changed before the new sample arrived:\n%s", out)
	}
	seedSample(t, a, issueSpans()[:1], nil)
	if out := screen(a); !strings.Contains(out, "1 exception span · last 24h") {
		t.Errorf("header did not follow the new sample:\n%s", out)
	}
}

// The header is a line of its own: the table gives it one row of its height.
func TestSampleLineTakesARowOfThePage(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	disablePreview(a)
	tv := a.top().(*tableView)
	seedSample(t, a, nil, nil)
	screen(a)
	bare := tv.pageSize()

	seedSample(t, a, manySpans(catalog.ExceptionSampleRows), nil)
	out := screen(a)
	if got := tv.pageSize(); got != bare-1 {
		t.Errorf("page size = %d with the header, %d without; want one less", got, bare)
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "row limit: newest 1000 exception spans") {
			if !strings.Contains(lines[i+1], "COUNT") {
				t.Errorf("the column header does not follow the sample line:\n%s", out)
			}
			return
		}
	}
	t.Errorf("no sample line:\n%s", out)
}

// With more issues than fit, the rows fill what the header leaves: of the
// 40 lines, the app's header and footer take two each, and the table's
// status line, sample line and column titles one each.
func TestSampleLineLeavesTheTableItsHeight(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	disablePreview(a)
	spans := make([]map[string]any, 80)
	for i := range spans {
		spans[i] = issueSpanRecord(i+1, "2026-07-15T11:59:58Z", "cart", fmt.Sprintf("ErrorKind%02d", i), "", "")
	}
	tv := seedSample(t, a, spans, nil)
	out := screen(a)
	if got := strings.Count(out, "ErrorKind"); got != 33 {
		t.Errorf("the table drew %d rows, want 33:\n%s", got, out)
	}
	if got := strings.Count(out, "\n") + 1; got != 40 {
		t.Errorf("screen has %d lines, want 40", got)
	}
	// The table itself keeps to the height it is given; the app would clip
	// an overflow and hide it.
	if got := strings.Count(tv.renderTable(120, 20), "\n") + 1; got != 20 {
		t.Errorf("a 20-line table rendered %d lines", got)
	}
}

// Enter opens the trace of the row's newest span, on that span.
func TestExceptionsEnterOpensTheNewestSpansTrace(t *testing.T) {
	a := testApp(t, "exceptions")
	seedSample(t, a, issueSpans(), nil)
	press(a, key("enter"))
	wf, ok := a.top().(*waterfallView)
	if !ok {
		t.Fatalf("enter opened %T, want the waterfall", a.top())
	}
	if want := fmt.Sprintf("%032x", 1); wf.traceID != want {
		t.Errorf("trace = %q, want the newest member's %q", wf.traceID, want)
	}
	if want := fmt.Sprintf("%016x", 1); wf.focusSpan != want {
		t.Errorf("focus span = %q, want %q", wf.focusSpan, want)
	}
	// The trace is fetched around the span's start, not over the view's
	// timeframe.
	if want := time.Date(2026, 7, 15, 11, 59, 58, 0, time.UTC); !wf.anchor.Equal(want) {
		t.Errorf("anchor = %v, want the newest member's start %v", wf.anchor, want)
	}
}

func TestExceptionsYankCopiesTheIssueKey(t *testing.T) {
	a := testApp(t, "exceptions")
	silent := issueSpanRecord(9, "2026-07-15T11:00:00Z", "cart", "", "", "")
	tv := seedSample(t, a, append(issueSpans(), silent), nil)

	text, label, ok := tv.YankText()
	issue, _ := catalog.IssueOf(tv.rows[0])
	if !ok || text != issue.Key || !strings.HasPrefix(text, "e1:") || label != "issue key "+text {
		t.Errorf("yank = %q, %q, %v", text, label, ok)
	}
	// The keyless row has no key to copy.
	tv.cursor = len(tv.rows) - 1
	if _, _, ok := tv.YankText(); ok {
		t.Error("the keyless row yanked something")
	}
	// A view that names no yank keeps the default.
	hosts := testApp(t, "hosts")
	seedRows(t, hosts, []map[string]any{{"id": "HOST-1", "name": "web-1"}})
	if _, _, ok := hosts.top().(*tableView).YankText(); ok {
		t.Error("a view without Yank must leave y to the app")
	}
}

// The facet picker offers the fields of the spans that were sampled, which
// the issue rows no longer carry.
func TestExceptionsFacetFieldsComeFromTheSample(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	got := tv.facetCandidates()
	want := []string{"dt.service.name", "dt.smartscape.service", "endpoint.name", "service.name", "span.id", "span.name", "start_time", "trace.id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facet fields = %q\nwant         %q", got, want)
	}
	if !tv.hasField("dt.service.name") || tv.hasField("k8s.namespace.name") {
		t.Error("hasField must answer from the sampled spans")
	}
	// The next sample replaces them.
	seedSample(t, a, nil, nil)
	if got := tv.facetCandidates(); len(got) != 0 {
		t.Errorf("facet fields of an empty sample = %q", got)
	}
}

// echoArgs pastes an echoed command into sh and returns the arguments dtctl
// would have been started with.
func echoArgs(t *testing.T, line string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	bin, work := t.TempDir(), t.TempDir()
	argv := filepath.Join(bin, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done >> '" + argv + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "dtctl"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := osexec.Command(sh, "-c", line)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("sh -c %q: err=%v output=%q", line, err, out)
	}
	if left, _ := os.ReadDir(work); len(left) > 0 {
		t.Fatalf("sh -c %q created %q: something besides dtctl ran", line, left[0].Name())
	}
	raw, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("sh -c %q never ran dtctl: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

// The echo is pasted into a shell. A facet value and a segment id come from
// the tenant's data, so neither may end the quoting, and the query must
// arrive as it ran.
func TestExceptionsEchoSurvivesTheShell(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	hostile := "it's; touch pwned $(touch pwned) `touch pwned`"
	tv.facets = []catalog.Facet{{Field: "span.name", Value: hostile}}
	a.ds.segments = []exec.FilterSegmentRef{{ID: "seg'; touch pwned #"}, {ID: "plain-id"}}
	tv.Refresh()

	echo := tv.Echo()
	if !strings.HasPrefix(echo, "dtctl query '") {
		t.Fatalf("echo = %q", echo)
	}
	got := echoArgs(t, echo)
	// The two limits the sample was fetched under go with it: without the
	// size limit a sample that was cut by it replays as a different set.
	want := []string{"query", tv.DQL(), "--max-result-records", "1000", "--max-result-bytes", "8388608",
		"-S", "seg'; touch pwned #", "-S", "plain-id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dtctl got %q\nwant      %q", got, want)
	}
	if !strings.Contains(tv.DQL(), "\n| filter ") || !strings.Contains(tv.DQL(), hostile) {
		t.Errorf("the query lost its lines or the value:\n%s", tv.DQL())
	}
}

// The footer has one line for the echo, however many the query has.
func TestExceptionsEchoIsOneLineInTheFooter(t *testing.T) {
	a := testApp(t, "exceptions")
	a.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	out := screen(a)
	if !strings.Contains(out, "≡ dtctl query 'fetch spans, from:now() - 2h | filter iAny(") {
		t.Errorf("footer echo is not flattened:\n%s", out)
	}
	if strings.Count(out, "\n") != 39 {
		t.Errorf("screen has %d lines, want 40", strings.Count(out, "\n")+1)
	}
}

// An issue key exists in dtui only: no span holds it. Typed into the filter
// it finds its row, and enter keeps the filter on the rows instead of
// sending the text to the server as a search no span can match.
func TestExceptionsFilterFindsAKeyAndEnterKeepsItLocal(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	issue, _ := catalog.IssueOf(tv.rows[0])
	before := tv.DQL()

	press(a, key("/"))
	if out := screen(a); !strings.Contains(out, "enter keep filter") || strings.Contains(out, "add server search") {
		t.Errorf("the filter hints promise a server search:\n%s", out)
	}
	typeText(a, issue.Key)
	if len(tv.rows) != 1 {
		t.Fatalf("typing the key left %d rows, want its one issue", len(tv.rows))
	}
	press(a, key("enter"))
	if len(tv.searches) != 0 || strings.Contains(tv.DQL(), issue.Key) || tv.DQL() != before {
		t.Errorf("enter sent the key to the server: searches=%q\n%s", tv.searches, tv.DQL())
	}
	if tv.filter != issue.Key || len(tv.rows) != 1 || tv.filtering {
		t.Errorf("enter did not keep the filter: filter=%q rows=%d filtering=%v", tv.filter, len(tv.rows), tv.filtering)
	}
	// The full type is findable too, though the column shortens it.
	tv.setFilter("java.sql.SQLException")
	if len(tv.rows) != 1 {
		t.Errorf("the full type matched %d rows, want 1", len(tv.rows))
	}
}

// ctrl+s is the explicit way to narrow what is sampled by text: a key every
// host delivers, the browser side panel included, which keeps Alt chords to
// itself. It adds a term to those already active.
func TestExceptionsCtrlSSearchesTheSpans(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	tv.searches = []string{"preset-term", "second"}
	press(a, key("/"))
	if out := screen(a); !strings.Contains(out, "ctrl+s server search") || strings.Contains(out, "alt+enter") {
		t.Errorf("the filter hints do not name ctrl+s:\n%s", out)
	}
	typeText(a, "locked")
	press(a, tea.KeyMsg{Type: tea.KeyCtrlS})
	if want := []string{"preset-term", "second", "locked"}; !reflect.DeepEqual(tv.searches, want) || tv.filter != "" || tv.filtering {
		t.Errorf("searches=%q filter=%q filtering=%v, want %q and no filter", tv.searches, tv.filter, tv.filtering, want)
	}
	if !strings.Contains(tv.DQL(), "\n| search \"*locked*\"\n") {
		t.Errorf("the search did not reach the query:\n%s", tv.DQL())
	}
	// Enter says which key searches.
	press(a, key("/"))
	typeText(a, "cart")
	press(a, key("enter"))
	if !strings.Contains(a.status, "ctrl+s searches the exception spans") {
		t.Errorf("status after enter = %q", a.status)
	}
	// On every other table ctrl+s adds a term as enter does.
	logs := testApp(t, "logs")
	lv := logs.top().(*tableView)
	lv.searches = []string{"first"}
	press(logs, key("/"))
	typeText(logs, "boom")
	press(logs, tea.KeyMsg{Type: tea.KeyCtrlS})
	if want := []string{"first", "boom"}; !reflect.DeepEqual(lv.searches, want) {
		t.Errorf("logs searches after ctrl+s = %q, want %q", lv.searches, want)
	}
}

// A sample that was cut short and holds nothing says only that: the table
// must not add that the timeframe has no data, which nothing showed.
func TestExceptionsEmptyCutSampleClaimsNoEmptyTimeframe(t *testing.T) {
	for _, notices := range [][]exec.QueryNotification{
		warning("SCAN_LIMIT_GBYTES", ""), warning("FETCH_TIMEOUT", ""), warning("BYTE_LIMIT_ADDED", ""),
	} {
		a := testApp(t, "exceptions")
		disablePreview(a)
		seedSample(t, a, nil, notices)
		out := screen(a)
		if strings.Contains(out, "no data in timeframe") {
			t.Errorf("%s: an empty cut sample reads as an empty timeframe:\n%s", notices[0].NotificationType, out)
		}
		if !strings.Contains(out, " limit: ") && !strings.Contains(out, "timed out: ") {
			t.Errorf("%s: no sample line:\n%s", notices[0].NotificationType, out)
		}
	}
}

// An issue last seen on another day shows its date and its time. Beside the
// preview pane the fixed columns are squeezed, and LAST must not give up the
// minute for it.
func TestExceptionsLastSeenKeepsItsTimeWhenSqueezed(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	seedSample(t, a, []map[string]any{
		issueSpanRecord(1, "2026-07-14T23:58:07.000000000Z", "cart", "java.lang.IllegalStateException", "cart 1 is locked", issueCartStack),
	}, nil)
	for _, size := range snapshotSizes {
		a.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		if out := screen(a); !strings.Contains(out, "  Jul 14 23:58  ") {
			t.Errorf("%dx%d: LAST lost part of the time:\n%s", size.w, size.h, out)
		}
	}
}

// The reach is nowhere else on the screen, so no width may cut it: not the
// preview pane at its narrowest, and not a reach on another day, which is
// longer by its date. Every width a terminal is likely to have is walked,
// with the longest reason that carries a reach.
func TestSampleLineKeepsItsReachAtEveryWidth(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	spans := manySpans(catalog.ExceptionSampleRows)
	spans[len(spans)-1]["start_time"] = "2026-07-14T21:59:00Z"
	seedSample(t, a, spans, warning("BYTE_LIMIT_ADDED", ""))
	want := "size limit: newest 1000 exception spans back to " + catalog.FormatTime("2026-07-14T21:59:00Z")
	if !strings.Contains(want, "Jul 14") {
		t.Fatalf("the case lost its date: %q", want)
	}
	for width := 80; width <= 200; width++ {
		a.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		if out := screen(a); !strings.Contains(out, want) {
			t.Fatalf("%d columns: the sample line lost its reach:\n%s", width, out)
		}
	}
}

// The line's facts are read from the records, not from their order: spans
// that arrive oldest first reach as far back as the same spans newest first.
func TestSampleLineReachIsTheOldestTimeInAnyOrder(t *testing.T) {
	pinSnapshotClock(t)
	spec := catalog.Lookup("exceptions").Sample
	spans := manySpans(5)
	slices.Reverse(spans)
	_, facts := takeSample(spec, "last 2h", spans, warning("BYTE_LIMIT_ADDED", ""), false)
	if text, _ := facts.line(); !strings.HasPrefix(text, "size limit: newest 5 exception spans back to 11:59:55;") {
		t.Errorf("line of spans delivered oldest first = %q", text)
	}
}

// With two notices the line above the table and the footer name the same
// reason, the gravest, in whichever order the notices arrive.
func TestSampleLineAndFooterNameTheSameReason(t *testing.T) {
	size := exec.QueryNotification{Severity: "WARNING", NotificationType: "BYTE_LIMIT_ADDED"}
	scan := exec.QueryNotification{Severity: "WARNING", NotificationType: "SCAN_LIMIT_GBYTES"}
	rows := exec.QueryNotification{Severity: "WARNING", NotificationType: "RESULT_LIMIT_RECORDS"}
	for _, tc := range []struct {
		notices      []exec.QueryNotification
		line, footer string
	}{
		{[]exec.QueryNotification{size, scan}, "scan limit: 3 exception spans of what was scanned", "Grail scan limit reached"},
		{[]exec.QueryNotification{scan, size}, "scan limit: 3 exception spans of what was scanned", "Grail scan limit reached"},
		{[]exec.QueryNotification{rows, size}, "size limit: newest 3 exception spans", "truncated to the size limit"},
		{[]exec.QueryNotification{size, rows}, "size limit: newest 3 exception spans", "truncated to the size limit"},
		{[]exec.QueryNotification{rows}, "row limit: newest 3 exception spans", "truncated to the row limit"},
	} {
		a := testApp(t, "exceptions")
		a.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
		seedSample(t, a, issueSpans(), tc.notices)
		out := screen(a)
		if !strings.Contains(out, tc.line) || !strings.Contains(out, tc.footer) {
			t.Errorf("%v: want the line %q and the footer %q:\n%s", tc.notices, tc.line, tc.footer, out)
		}
	}
}

// Both exits hand over the query that fetched the sample: spans, one per
// row, with no issue key and no count. Each says so where the user reads
// what happened, so the notebook or the pasted command is not taken for the
// grouped table.
func TestExceptionsExitsSayTheyHandOverTheUngroupedSample(t *testing.T) {
	var opened string
	a, err := newApp(Options{ContextName: "test", SafetyLevel: "readonly", Environment: "https://tenant.example.invalid",
		Launch: NavigateMsg{Target: "exceptions"}, OpenURL: func(u string) error { opened = u; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	a.ds.runFn = func(string) ([]map[string]any, error) { return nil, nil }
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	deliver(a, a.Init())
	tv := seedSample(t, a, issueSpans(), nil)
	const note = "(the sampled exception spans, ungrouped)"

	press(a, key("o"))
	if want := queryLink(a.opts.Environment, tv.DQL()); opened != want {
		t.Errorf("o opened %q\nwant     %q", opened, want)
	}
	if want := "opened query in a Notebook " + note; a.status != want {
		t.Errorf("status after o = %q, want %q", a.status, want)
	}
	press(a, key("c"))
	if want := "copied " + note + ": " + flatten(tv.Echo()); a.status != want {
		t.Errorf("status after c = %q\nwant             %q", a.status, want)
	}

	// Another view's exits are its rows and say nothing more.
	logs := testApp(t, "logs")
	press(logs, key("c"))
	if !strings.HasPrefix(logs.status, "copied: dtctl query ") {
		t.Errorf("logs status after c = %q", logs.status)
	}
}

// The fetch command itself asks for both caps and reads what cut the sample
// from the response that carried the spans.
func TestExceptionsFetchCommandCapsTheQueryAndReadsItsNotices(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	var ran []exec.DQLExecuteOptions
	a.ds.runWith = func(_ string, opts exec.DQLExecuteOptions) (*exec.DQLQueryResponse, error) {
		ran = append(ran, opts)
		return response(manySpans(412), warning("BYTE_LIMIT_ADDED", "")), nil
	}
	msg := tv.Refresh()().(dataMsg)
	if len(ran) != 1 || ran[0].MaxResultRecords != catalog.ExceptionSampleRows || ran[0].MaxResultBytes != catalog.ExceptionSampleBytes {
		t.Fatalf("the query ran with %+v, want one run with both caps", ran)
	}
	a.Update(msg)
	if out := screen(a); !strings.Contains(out, "size limit: newest 412 exception spans back to 11:53:08") {
		t.Errorf("the header does not name the size limit:\n%s", out)
	}
	// A failed fetch delivers its error and no sample.
	a.ds.runWith = func(string, exec.DQLExecuteOptions) (*exec.DQLQueryResponse, error) {
		return nil, fmt.Errorf("boom")
	}
	if msg := tv.Refresh()().(dataMsg); msg.err == nil || msg.sample != nil || msg.records != nil {
		t.Errorf("a failed fetch delivered %+v", msg)
	}
}

// A result for a superseded request is dropped: a slow, clean answer to an
// earlier refresh must not replace the cut sample of the later one, nor its
// line.
func TestExceptionsLateResultOfAnEarlierRefreshIsDropped(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	a.ds.runFn = func(string) ([]map[string]any, error) { return issueSpans(), nil }
	early := tv.Refresh() // not run yet: the slow one
	seedSample(t, a, manySpans(5), warning("SCAN_LIMIT_GBYTES", ""))

	a.Update(early())
	if tv.sample.fetched != 5 || len(tv.rows) != 1 {
		t.Errorf("the late result replaced the current sample: %+v, %d rows", tv.sample, len(tv.rows))
	}
	if out := screen(a); !strings.Contains(out, "scan limit: 5 exception spans of what was scanned") {
		t.Errorf("the line no longer describes the rows on screen:\n%s", out)
	}
}

// A refresh while issues are on screen: the commands group on their own
// goroutines, here even over the same span records, while the update loop
// draws, rewires the data source and takes results in whatever order they
// finish. Run under the race detector (make test).
func TestExceptionsRefreshWhileOnScreenIsRaceFree(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	shared := issueSample()
	a.ds.runFn = func(string) ([]map[string]any, error) { return shared, nil }
	seedSample(t, a, issueSpans(), nil)

	const refreshes = 6
	results := make(chan tea.Msg, refreshes)
	for i := range refreshes {
		cmd := tv.Refresh()
		go func() { results <- cmd() }()
		// What a segment toggle and a context switch write on the update
		// loop while the command runs: it must have taken its wiring when
		// it was created.
		a.ds.segments = []exec.FilterSegmentRef{{ID: fmt.Sprintf("uid-%d", i)}}
		a.ds.exec, a.ds.sources = nil, map[string]Source{}
		screen(a)
	}
	for range refreshes {
		a.Update(<-results)
		screen(a)
	}
	// Only the last refresh's result may have landed, whenever it finished.
	if tv.loading || tv.sample == nil || tv.sample.fetched != len(shared) || len(tv.rows) != 6 {
		t.Errorf("after the refreshes: loading=%v sample=%+v rows=%d", tv.loading, tv.sample, len(tv.rows))
	}
	for _, span := range shared {
		for k := range span {
			if strings.HasPrefix(k, "__") {
				t.Fatalf("grouping wrote %q into a span record", k)
			}
		}
	}
}

// The facet picker offers every scalar field any sampled span carried, also
// when the spans of one issue carry different ones.
func TestExceptionsFacetFieldsAreTheUnionOverTheSample(t *testing.T) {
	a := testApp(t, "exceptions")
	one := issueSpanRecord(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "cart 1 is locked", issueCartStack)
	two := issueSpanRecord(2, "2026-07-15T11:59:57Z", "cart", "java.lang.IllegalStateException", "cart 2 is locked", issueCartStack)
	delete(one, "endpoint.name")
	delete(two, "span.name")
	tv := seedSample(t, a, []map[string]any{one, two}, nil)
	if len(tv.rows) != 1 {
		t.Fatalf("rows = %d, want the one issue", len(tv.rows))
	}
	if !tv.hasField("endpoint.name") || !tv.hasField("span.name") {
		t.Errorf("facet fields = %q, want both spans' fields", tv.facetCandidates())
	}
}

// d opens the inspector on the row's real fields. The page is written to
// the history file, so it must not carry the issue: a key, a message and
// frames computed from spans are not a record of the tenant's, and the view
// stores no keys.
func TestExceptionsInspectedRowKeepsNoIssueInHistory(t *testing.T) {
	a := testApp(t, "exceptions")
	seedSample(t, a, issueSpans(), nil)
	press(a, key("d"))
	iv, ok := a.top().(*inspectorView)
	if !ok {
		t.Fatalf("d opened %T, want the inspector", a.top())
	}
	ref, ok := pageRefOf(iv)
	if !ok {
		t.Fatal("the inspector has no history codec")
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "__") || strings.Contains(string(raw), "e1:") {
		t.Errorf("the persisted page holds the issue:\n%s", raw)
	}
	// The page is named after the issue, not after the view it came from.
	if iv.title != "java.lang.IllegalStateException" {
		t.Errorf("inspector title = %q, want the issue's name", iv.title)
	}
	if catalog.Str(iv.rec, "span.id") == "" || catalog.Str(iv.rec, "trace.id") == "" || catalog.Str(iv.rec, "start_time") == "" {
		t.Errorf("the inspector lost the row's real fields: %v", iv.rec)
	}
	// The table's own row keeps its issue for the cells and the preview.
	press(a, key("esc"))
	if _, isIssue := catalog.IssueOf(a.top().(*tableView).selected()); !isIssue {
		t.Error("inspecting a row took the issue off the table's row")
	}
}

// Two issues inspected from one table are two history entries: an issue row
// has no title, timestamp or display id to tell them apart, so the page is
// identified by its span.
func TestExceptionsInspectedIssuesAreSeparateHistoryEntries(t *testing.T) {
	a := testApp(t, "exceptions")
	seedSample(t, a, issueSpans(), nil)
	press(a, key("d"))
	first := len(a.hist.entries)
	press(a, key("esc"))
	press(a, key("j"))
	press(a, key("d"))
	if got := len(a.hist.entries); got != first+1 {
		t.Errorf("history has %d entries after the second inspect, %d after the first: the pages collapsed", got, first)
	}
}

// The first J on LAST puts the newest issue first, like a number's biggest,
// and orders by instant: Grail trims a fraction's zeros, and as text
// 11:59:58.5 sorts before 11:59:58.
func TestExceptionsSortByLastIsNewestFirst(t *testing.T) {
	a := testApp(t, "exceptions")
	stack := func(method string) string { return "com.example.shop.Api." + method + " (Api.java:1)" }
	tv := seedSample(t, a, []map[string]any{
		issueSpanRecord(1, "2026-07-15T11:59:58Z", "a", "T", "", stack("whole")),
		issueSpanRecord(2, "2026-07-15T11:59:58Z", "a", "T", "", stack("whole")),
		issueSpanRecord(3, "2026-07-15T11:59:58Z", "a", "T", "", stack("whole")),
		issueSpanRecord(4, "2026-07-15T11:59:57Z", "a", "T", "", stack("older")),
		issueSpanRecord(5, "2026-07-15T11:59:57Z", "a", "T", "", stack("older")),
		issueSpanRecord(6, "2026-07-15T11:59:58.5Z", "a", "T", "", stack("half")),
	}, nil)
	order := func() []string {
		var out []string
		for _, row := range tv.rows {
			out = append(out, catalog.Str(row, "start_time"))
		}
		return out
	}
	press(a, key("J")) // COUNT
	press(a, key("J")) // LAST
	if col := tv.columns()[tv.sortCol].Title; col != "LAST" {
		t.Fatalf("sorting by %s, want LAST", col)
	}
	want := []string{"2026-07-15T11:59:58.5Z", "2026-07-15T11:59:58Z", "2026-07-15T11:59:57Z"}
	if got := order(); !reflect.DeepEqual(got, want) {
		t.Errorf("first sort by LAST = %q\nwant newest first  %q", got, want)
	}
	press(a, key("K")) // the other direction
	slices.Reverse(want)
	if got := order(); !reflect.DeepEqual(got, want) {
		t.Errorf("reversed sort by LAST = %q, want %q", got, want)
	}
}

// The filter finds an issue by a service its cell does not show.
func TestExceptionsFilterFindsAHiddenService(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	press(a, key("/"))
	typeText(a, "checkout")
	if len(tv.rows) != 1 {
		t.Fatalf("/checkout left %d rows, want the issue that also ran in checkout", len(tv.rows))
	}
	if issue, _ := catalog.IssueOf(tv.rows[0]); !slices.Contains(issue.Services, "checkout") {
		t.Errorf("/checkout kept %+v", issue)
	}
}

// J cycles through the columns and back to the fetch order, which on this
// view is a ranking: loudest issue first. A column sort must not leave its
// order behind in the fetched rows.
func TestSortCycleEndsInTheFetchedOrder(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSample(), nil)
	keys := func() []string {
		var out []string
		for _, row := range tv.rows {
			issue, _ := catalog.IssueOf(row)
			out = append(out, issue.Key)
		}
		return out
	}
	fetched := keys()
	changed := false
	for range tv.columns() {
		press(a, key("J"))
		changed = changed || !reflect.DeepEqual(keys(), fetched)
		press(a, key("K"))
		changed = changed || !reflect.DeepEqual(keys(), fetched)
	}
	if !changed {
		t.Fatal("no column sort changed the order: the case proves nothing")
	}
	press(a, key("J"))
	if tv.sortCol != -1 || a.status != "sort: fetch order" {
		t.Fatalf("after the cycle: sortCol=%d status=%q", tv.sortCol, a.status)
	}
	if got := keys(); !reflect.DeepEqual(got, fetched) {
		t.Errorf("fetch order after a sort cycle = %q\nwant %q", got, fetched)
	}
	// The same under a filter that keeps every row.
	press(a, key("J"))
	tv.setFilter("e")
	tv.sortCol = -1
	tv.setFilter("")
	if got := keys(); !reflect.DeepEqual(got, fetched) {
		t.Errorf("fetch order after a filtered sort = %q\nwant %q", got, fetched)
	}
}

// A filter that matches nothing counts what it was tried on, and those are
// rows: the line above the table counts spans.
func TestExceptionsFilterMissCountsRows(t *testing.T) {
	a := testApp(t, "exceptions")
	seedSample(t, a, issueSample(), nil)
	press(a, key("/"))
	typeText(a, "zzzz")
	out := screen(a)
	if !strings.Contains(out, `no rows match "zzzz" (of 6 rows — esc clears the filter)`) || strings.Contains(out, "6 fetched") {
		t.Errorf("filter miss:\n%s", out)
	}
}

// Workspace primary tags are columns of projected entity rows. The rows of
// a sampled view are computed and carry none.
func TestExceptionsViewTakesNoPrimaryTagColumns(t *testing.T) {
	a := testApp(t, "exceptions")
	a.ds.primaryTags = []string{"team", "env"}
	tv := seedSample(t, a, issueSpans(), nil)
	var titles []string
	for _, c := range tv.columns() {
		titles = append(titles, c.Title)
	}
	if want := []string{"COUNT", "LAST", "EXCEPTION", "MESSAGE", "SERVICE", "BY"}; !reflect.DeepEqual(titles, want) {
		t.Errorf("columns = %q, want %q", titles, want)
	}
}

// A facet sent from a span inspector lands on any field the sampled spans
// carry, an array like span.events included; the picker still offers the
// scalar ones only.
func TestExceptionsAcceptsAFacetOnAnArrayField(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	if !tv.hasField("span.events") || !tv.hasField("dt.service.name") {
		t.Errorf("hasField: span.events=%v dt.service.name=%v, want both", tv.hasField("span.events"), tv.hasField("dt.service.name"))
	}
	if tv.hasField("k8s.namespace.name") || tv.hasField("__issue") {
		t.Error("hasField accepts a field no sampled span carries")
	}
	if slices.Contains(tv.facetCandidates(), "span.events") {
		t.Errorf("the picker offers an array field: %q", tv.facetCandidates())
	}
}

// The line above the table names the window the fetch ran over, also for a
// timeframe other than the default.
func TestExceptionsSampleLineNamesTheFetchedWindow(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	a.ds.runFn = func(string) ([]map[string]any, error) { return issueSpans(), nil }
	tf, ok := catalog.ParseTimeframe("7d")
	if !ok {
		t.Fatal("no 7d timeframe")
	}
	a.setTimeframe(tf)
	a.Update(tv.Refresh()())
	if out := screen(a); !strings.Contains(out, "all 3 exception spans · last 7d") {
		t.Errorf("the sample line does not name the window:\n%s", out)
	}
	// And an empty sample says the same window, not "last" before a range.
	from := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	abs := catalog.AbsoluteTimeframe(from, from.Add(2*time.Hour), snapshotNow)
	a.setTimeframe(abs)
	a.ds.runFn = func(string) ([]map[string]any, error) { return nil, nil }
	a.Update(tv.Refresh()())
	out := screen(a)
	if !strings.Contains(out, "no data in timeframe ("+abs.Label+")") || strings.Contains(out, "(last "+abs.Label) {
		t.Errorf("the empty table words the window wrong:\n%s", out)
	}
}

// The open picker shows the whole of each entry: the note on the Notebook
// entry ends in the word that matters.
func TestOpenPickerShowsTheWholeExitNote(t *testing.T) {
	p := &openPicker{list: []linkOption{
		{Label: "Distributed Tracing", URL: "https://tenant.example.invalid/a"},
		{Label: "query in a Notebook (the sampled exception spans, ungrouped)", URL: "https://tenant.example.invalid/b"},
	}}
	if out := p.View(120, 30); !strings.Contains(out, "ungrouped)") {
		t.Errorf("the picker cut the note:\n%s", out)
	}
	// On a screen too narrow for it the entry is cut, not the box broken.
	for _, line := range strings.Split(p.View(50, 30), "\n") {
		if lipgloss.Width(line) > 50 {
			t.Errorf("picker line wider than the screen: %q", line)
		}
	}
}

// Where enter leaves for another view, the folded preview does not promise
// that it opens the record.
func TestExceptionsFoldedPreviewNamesTheKeyThatOpensTheRecord(t *testing.T) {
	a := testApp(t, "exceptions")
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	seedSample(t, a, issueSample(), nil)
	out := screen(a)
	if strings.Contains(out, "enter opens the full record") || !strings.Contains(out, "… (d opens the full record)") {
		t.Errorf("the fold of the preview:\n%s", out)
	}
}

// The echo is shown as text: a control sequence in it (from a query the
// user typed, or a segment id) is drawn, not sent to the terminal.
func TestFooterShowsAnEchoAsText(t *testing.T) {
	const colour, paste = "\x1b[38;2;1;2;3m", "\x1b[201~"
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	tv.dql = "fetch spans " + colour + paste + " | limit 1"
	if !strings.Contains(tv.Echo(), colour) {
		t.Fatalf("the case lost its control bytes: %q", tv.Echo())
	}
	if out := a.View(); strings.Contains(out, colour) || strings.Contains(out, paste) || !strings.Contains(out, "␛[201~") {
		t.Errorf("the footer passes the echo's control bytes through:\n%q", out[max(len(out)-400, 0):])
	}
	press(a, key("c"))
	if strings.ContainsRune(a.status, 0x1b) || !strings.Contains(a.status, "␛[201~") {
		t.Errorf("status after c = %q", a.status)
	}
	logs := testApp(t, "logs")
	lv := logs.top().(*tableView)
	lv.searches, lv.dql = []string{"x"}, "fetch logs "+paste
	press(logs, key("c"))
	if strings.ContainsRune(logs.status, 0x1b) || !strings.Contains(logs.status, "␛[201~") {
		t.Errorf("logs status after c = %q", logs.status)
	}
}

// The facts of a sample outlive its records, like the rows: none of their
// strings may be cut out of what was fetched, or one field name would keep
// a whole response alive.
func TestSampleFactsShareNoStringWithTheRecords(t *testing.T) {
	buffer := strings.Repeat("x", 64<<10) + "start_time|2026-07-15T11:00:00Z|span.events|dt.service.name"
	cut := func(part string) string {
		at := strings.Index(buffer, part)
		return buffer[at : at+len(part)]
	}
	_, facts := takeSample(catalog.Lookup("exceptions").Sample, "last 2h", []map[string]any{{
		cut("start_time"): cut("2026-07-15T11:00:00Z"), cut("span.events"): []any{}, cut("dt.service.name"): "cart",
	}}, nil, false)
	kept := []string{facts.oldest}
	for k := range facts.keys {
		kept = append(kept, k)
	}
	kept = append(kept, facts.fields...)
	if facts.oldest == "" || len(facts.keys) != 3 || len(facts.fields) == 0 {
		t.Fatalf("the case lost its facts: %+v", facts)
	}
	lo := uintptr(unsafe.Pointer(unsafe.StringData(buffer)))
	for _, text := range kept {
		if at := uintptr(unsafe.Pointer(unsafe.StringData(text))); at >= lo && at < lo+uintptr(len(buffer)) {
			t.Errorf("%q points into the fetched buffer: the facts keep it alive", text)
		}
	}
}
