// Sampled views (catalog.Spec.Sample): the fetch that groups a bounded
// sample into rows, and the header line that says what the sample covers.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// sampleCut is why a sample holds less than its timeframe does.
type sampleCut int

const (
	cutNone    sampleCut = iota
	cutRows              // the query's own limit was reached
	cutBytes             // the result reached its size limit
	cutPartial           // cut short where nothing says which records arrived
	cutScan              // Grail stopped scanning at its scan limit
	cutTimeout           // the fetch timed out
)

// sampleFacts describe the records a sampled view's rows were computed from.
// They travel with those rows in one dataMsg, so the header can never
// describe another fetch than the table below it: not a later timeframe,
// and not another response's notices.
type sampleFacts struct {
	noun    string // one fetched record ("exception span")
	window  string // the timeframe the query ran over, as the header words it
	fetched int
	oldest  string // the oldest record's time field, "" when none carries one
	cut     sampleCut
	// sampled is set when Grail read only a part of the data (a sampling
	// ratio, which dtui never asks for but an environment may default to).
	sampled bool
	// fields are the scalar fields of the fetched records, sorted: the facet
	// picker's candidates, which the computed rows no longer carry.
	fields []string
	// keys are all fields of the fetched records, arrays and records
	// included: what a facet sent from another view may name (hasField).
	keys map[string]bool
}

// sampleWindow words a timeframe for the header.
func sampleWindow(tf catalog.Timeframe) string {
	if tf.Absolute() {
		return tf.Label
	}
	return "last " + tf.Label
}

// takeSample computes a sampled view's rows and the facts about the records
// they come from. It runs in the fetch command, off the update loop.
// sampled is the response's own word that it read a part of the data.
func takeSample(s *catalog.SampleSpec, window string, records []map[string]any, notices []exec.QueryNotification, sampled bool) ([]map[string]any, *sampleFacts) {
	facts := &sampleFacts{
		noun:    s.Noun,
		window:  window,
		fetched: len(records),
		cut:     cutFor(notices),
		sampled: sampled || samplingNoticed(notices),
		fields:  scalarFacetFields(records),
		keys:    map[string]bool{},
	}
	// The facts outlive the records: no string of theirs may be a record's.
	for i, field := range facts.fields {
		facts.fields[i] = strings.Clone(field)
	}
	for _, rec := range records {
		for k := range rec {
			if !facts.keys[k] && !strings.HasPrefix(k, "__") {
				facts.keys[strings.Clone(k)] = true
			}
		}
	}
	if facts.cut == cutNone && int64(len(records)) >= s.Rows {
		facts.cut = cutRows
	}
	var oldest time.Time
	for _, rec := range records {
		raw := catalog.Str(rec, s.Time)
		if at, err := time.Parse(time.RFC3339Nano, raw); err == nil && (facts.oldest == "" || at.Before(oldest)) {
			oldest, facts.oldest = at, strings.Clone(raw)
		}
	}
	return s.Group(records), facts
}

// cutFor reads from a response's notices what cut its result short: of
// several the gravest, the one the footer names too (gravestNotice).
//
// A notice counts whatever its severity, as it does for dtctl
// (exec.PartialCause): a result that was cut is cut, also when Grail files
// the fact as information. The consumption limit has no wording of its own
// here and reads as cutPartial.
func cutFor(notices []exec.QueryNotification) sampleCut {
	switch gravestNotice(notices, nil) {
	case noticeTimeout:
		return cutTimeout
	case noticeScanLimit:
		return cutScan
	case noticeConsumption:
		return cutPartial
	case noticeSizeLimit:
		return cutBytes
	case noticeResultLimit:
		return cutRows
	}
	return cutNone
}

// samplingNoticed reports whether a notice says the read was sampled.
func samplingNoticed(notices []exec.QueryNotification) bool {
	for _, n := range notices {
		if classifyNotice(n.NotificationType, n.Message) == noticeSampling {
			return true
		}
	}
	return false
}

// line renders the header: what cut the sample, how many records arrived,
// how far back they reach, and last the window they were fetched over. A
// narrow table truncates the line at its end, so what is nowhere else comes
// first, as short as it can be said: the reason, the count and the reach
// fit beside the preview pane at its narrowest, also with a date in the
// reach (TestSampleLineKeepsItsReachAtEveryWidth). The caveat, the hint
// and the window, which the screen's own header names anyway, trail.
// warn is set when the records are not known to be all the timeframe holds:
// the sample was cut, or read with sampling. "" for an empty sample nothing
// cut: an empty view needs no caveat.
func (f *sampleFacts) line() (text string, warn bool) {
	if f == nil || (f.fetched == 0 && f.cut == cutNone && !f.sampled) {
		return "", false
	}
	count := fmt.Sprintf("%d %s", f.fetched, f.noun)
	if f.fetched != 1 {
		count += "s"
	}
	const narrow = "; narrow the timeframe (t)"
	var reason, what string
	switch f.cut {
	case cutNone:
		switch {
		case !f.sampled && f.fetched == 1:
			return count + " · " + f.window, false
		case !f.sampled:
			return "all " + count + " · " + f.window, false
		case f.fetched == 0:
			reason, what = "sampled read", "no "+f.noun+"s in the part that was read"
		default:
			reason, what = "sampled read", count+" in the part that was read"
		}
	case cutRows:
		// A full sample without a notice cannot be told from a cut one, so
		// the line claims nothing about older records.
		reason, what = "row limit", "newest "+count+f.reach()+"; any older ones are not counted"
		if f.fetched == 0 {
			what = "the result was cut and holds no " + f.noun + "s"
		}
	case cutBytes:
		reason, what = "size limit", "newest "+count+f.reach()+"; older ones are not counted"
		if f.fetched == 0 {
			what = "no " + f.noun + " fit"
		}
	case cutPartial:
		reason, what = "partial result", count+" of what was returned"+narrow
		if f.fetched == 0 {
			what = "no " + f.noun + "s in what was returned" + narrow
		}
	case cutScan, cutTimeout:
		reason = "scan limit"
		if f.cut == cutTimeout {
			reason = "fetch timed out"
		}
		// Grail scans in no promised order, so these are not known to be
		// the newest.
		what = count + " of what was scanned" + narrow
		if f.fetched == 0 {
			what = "the scan gave up before it found " + f.noun + "s" + narrow
		}
	}
	return reason + ": " + what + " · " + f.window, true
}

// reach words how far back a sample of the newest records goes ("" when no
// record carries a readable time).
func (f *sampleFacts) reach() string {
	if f.oldest == "" {
		return ""
	}
	return " back to " + catalog.FormatTime(f.oldest)
}

// sampleOpts are a sample query's execution options: the usual ones, with
// the spec's two caps.
func (d *dataSource) sampleOpts(s *catalog.SampleSpec) exec.DQLExecuteOptions {
	opts := d.execOpts(s.Rows, false)
	opts.MaxResultBytes = s.Bytes
	return opts
}

// sample runs a sampled view's list query and groups the result before it
// is delivered: the dataMsg carries the computed rows and the facts of the
// sample, and the fetched records are released here. Grouping a thousand
// stack traces is too slow for the update loop, which also draws. Like every
// query it is never cancelled; a superseded result is dropped by seq.
func (d *dataSource) sample(owner any, seq int, dql string, s *catalog.SampleSpec, window string) tea.Cmd {
	fetch, opts := d.fetcher(), d.sampleOpts(s)
	return func() tea.Msg {
		start := time.Now()
		msg := fetch(owner, seq, dql, opts)
		if msg.err == nil {
			msg.records, msg.sample = takeSample(s, window, msg.records, msg.notices, msg.sampled)
		}
		msg.elapsed = time.Since(start)
		return msg
	}
}
