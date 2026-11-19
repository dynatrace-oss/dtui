package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindFrom(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("not found", func(t *testing.T) {
		if path, ok := findFrom(nested); ok {
			t.Fatalf("expected no workspace file, found %s", path)
		}
	})

	want := writeFile(t, root, "version: 1\n")

	t.Run("found in start dir", func(t *testing.T) {
		path, ok := findFrom(root)
		if !ok || path != want {
			t.Fatalf("findFrom(root) = %q, %v; want %q, true", path, ok, want)
		}
	})

	t.Run("found walking up", func(t *testing.T) {
		path, ok := findFrom(nested)
		if !ok || path != want {
			t.Fatalf("findFrom(nested) = %q, %v; want %q, true", path, ok, want)
		}
	})

	t.Run("nearest file wins", func(t *testing.T) {
		near := writeFile(t, filepath.Join(root, "a"), "version: 1\n")
		path, ok := findFrom(nested)
		if !ok || path != near {
			t.Fatalf("findFrom(nested) = %q, %v; want %q, true", path, ok, near)
		}
	})
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, `
version: 1
environment: https://abc12345.apps.dynatrace.com
view: pods
timeframe: 2h
segments:
  - payments-prod
  - segment: 4lpVjcpcsjd
    variables:
      environment: [production]
      region: [us-east-1, us-west-2]
`)
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}
	if w.Environment != "https://abc12345.apps.dynatrace.com" || w.View != "pods" || w.Timeframe != "2h" {
		t.Errorf("unexpected fields: %+v", w)
	}
	if len(w.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(w.Segments))
	}
	if w.Segments[0].Ref != "payments-prod" || w.Segments[0].Variables != nil {
		t.Errorf("scalar segment = %+v", w.Segments[0])
	}
	if w.Segments[1].Ref != "4lpVjcpcsjd" {
		t.Errorf("map segment ref = %q", w.Segments[1].Ref)
	}
	if got := w.Segments[1].Variables["region"]; len(got) != 2 || got[1] != "us-west-2" {
		t.Errorf("variables = %+v", w.Segments[1].Variables)
	}
}

func TestLoadUnknownKeysIgnored(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "version: 1\nfuture-key: whatever\nview: logs\n")
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.View != "logs" {
		t.Errorf("View = %q, want logs", w.View)
	}
}

func TestLoadErrors(t *testing.T) {
	segments := func(n int) string {
		var b strings.Builder
		b.WriteString("segments:\n")
		for i := 0; i < n; i++ {
			b.WriteString("  - seg-")
			b.WriteByte(byte('a' + i))
			b.WriteString("\n")
		}
		return b.String()
	}
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"invalid yaml", "segments: [unclosed", "parsing"},
		{"future version", "version: 2\n", "newer than this build"},
		{"too many segments", segments(MaxSegments + 1), "at most 10"},
		{"empty ref scalar", "segments:\n  - \"\"\n", "missing segment name"},
		{"empty ref map", "segments:\n  - variables:\n      env: [prod]\n", "missing segment name"},
		{"variable without values", "segments:\n  - segment: s1\n    variables:\n      env: []\n", "has no values"},
		{"bad timeframe", "timeframe: yesterday\n", "expected <n>m"},
		{"bad timeframe unit", "timeframe: 2w\n", "expected <n>m"},
		{"bad environment scheme", "environment: ftp://host\n", "unsupported scheme"},
		{"environment with credentials", "environment: https://user:pw@host\n", "credentials"},
		{"environment with path as bare host", "environment: host/path\n", "bare hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadValidBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"absent version", "view: pods\n"},
		{"max segments", func() string {
			var b strings.Builder
			b.WriteString("segments:\n")
			for i := 0; i < MaxSegments; i++ {
				b.WriteString("  - seg-")
				b.WriteByte(byte('a' + i))
				b.WriteString("\n")
			}
			return b.String()
		}()},
		{"bare hostname environment", "environment: abc12345.apps.dynatrace.com\n"},
		{"minutes timeframe", "timeframe: 30m\n"},
		{"days timeframe", "timeframe: 7d\n"},
		{"empty file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), tc.content)
			if _, err := Load(path); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestPrimaryTagsWorkspace(t *testing.T) {
	t.Run("global primary-tags parsed", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
primary-tags:
  - app
  - env
  - version
segments:
  - payments-prod
`)
		w, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(w.PrimaryTags) != 3 {
			t.Fatalf("PrimaryTags len = %d, want 3", len(w.PrimaryTags))
		}
		if w.PrimaryTags[0] != "app" || w.PrimaryTags[1] != "env" || w.PrimaryTags[2] != "version" {
			t.Errorf("PrimaryTags = %v", w.PrimaryTags)
		}
	})

	t.Run("per-segment primary-tags scalar form", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
segments:
  - payments-prod
  - segment: seg-uid-123
    primary-tags:
      - app
      - tier
`)
		w, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if w.Segments[0].PrimaryTags != nil {
			t.Errorf("scalar segment should have no PrimaryTags, got %v", w.Segments[0].PrimaryTags)
		}
		if len(w.Segments[1].PrimaryTags) != 2 {
			t.Fatalf("map segment PrimaryTags len = %d, want 2", len(w.Segments[1].PrimaryTags))
		}
		if w.Segments[1].PrimaryTags[0] != "app" || w.Segments[1].PrimaryTags[1] != "tier" {
			t.Errorf("segment PrimaryTags = %v", w.Segments[1].PrimaryTags)
		}
	})

	t.Run("deduplication is silent", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
primary-tags:
  - app
  - env
  - app
`)
		w, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(w.PrimaryTags) != 2 {
			t.Fatalf("after dedup PrimaryTags len = %d, want 2 (got %v)", len(w.PrimaryTags), w.PrimaryTags)
		}
	})

	t.Run("empty key rejected", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
primary-tags:
  - app
  - ""
`)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error for empty key, got nil")
		}
		if !strings.Contains(err.Error(), "empty key") {
			t.Errorf("error %q does not contain 'empty key'", err)
		}
	})

	t.Run("cap exceeded", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
primary-tags:
  - a
  - b
  - c
  - d
  - e
  - f
`)
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error for too many primary tag keys, got nil")
		}
		if !strings.Contains(err.Error(), "max") {
			t.Errorf("error %q does not contain 'max'", err)
		}
	})

	t.Run("duplicates don't inflate cap", func(t *testing.T) {
		// MaxPrimaryTags=5; 6 entries with 5 unique must load without error.
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
primary-tags:
  - a
  - b
  - c
  - d
  - e
  - a
`)
		w, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v — duplicates should not inflate the cap count", err)
		}
		if len(w.PrimaryTags) != 5 {
			t.Fatalf("after dedup PrimaryTags len = %d, want 5 (got %v)", len(w.PrimaryTags), w.PrimaryTags)
		}
	})

	t.Run("whitespace trimmed", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, `
version: 1
primary-tags:
  - "  app  "
  - env
`)
		w, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if w.PrimaryTags[0] != "app" {
			t.Errorf("expected trimmed key 'app', got %q", w.PrimaryTags[0])
		}
	})
}

func TestDiscoverAbsent(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	w, err := Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if w != nil {
		t.Fatalf("Discover = %+v, want nil (no file anywhere above %s)", w, dir)
	}
}

func TestLoadPresets(t *testing.T) {
	path := writeFile(t, t.TempDir(), `
version: 1
presets:
  payments-errors:
    view: logs
    timeframe: 2h
    facets:
      - field: loglevel
        value: ERROR
      - field: k8s.namespace.name
        value: payments
    searches:
      - timeout
  pods-checkout:
    view: pods
    facets:
      - field: k8s.namespace.name
        value: checkout
`)
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := w.LookupPreset("payments-errors")
	if !ok {
		t.Fatal("payments-errors missing")
	}
	if p.View != "logs" || p.Timeframe != "2h" || len(p.Facets) != 2 || p.Facets[0].Field != "loglevel" {
		t.Errorf("payments-errors = %+v", p)
	}
	if len(p.Searches) != 1 || p.Searches[0] != "timeout" {
		t.Errorf("searches = %v", p.Searches)
	}
	names := w.PresetNames()
	if len(names) != 2 || names[0] != "payments-errors" || names[1] != "pods-checkout" {
		t.Errorf("PresetNames = %v", names)
	}
	if _, ok := w.LookupPreset("missing"); ok {
		t.Error("LookupPreset(missing) should fail")
	}
}

func TestLoadPresetErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"bad name", "presets:\n  LimaErrors:\n    view: logs\n    facets:\n      - field: a\n        value: b\n", "kebab-case"},
		{"missing view", "presets:\n  payments-errors:\n    facets:\n      - field: a\n        value: b\n", "view is required"},
		{"empty narrowing", "presets:\n  payments-errors:\n    view: logs\n", "at least one facet or search"},
		{"missing facet value", "presets:\n  payments-errors:\n    view: logs\n    facets:\n      - field: loglevel\n", "field and value"},
		{"unsafe field", "presets:\n  payments-errors:\n    view: logs\n    facets:\n      - field: \"a|b\"\n        value: x\n", "unsafe"},
		{"unsafe map-field", "presets:\n  payments-errors:\n    view: logs\n    facets:\n      - field: labels\n        value: x\n        map-field: \"tags:`k8s\"\n        map-key: app\n", "unsafe"},
		{"unsafe map-key", "presets:\n  payments-errors:\n    view: logs\n    facets:\n      - field: labels\n        value: x\n        map-field: tags:k8s.labels\n        map-key: \"a|b\"\n", "unsafe"},
		{"bad timeframe", "presets:\n  payments-errors:\n    view: logs\n    timeframe: 2w\n    facets:\n      - field: a\n        value: b\n", "expected <n>m"},
		{"map-field without key", "presets:\n  payments-errors:\n    view: logs\n    facets:\n      - field: labels\n        value: x\n        map-field: tags:k8s.labels\n", "map-key"},
		{"empty search", "presets:\n  payments-errors:\n    view: logs\n    searches:\n      - \"\"\n", "empty search"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeFile(t, t.TempDir(), tc.content))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadTooManyPresetFacets(t *testing.T) {
	var b strings.Builder
	b.WriteString("presets:\n  payments-errors:\n    view: logs\n    facets:\n")
	for i := 0; i < MaxFacetsPerPreset+1; i++ {
		fmt.Fprintf(&b, "      - field: f%d\n        value: v\n", i)
	}
	_, err := Load(writeFile(t, t.TempDir(), b.String()))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "max") {
		t.Errorf("error %q does not contain max", err)
	}
}

func TestSavePresetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	p := Preset{
		View:     "logs",
		Facets:   []PresetFacet{{Field: "loglevel", Value: "ERROR"}},
		Searches: []string{"timeout"},
	}
	if err := SavePreset(path, "payments-errors", p); err != nil {
		t.Fatal(err)
	}
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := w.LookupPreset("payments-errors")
	if !ok || got.View != "logs" || len(got.Facets) != 1 || got.Facets[0].Value != "ERROR" {
		t.Errorf("loaded %+v", got)
	}
	p.Facets = []PresetFacet{{Field: "loglevel", Value: "WARN"}}
	if err := SavePreset(path, "payments-errors", p); err != nil {
		t.Fatal(err)
	}
	w, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = w.LookupPreset("payments-errors")
	if got.Facets[0].Value != "WARN" {
		t.Errorf("replace failed: %+v", got)
	}
	if err := SavePreset(path, "Not-Kebab", p); err == nil {
		t.Fatal("expected kebab-case error")
	}
}

func TestDeletePresetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yaml")
	p := Preset{View: "logs", Facets: []PresetFacet{{Field: "loglevel", Value: "ERROR"}}}
	if err := SavePreset(path, "payments-errors", p); err != nil {
		t.Fatal(err)
	}
	if err := SavePreset(path, "checkout-pods", Preset{View: "pods", Facets: []PresetFacet{{Field: "k8s.namespace.name", Value: "checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if err := DeletePreset(path, "payments-errors"); err != nil {
		t.Fatal(err)
	}
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.LookupPreset("payments-errors"); ok {
		t.Fatal("payments-errors still present")
	}
	if _, ok := w.LookupPreset("checkout-pods"); !ok {
		t.Fatal("checkout-pods removed")
	}
	if err := DeletePreset(path, "missing"); err == nil {
		t.Fatal("expected missing-name error")
	}
}

func TestMergePresetCatalogPersonalWins(t *testing.T) {
	project := &Workspace{Presets: map[string]Preset{
		"shared":  {View: "logs", Facets: []PresetFacet{{Field: "a", Value: "project"}}},
		"only-ws": {View: "pods", Facets: []PresetFacet{{Field: "b", Value: "ws"}}},
	}}
	personal := &Workspace{Presets: map[string]Preset{
		"shared":  {View: "logs", Facets: []PresetFacet{{Field: "a", Value: "personal"}}},
		"only-me": {View: "logs", Facets: []PresetFacet{{Field: "c", Value: "me"}}},
	}}
	merged := MergePresetCatalog(project, personal)
	if v, _ := merged.LookupPreset("shared"); v.Facets[0].Value != "personal" {
		t.Errorf("shared = %+v, want personal overlay", v)
	}
	if _, ok := merged.LookupPreset("only-ws"); !ok {
		t.Error("missing only-ws")
	}
	if _, ok := merged.LookupPreset("only-me"); !ok {
		t.Error("missing only-me")
	}
	if MergePresetCatalog(nil, nil).Presets == nil {
		t.Error("merge of nils should still have a map")
	}
}
