package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dynatrace-oss/dtui/internal/exckey"
)

// The exceptions view: the newest exception-bearing spans in scope, grouped
// into issues by the key exckey derives from each span's exception events.
// Span exceptions carry no grouping key in Grail, and no DQL computes this
// one, so the view fetches a bounded sample and groups it in Go. Every count
// is therefore a share of the sample, which the table's header spells out.

// The sample's two caps. Both are provisional: they were chosen from the
// size of single spans on a small OneAgent-fed tenant (about 5 KB per
// projected span, most of it stack text) and have not been measured on an
// environment that fills them. Revisit them there: a cap is right when a
// full sample still arrives within the time a view may take to open.
const (
	// ExceptionSampleRows is how many spans the view fetches at most; the
	// query's limit.
	ExceptionSampleRows = 1000
	// ExceptionSampleBytes caps the result. A sample of spans with long
	// stack traces is cut at this size before it reaches ExceptionSampleRows;
	// the header then says so.
	ExceptionSampleBytes = 8 << 20
)

// exceptionSampleFields are the span fields the sample keeps
// (SampleSpec.Fields): what the key is computed from (span.events), what a
// row shows (the start time and the service), what enter needs to open the
// newest span's trace, and three fields no row reads (span.name,
// endpoint.name, dt.smartscape.service), kept so the facet picker has them
// to offer. Everything else on a span is left on the server, which halves
// the result (measured on single spans).
const exceptionSampleFields = "start_time, span.id, trace.id, span.name, endpoint.name, " +
	"dt.service.name, service.name, dt.smartscape.service, span.events"

var exceptionsSpec = &Spec{
	Name:    "exceptions",
	Aliases: []string{"exc", "exception"},
	Kind:    KindSignal,
	Desc:    "Exception issues: the newest exception spans, grouped by what was thrown where",
	// The query ends on its sort and limit like every catalog query, so a
	// search or a facet lands before them (tailStart): it changes what is
	// sampled and can use any span field. Sample.Fields projects afterwards.
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch spans, from:%s", s.Timeframe.DQL())
		if f := ScopeSpanFilter(s); f != "" {
			fmt.Fprintf(&b, "\n| filter %s", f)
		}
		fmt.Fprintf(&b, "\n| filter %s", exceptionSpanFilter)
		fmt.Fprintf(&b, "\n| sort start_time desc\n| limit %d", ExceptionSampleRows)
		return b.String()
	},
	Sample: &SampleSpec{
		Noun:   "exception span",
		Time:   "start_time",
		Rows:   ExceptionSampleRows,
		Bytes:  ExceptionSampleBytes,
		Fields: exceptionSampleFields,
		Group:  GroupExceptionIssues,
		// What the preview pane shows of a row and its cells do not: the
		// key, the whole type, the type the event recorded, and every
		// service (the cell names one).
		Match: func(row map[string]any) string {
			issue := issueOf(row)
			return strings.Join(append([]string{issue.Key, issue.Type, issue.EventType}, issue.Services...), " ")
		},
	},
	Columns: []Column{
		{Title: "COUNT", Width: 5, Right: true,
			Value: func(rec map[string]any) string { return strconv.Itoa(issueOf(rec).Count) },
			Sort:  func(rec map[string]any) any { return issueOf(rec).Count }},
		// Min keeps "Jan 02 15:04" whole when the fixed columns are squeezed.
		{Title: "LAST", Width: 12, Min: 12,
			Value: func(rec map[string]any) string { return FormatTime(issueOf(rec).Newest) },
			Sort: func(rec map[string]any) any {
				// The instant, not its text: Grail trims a fraction's
				// trailing zeros, and "…58.5Z" sorts before "…58Z".
				if at, err := time.Parse(time.RFC3339Nano, issueOf(rec).Newest); err == nil {
					return at
				}
				return nil
			}},
		{Title: "EXCEPTION", Width: 22, Min: 12, Class: func(string) string { return "error" },
			Value: func(rec map[string]any) string { return ExceptionTypeShort(issueOf(rec).name()) }},
		{Title: "MESSAGE", Value: func(rec map[string]any) string {
			issue := issueOf(rec)
			if issue.Message == "" && len(issue.Frames) > 0 {
				return "at " + issue.Frames[0]
			}
			return issue.Message
		}},
		// The count of further services leads: behind the name, a long
		// name would push it out of the cell.
		{Title: "SERVICE", Width: 12, Min: 8,
			Value: func(rec map[string]any) string {
				services := issueOf(rec).Services
				switch len(services) {
				case 0:
					return ""
				case 1:
					return services[0]
				}
				return fmt.Sprintf("+%d %s", len(services)-1, services[0])
			},
			Sort: func(rec map[string]any) any {
				if services := issueOf(rec).Services; len(services) > 0 {
					return services[0]
				}
				return nil
			}},
		// What the row is grouped by says how specific its key is: a row
		// keyed on the type alone is visibly coarser than one keyed on the
		// application's frames ("app"; the preview pane has the full label).
		{Title: "BY", Width: 7, Min: 7, Class: func(string) string { return "dim" },
			Value: func(rec map[string]any) string {
				return strings.TrimSuffix(issueOf(rec).Rung, " frames")
			}},
	},
	// A row stands for its newest span, whose trace enter opens.
	EnterTarget: "waterfall",
	Trace:       func(rec map[string]any) string { return Str(rec, "trace.id") },
	Drills:      map[string]string{"l": "trace-logs"},
	Scopable:    func(e Entity) bool { return SpanScopable(e.Type) },
	Yank: func(rec map[string]any) (string, string) {
		key := issueOf(rec).Key
		if key == "" {
			return "", ""
		}
		return key, "issue key " + key
	},
}

// ExceptionIssue is one row of the exceptions view: the spans of the sample
// that share an issue key.
type ExceptionIssue struct {
	// Key is the issue key ("e1:" and 12 hex characters). "" on the one row
	// that collects the spans whose exception events say nothing.
	Key string
	// Rung labels the step of the fallback ladder the key was computed on.
	Rung string
	// Type is the exception type in the key; EventType the type the newest
	// span's event recorded, where that is an outer exception of Type.
	Type, EventType string
	// Message is the newest span's message, parameterised.
	Message string
	// Frames are the frames in the key, the throw site first.
	Frames []string
	// Count is how many spans of the sample share the key.
	Count int
	// Services are the names of the services those spans ran in: the one
	// most of them ran in first, then by name.
	Services []string
	// Newest and Oldest are the start times of the group's newest and
	// oldest span in the sample, as the records spelled them. Only a time
	// that can be read counts: both are "" when no span of the group has
	// one.
	Newest, Oldest string
}

// name is what a row is called where its type would stand: the type, a
// stand-in when the keyed event recorded none, or what the keyless row is:
// its spans may have recorded something, but nothing a key can be built
// from.
func (issue ExceptionIssue) name() string {
	if issue.Key == "" {
		return "(no key)"
	}
	return firstNonEmpty(issue.Type, "exception")
}

// exceptionIssueKey holds a row's ExceptionIssue. The "__" prefix keeps it
// out of the record inspector and the facet picker: it is no Grail field.
const exceptionIssueKey = "__issue"

// IssueOf returns the issue an exceptions-view row stands for.
func IssueOf(rec map[string]any) (ExceptionIssue, bool) {
	issue, ok := rec[exceptionIssueKey].(ExceptionIssue)
	return issue, ok
}

func issueOf(rec map[string]any) ExceptionIssue {
	issue, _ := IssueOf(rec)
	return issue
}

// GroupExceptionIssues turns a sample of exception-bearing spans into one
// row per issue key: the loudest issue first, then the one seen last, then
// by key. Every span is counted in exactly one row; a span none of whose
// exception events gives a type, a message that is non-empty once
// parameterised, or a usable frame has no key and is counted in a keyless
// last row, so the counts add up to the size of the sample.
//
// A row holds the issue under exceptionIssueKey and, as real span fields,
// the start time and the ids of the span that describes the group: its
// newest one, and of several that started in the same instant the one with
// the lowest span id, so the same sample gives the same row in whatever
// order Grail returns it. A start time that cannot be read is no instant;
// such a span describes its group only while no other does.
//
// A row shares no memory with the spans: every string it keeps is a copy,
// and their stack traces are released with them.
func GroupExceptionIssues(records []map[string]any) []map[string]any {
	type group struct {
		issue    ExceptionIssue
		by       issueMember // the span that describes the group
		oldest   time.Time
		services map[string]int // spans per service name
	}
	groups := map[string]*group{}
	var order []*group
	for _, rec := range records {
		fp, _ := FingerprintExceptions(parseSpanExceptions(rec))
		span := issueMember{start: Str(rec, "start_time"), spanID: Str(rec, "span.id"), trace: Str(rec, "trace.id")}
		var err error
		span.at, err = time.Parse(time.RFC3339Nano, span.start)
		span.timed = err == nil
		g := groups[fp.Key]
		if g == nil {
			g = &group{services: map[string]int{}}
			groups[fp.Key] = g
			order = append(order, g)
		}
		g.issue.Count++
		if svc := SpanService(rec); svc != "" {
			g.services[svc]++
		}
		if span.timed && (g.issue.Oldest == "" || span.at.Before(g.oldest)) {
			g.oldest, g.issue.Oldest = span.at, span.start
		}
		if g.issue.Count == 1 || span.describesBefore(g.by) {
			g.by = span
			describeIssue(&g.issue, fp)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		switch {
		case (a.issue.Key == "") != (b.issue.Key == ""):
			return b.issue.Key == ""
		case a.issue.Count != b.issue.Count:
			return a.issue.Count > b.issue.Count
		case !a.by.at.Equal(b.by.at):
			return a.by.at.After(b.by.at)
		}
		return a.issue.Key < b.issue.Key
	})
	rows := make([]map[string]any, len(order))
	for i, g := range order {
		for svc := range g.services {
			g.issue.Services = append(g.issue.Services, strings.Clone(svc))
		}
		sort.Slice(g.issue.Services, func(a, b int) bool {
			x, y := g.issue.Services[a], g.issue.Services[b]
			if g.services[x] != g.services[y] {
				return g.services[x] > g.services[y]
			}
			return x < y
		})
		if g.by.timed {
			g.issue.Newest = strings.Clone(g.by.start)
		}
		g.issue.Oldest = strings.Clone(g.issue.Oldest)
		rows[i] = map[string]any{
			exceptionIssueKey: g.issue,
			"start_time":      strings.Clone(g.by.start),
			"span.id":         strings.Clone(g.by.spanID),
			"trace.id":        strings.Clone(g.by.trace),
		}
	}
	return rows
}

// issueMember is what grouping reads of one span to pick the span that
// describes its group.
type issueMember struct {
	start, spanID, trace string
	at                   time.Time // the zero instant unless timed
	timed                bool      // start was read as a time
}

// describesBefore reports whether m describes a group rather than other: a
// span with a readable time before one without, then the newer one, then
// the lower span id.
func (m issueMember) describesBefore(other issueMember) bool {
	switch {
	case m.timed != other.timed:
		return m.timed
	case !m.at.Equal(other.at):
		return m.at.After(other.at)
	}
	return m.spanID < other.spanID
}

// describeIssue copies what a fingerprint says about its exception into the
// issue. The fingerprint's strings may be slices of a span's stack trace, so
// each is cloned.
func describeIssue(issue *ExceptionIssue, fp exckey.ExceptionFingerprint) {
	issue.Key = fp.Key
	issue.Rung = fp.Rung.String()
	issue.Type = strings.Clone(fp.Type)
	issue.EventType = strings.Clone(fp.EventType)
	issue.Message = strings.Clone(fp.Message)
	issue.Frames = make([]string, len(fp.Frames))
	for i, f := range fp.Frames {
		issue.Frames[i] = strings.Clone(joinNonEmpty(".", f.Module, f.Function))
	}
}

// issuePreview is the preview pane of an exceptions-view row: what the key
// was computed from, so a group can be judged without opening a trace.
func issuePreview(issue ExceptionIssue) []PreviewFact {
	var b previewBuilder
	b.add("issue", issue.Key)
	spans := "spans"
	if issue.Count == 1 {
		spans = "span"
	}
	b.add("in sample", fmt.Sprintf("%d %s", issue.Count, spans))
	b.add("grouped by", issue.Rung)
	if issue.EventType != issue.Type {
		b.add("recorded as", issue.EventType)
	}
	b.wrapped("message", issue.Message)
	for i, frame := range issue.Frames {
		label := "then"
		if i == 0 {
			label = "thrown at"
		}
		// Wrapped like the message: cut at the pane's edge, a frame loses
		// its function, which is what tells two throw sites apart.
		b.facts = append(b.facts, PreviewFact{Label: label, Value: frame, Wrap: true})
	}
	b.add("services", strings.Join(issue.Services, ", "))
	last, first := FormatTime(issue.Newest), FormatTime(issue.Oldest)
	b.add("last seen", last)
	// Two sightings the clock prints alike are one line.
	if first != last {
		b.add("first seen", first)
	}
	return b.facts
}
