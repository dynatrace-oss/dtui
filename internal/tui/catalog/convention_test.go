package catalog

import (
	"fmt"
	"strings"
	"testing"
)

// The facet/search injectors (facets.go) rewrite generated DQL by line
// convention instead of a structured pipeline type: the source command owns
// the first line (InjectSearches inserts after it — DQL rejects `search`
// after transforming commands), and the trailing `| sort`/`| limit` block is
// where InjectStages splices narrowing in front of. These tests enforce the
// conventions on every spec so a drifting Query closure fails here instead
// of silently mis-splicing at runtime.

// conventionScopes are the fixture scopes every query renders against.
func conventionScopes() []Scope {
	tf := Timeframe{Label: "2h"}
	return []Scope{
		{Timeframe: tf},
		{Timeframe: tf, Entity: &Entity{ID: "HOST-0000000000000001", Name: "web-1", Type: "HOST"}},
		{Timeframe: tf, Entity: &Entity{ID: "K8S_POD-0000000000000002", Name: "checkout-1", Type: "K8S_POD"}},
		{Timeframe: tf, Arg: "argvalue"},
	}
}

func conventionSpecs() []*Spec {
	return append(All(),
		DavisEventsSpec, VulnEntitiesSpec, VulnAttacksSpec, VulnEntryPointsSpec, VulnTimelineSpec)
}

// sourceCommands are the pipeline heads catalog queries start with.
var sourceCommands = []string{"fetch ", "smartscapeNodes", "smartscapeEdges", "timeseries ", "metrics "}

func TestQueryConventionSourceOwnsFirstLine(t *testing.T) {
	for _, spec := range conventionSpecs() {
		if spec.Query == nil {
			continue
		}
		for _, scope := range conventionScopes() {
			maxLens := len(spec.Lenses)
			if maxLens == 0 {
				maxLens = 1
			}
			for lens := 0; lens < maxLens; lens++ {
				scope.Lens = lens
				dql := spec.Query(scope)
				if dql == "" {
					continue
				}
				first := strings.SplitN(dql, "\n", 2)[0]
				ok := false
				for _, src := range sourceCommands {
					if strings.HasPrefix(first, src) {
						ok = true
						break
					}
				}
				if !ok {
					t.Errorf("%s (lens %d): first line is not a source command — search injection would land mid-pipeline:\n%s",
						spec.Name, lens, first)
				}
				if strings.Contains(first, " | ") {
					t.Errorf("%s (lens %d): first line chains stages inline — searches must inject after the bare source:\n%s",
						spec.Name, lens, first)
				}
			}
		}
	}
}

// Every query must end in the sort/limit tail the stage injector splices in
// front of — a tail-less query would take facet filters after its last stage
// (still correct), but a MID-pipeline trailing block (e.g. ending on a bare
// summarize after a sort) would put filters before the aggregation that
// renames the very fields they reference.
func TestQueryConventionSortLimitTail(t *testing.T) {
	for _, spec := range conventionSpecs() {
		if spec.Query == nil {
			continue
		}
		for _, scope := range conventionScopes() {
			maxLens := len(spec.Lenses)
			if maxLens == 0 {
				maxLens = 1
			}
			for lens := 0; lens < maxLens; lens++ {
				scope.Lens = lens
				dql := spec.Query(scope)
				if dql == "" {
					continue
				}
				lines := strings.Split(dql, "\n")
				if cut := tailStart(lines); cut == len(lines) {
					t.Errorf("%s (lens %d): query has no trailing | sort / | limit — facet stages would append at the very end; add the conventional tail:\n%s",
						spec.Name, lens, dql)
				}
			}
		}
	}
}

// A sampled view (Spec.Sample) restates by hand what its query and the
// table rely on. Each omission fails silently or late: Rows 0 reports every
// sample as cut, Bytes 0 leaves the response unbounded, a nil Group panics
// in the fetch command, and a Rows that is not the query's own limit tells
// a full sample from a cut one at the wrong count.
func TestSampledSpecsAreComplete(t *testing.T) {
	sampled := 0
	for _, spec := range All() {
		s := spec.Sample
		if s == nil {
			continue
		}
		sampled++
		if s.Noun == "" || s.Time == "" || s.Group == nil {
			t.Errorf("%s: a sample needs a Noun, a Time field and a Group: %+v", spec.Name, s)
		}
		if s.Rows <= 0 || s.Bytes <= 0 {
			t.Errorf("%s: a sample needs both limits, got %d rows and %d bytes", spec.Name, s.Rows, s.Bytes)
		}
		if spec.API != "" || spec.Decode != nil || spec.Query == nil {
			t.Errorf("%s: a sampled view runs one DQL query and groups in its fetch command: no API source, no Decode", spec.Name)
			continue
		}
		for lens := range max(1, len(spec.Lenses)) {
			lines := strings.Split(StageLines(spec.Query(Scope{Timeframe: DefaultTimeframe, Lens: lens})), "\n")
			if want := fmt.Sprintf("| limit %d", s.Rows); strings.TrimSpace(lines[len(lines)-1]) != want {
				t.Errorf("%s (lens %d): the query ends on %q, want %q, the sample's Rows", spec.Name, lens, lines[len(lines)-1], want)
			}
		}
	}
	if sampled == 0 {
		t.Error("no spec has a Sample: the exceptions view lost its hook")
	}
}
