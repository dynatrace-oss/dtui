package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// richRecord exercises record mode: more link targets than the links block
// holds, an era-twin entity pair, every namespace rank, a value too long for
// its line and a JSON object.
func richRecord() map[string]any {
	rec := map[string]any{
		"trace.id":                "4bf92f3577b34da6a3ce929d0e0e4736",
		"dt.smartscape.host":      "HOST-00000000000000A1",
		"dt.entity.host":          "HOST-00000000000000A1",
		"dt.entity.process_group": []any{"PROCESS_GROUP-00000000000000B1", "PROCESS_GROUP-00000000000000B2"},
		"url.full":                "https://shop.example.invalid/cart",
		"status":                  "ok",
		"note":                    strings.Repeat("a long free-text value ", 8),
		"payload":                 map[string]any{"order": map[string]any{"id": 7.0, "items": []any{"x", "y"}}},
		"service.name":            "checkout",
		"host.name":               "node-1",
		"__enrich.cpu":            "hidden",
		"empty":                   "",
	}
	for i := range 5 {
		rec[fmt.Sprintf("link%d.url", i)] = fmt.Sprintf("https://example.invalid/%d", i)
	}
	return rec
}

func TestSnapshotInspectorRecordMode(t *testing.T) {
	v := newInspectorView(&dataSource{}, "rec", richRecord())
	assertSnapshot(t, "inspector-record-80x60", ansi.Strip(v.View(80, 60)))
	assertSnapshot(t, "inspector-record-50x60", ansi.Strip(v.View(50, 60)))
}

func TestInspectorSearchNarrowsAndClampsCursor(t *testing.T) {
	v := newInspectorView(&dataSource{}, "rec", richRecord())
	v.View(80, 40)
	v.Update(key("G"))
	if v.cursor != len(v.rows)-1 {
		t.Fatalf("G should select the last row, cursor = %d of %d", v.cursor, len(v.rows))
	}
	v.search = "checkout"
	v.rebuild()
	if len(v.rows) != 1 || v.rows[0].key != "service.name" || v.cursor != 0 {
		t.Fatalf("search should keep only service.name, rows = %d cursor = %d", len(v.rows), v.cursor)
	}
	v.search = "no-such-thing"
	v.rebuild()
	if body := ansi.Strip(v.View(80, 40)); len(v.rows) != 0 || !strings.Contains(body, "no matching properties") {
		t.Errorf("an unmatched search should say so:\n%s", body)
	}
	v.search = ""
	v.rebuild()
	if strings.Contains(ansi.Strip(v.View(80, 40)), "no matching properties") {
		t.Error("no search must not report a miss")
	}
}

func TestInspectorFieldDocFooter(t *testing.T) {
	ds := &dataSource{dict: map[string]catalog.FieldDoc{
		"service.name": {Description: "Logical service name.", Unit: "none", Stability: "experimental"},
		"status":       {Description: "Outcome.", Stability: "stable"},
		"host.name":    {Unit: "none"},
	}}
	v := newInspectorView(ds, "rec", map[string]any{"service.name": "checkout", "status": "ok", "host.name": "node-1", "zone": "a"})
	v.View(80, 20)
	footer := func(k string) string {
		for i, r := range v.rows {
			if r.key == k {
				v.cursor = i
				return ansi.Strip(v.docFooter())
			}
		}
		t.Fatalf("no row %s", k)
		return ""
	}
	cases := map[string]string{
		"service.name": " ⓘ Logical service name. · unit: none · experimental",
		"status":       " ⓘ Outcome.",
		"host.name":    "",
		"zone":         "",
	}
	for k, want := range cases {
		if got := footer(k); got != want {
			t.Errorf("footer(%s) = %q, want %q", k, got, want)
		}
	}
	v.cursor = len(v.rows)
	if v.docFooter() != "" {
		t.Error("no selection, no footer")
	}
}

func TestInspectorVitalRows(t *testing.T) {
	v := newInspectorView(&dataSource{}, "e", map[string]any{"a": "b"})
	v.View(120, 20)
	v.lines = nil
	v.vitalsWindow = "2h"
	v.vitals = []vitalStat{
		{title: "CPU usage", unit: "%", key: "cpu", series: []float64{10, 80, 95}},
		{title: "Network received bytes", unit: "B/s", key: "rx", series: []float64{1024, 2048}},
	}
	v.addVitalRows()
	got := make([]string, len(v.lines))
	for i, l := range v.lines {
		got[i] = ansi.Strip(l)
	}
	// The label column widens to the longest title; the absolute row keeps a
	// blank gauge slot so values align.
	cpu, rx := got[2], got[3]
	if got[1] != "▍ vitals (last 2h)" && !strings.Contains(got[1], "vitals (last 2h)") {
		t.Errorf("header = %q", got[1])
	}
	if !strings.HasPrefix(cpu, " cpu usage               ") || !strings.HasPrefix(rx, " network received bytes  ") {
		t.Errorf("label column:\n%q\n%q", cpu, rx)
	}
	if !strings.Contains(cpu, "███████████░          95%   avg 61.67% · max 95%") {
		t.Errorf("cpu row = %q", cpu)
	}
	if !strings.Contains(rx, strings.Repeat(" ", 12)+"   2.0 KiB/s   avg 1.5 KiB/s · max 2.0 KiB/s") {
		t.Errorf("rx row = %q", rx)
	}
	if n := len(v.rows); n < 2 || v.rows[n-2].key != "__vital.cpu" || v.rows[n-1].key != "__vital.rx" {
		t.Errorf("vital rows should be selectable actions, got %d", len(v.rows))
	}

	v.lines, v.vitals = nil, nil
	v.addVitalRows()
	if len(v.lines) != 0 {
		t.Error("no vitals, no block")
	}
}

func TestMeterAndLoadClass(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{-5, "░░░░"}, {0, "░░░░"}, {12.4, "░░░░"}, {12.5, "█░░░"}, {50, "██░░"}, {100, "████"}, {150, "████"},
	}
	for _, c := range cases {
		if got := ansi.Strip(meter(c.pct, 4)); got != c.want {
			t.Errorf("meter(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
	for p, want := range map[float64]string{74.9: "ok", 75: "warn", 89.9: "warn", 90: "error"} {
		if got := pctClass(p); got != want {
			t.Errorf("pctClass(%v) = %q, want %q", p, got, want)
		}
	}
}
