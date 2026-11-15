package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func chartFixture(t *testing.T) *metricsView {
	t.Helper()
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	mv := newMetricChartView(ds, "acme.queue.depth", catalog.Entity{}, catalog.DefaultTimeframe)
	mv.Init()()
	mv.Update(dataMsg{owner: mv, seq: mv.seq,
		records: []map[string]any{{"value": []any{-2.0, 4.0, nil, 10.0, 7.0, 3.0}}},
		metrics: []exec.MetricInfo{{MetricKey: "acme.queue.depth", FieldName: "value", Aggregation: "avg",
			DisplayName: "Queue depth", Description: "Messages waiting."}}})
	return mv
}

// The chart layout clamps its width and height at both ends: each size
// below crosses one of those edges.
func TestMetricsChartLayout(t *testing.T) {
	mv := chartFixture(t)
	for _, s := range []struct{ w, h int }{{14, 6}, {20, 10}, {60, 14}, {60, 30}} {
		assertSnapshot(t, fmt.Sprintf("metrics-chart-%dx%d", s.w, s.h), ansi.Strip(mv.View(s.w, s.h)))
	}
}

// Split charts share the body height; the picker scrolls with its cursor
// and counts what is below the fold.
func TestMetricsSplitLayout(t *testing.T) {
	mv := chartFixture(t)
	mv.splitDim = "queue"
	mv.unavailable = []string{"lag"}
	mv.seq++
	mv.Update(dataMsg{owner: mv, seq: mv.seq, records: []map[string]any{
		{"queue": "a", "value": []any{50.0, 80.0}},
		{"queue": "b", "value": []any{1.0, 2.0}},
		{"queue": "c", "value": []any{}},
	}})
	mv.entity = catalog.Entity{ID: "HOST-1", Name: "node-1"}
	assertSnapshot(t, "metrics-split-70x22", ansi.Strip(mv.View(70, 22)))

	mv.dimPick = true
	mv.dims = []dimInfo{{name: "queue", values: 1}, {name: "region", values: 4}, {name: "pod", values: 9}, {name: "zone", values: 2}}
	mv.dimSel = 3
	assertSnapshot(t, "metrics-picker-40x9", ansi.Strip(mv.View(40, 9)))
}

func TestSeriesStats(t *testing.T) {
	minV, maxV, avg, last := seriesStats([]float64{3, -1, 8, 2})
	if minV != -1 || maxV != 8 || avg != 3 || last != 2 {
		t.Errorf("seriesStats = %v %v %v %v", minV, maxV, avg, last)
	}
	if got := seriesAvg([]float64{1, 2, 6}); got != 3 {
		t.Errorf("seriesAvg = %v", got)
	}
}
