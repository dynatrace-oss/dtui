package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dynatrace/dtctl/sdk/session"
	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/workspace"
)

func TestFindContextByEnvironment(t *testing.T) {
	cfg := &session.Config{
		Contexts: []session.NamedContext{
			{Name: "prod", Context: session.Context{Environment: "https://abc12345.apps.dynatrace.com"}},
			{Name: "dev", Context: session.Context{Environment: "https://dev67890.apps.dynatrace.com/"}},
		},
	}
	cases := []struct {
		env  string
		want string
		ok   bool
	}{
		{"https://abc12345.apps.dynatrace.com", "prod", true},
		{"https://ABC12345.apps.dynatrace.com/", "prod", true}, // case + trailing slash
		{"abc12345.apps.dynatrace.com", "prod", true},          // bare host
		{"http://dev67890.apps.dynatrace.com", "dev", true},    // scheme ignored
		{"https://other.apps.dynatrace.com", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		name, ok := findContextByEnvironment(cfg, tc.env)
		if name != tc.want || ok != tc.ok {
			t.Errorf("findContextByEnvironment(%q) = %q, %v; want %q, %v", tc.env, name, ok, tc.want, tc.ok)
		}
	}
}

func TestWorkspaceSegmentsDeterministicVariableOrder(t *testing.T) {
	ws := &workspace.Workspace{Segments: []workspace.Segment{
		{Ref: "seg-1", Variables: map[string][]string{
			"zone": {"z1"}, "env": {"prod"}, "region": {"eu"},
		}},
	}}
	refs := workspaceSegments(ws)
	if len(refs) != 1 || refs[0].Ref != "seg-1" {
		t.Fatalf("refs = %+v", refs)
	}
	var names []string
	for _, v := range refs[0].Variables {
		names = append(names, v.Name)
	}
	if got := strings.Join(names, ","); got != "env,region,zone" {
		t.Errorf("variable order = %s, want sorted env,region,zone", got)
	}
}

func TestWorkspaceNotice(t *testing.T) {
	ws, err := workspace.Load(writeWorkspaceFile(t, `
view: pods
timeframe: 24h
segments:
  - payments-prod
  - team-checkout
`))
	if err != nil {
		t.Fatal(err)
	}
	notice := workspaceNotice(ws)
	for _, want := range []string{"workspace ", "2 segments", "last 24h", "view pods"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q misses %q", notice, want)
		}
	}

	empty, err := workspace.Load(writeWorkspaceFile(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceNotice(empty); got != "" {
		t.Errorf("empty workspace notice = %q, want none", got)
	}
}

func TestWritePresetListSanitizesValues(t *testing.T) {
	raw := "ERR\x1b[31mOR"
	ws := &workspace.Workspace{Presets: map[string]workspace.Preset{
		"payments-errors": {
			View:     "logs",
			Facets:   []workspace.PresetFacet{{Field: "loglevel", Value: raw}},
			Searches: []string{"a\x07b"},
		},
	}}
	var buf strings.Builder
	writePresetList(&buf, ws, nil)
	out := buf.String()
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") {
		t.Fatalf("raw control characters leaked:\n%q", out)
	}
	if !strings.Contains(out, catalog.SafeText(raw)) {
		t.Errorf("sanitized facet value missing:\n%s", out)
	}
	if !strings.Contains(out, catalog.SafeText("a\x07b")) {
		t.Errorf("sanitized search missing:\n%s", out)
	}
}

func TestWritePresetListLabelsSource(t *testing.T) {
	ws := &workspace.Workspace{Presets: map[string]workspace.Preset{
		"payments-errors": {View: "logs", Searches: []string{"timeout"}},
	}}
	personal := &workspace.Workspace{Presets: map[string]workspace.Preset{
		"checkout-pods": {View: "pods", Searches: []string{"checkout"}},
	}}
	var buf strings.Builder
	writePresetList(&buf, workspace.MergePresetCatalog(ws, personal), personal)
	for _, line := range strings.Split(buf.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "checkout-pods") && !strings.Contains(line, "personal"):
			t.Errorf("personal preset not labeled personal: %q", line)
		case strings.HasPrefix(line, "payments-errors") && !strings.Contains(line, "workspace"):
			t.Errorf("workspace preset not labeled workspace: %q", line)
		}
	}
}

func TestCanonicalViewNameKeepsUnknownNames(t *testing.T) {
	if got := canonicalViewName("not-a-view"); got != "not-a-view" {
		t.Errorf("canonicalViewName(not-a-view) = %q", got)
	}
	if got := canonicalViewName("logs"); got != "logs" {
		t.Errorf("canonicalViewName(logs) = %q", got)
	}
}

func TestListPresetsIsACommand(t *testing.T) {
	need := map[string]bool{"list-presets": false, "delete-preset": false, "save-preset": false, "send": false}
	for _, c := range rootCmd.Commands() {
		if _, ok := need[c.Name()]; ok {
			need[c.Name()] = true
		}
	}
	for name, found := range need {
		if !found {
			t.Fatalf("%s must be a cobra subcommand so `dtui --context prod %s` is not parsed as a view", name, name)
		}
	}
}

func TestResolveLaunchPreset(t *testing.T) {
	ws, err := workspace.Load(writeWorkspaceFile(t, `
presets:
  payments-errors:
    view: logs
    facets:
      - field: loglevel
        value: ERROR
`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := resolveLaunch("", "", ws, launchFlags{preset: "payments-errors"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Target != "logs" || len(p.Facets) != 1 || p.Facets[0].Value != "ERROR" || !p.FromPreset {
		t.Errorf("preset launch = %+v", p)
	}
	p, err = resolveLaunch("logs", "", ws, launchFlags{preset: "payments-errors", facets: []string{"loglevel=WARN", "k8s.namespace.name=payments"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Facets) != 2 || p.Facets[0].Value != "WARN" || p.Facets[1].Field != "k8s.namespace.name" {
		t.Errorf("cli facet merge = %+v", p.Facets)
	}
	if _, err := resolveLaunch("pods", "", ws, launchFlags{preset: "payments-errors"}); err == nil {
		t.Fatal("view mismatch should error")
	}
	if _, err := resolveLaunch("", "", nil, launchFlags{preset: "payments-errors"}); err == nil {
		t.Fatal("missing catalog should error")
	}
	if _, err := resolveLaunch("query", "", nil, launchFlags{facets: []string{"loglevel=ERROR"}}); err == nil {
		t.Fatal("query --facet should error")
	}
	if _, err := resolveLaunch("slos", "", nil, launchFlags{facets: []string{"id=x"}}); err == nil {
		t.Fatal("API-backed --facet should error")
	}
	if _, err := resolveLaunch("nav", "", nil, launchFlags{facets: []string{"id=x"}}); err == nil {
		t.Fatal("nav --facet should error")
	}
	if _, err := resolveLaunch("", "", nil, launchFlags{facets: []string{"loglevel=ERROR"}}); err == nil {
		t.Fatal("--facet without a view should error")
	}
	if _, err := resolveLaunch("not-a-view", "", nil, launchFlags{}); err == nil {
		t.Fatal("unknown view should error")
	}
	for _, view := range []string{"", "nav", "query"} {
		if _, err := resolveLaunch(view, "", nil, launchFlags{}); err != nil {
			t.Errorf("plain launch of %q: %v", view, err)
		}
	}
	if msg, err := resolveLaunch("logs", "", nil, launchFlags{facets: []string{"loglevel=ERROR"}}); err != nil || len(msg.Facets) != 1 {
		t.Errorf("logs --facet = %+v, %v", msg, err)
	}
}

// The positional argument keeps its command-bar meaning.
func TestResolveLaunchArgument(t *testing.T) {
	cases := []struct {
		view, arg string
		want      tui.NavigateMsg
	}{
		{"", "", tui.NavigateMsg{}},
		{"pods", "checkout", tui.NavigateMsg{Target: "pods", Filter: "checkout"}},
		{"po", "", tui.NavigateMsg{Target: "pods"}},
		{"trace", "4bf92f3577b34da6a3ce929d0e0e4736", tui.NavigateMsg{Target: "trace", ID: "4bf92f3577b34da6a3ce929d0e0e4736"}},
		{"trace", "", tui.NavigateMsg{Target: "traces"}},
		{"smartscape", "payments", tui.NavigateMsg{Target: "nav", ID: "payments"}},
		{"query", "fetch logs | limit 5", tui.NavigateMsg{Target: "query", DQL: "fetch logs | limit 5"}},
		{"dql", "", tui.NavigateMsg{Target: "dql"}},
		{"records", "", tui.NavigateMsg{Target: "tables"}},
	}
	for _, c := range cases {
		got, err := resolveLaunch(c.view, c.arg, nil, launchFlags{})
		if err != nil || got.Target != c.want.Target || got.ID != c.want.ID || got.Filter != c.want.Filter || got.DQL != c.want.DQL {
			t.Errorf("dtui %s %s = %+v, %v; want %+v", c.view, c.arg, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"home", "x"}, {"bogus", ""}, {"bogus", "x"}} {
		if _, err := resolveLaunch(c[0], c[1], nil, launchFlags{}); err == nil {
			t.Errorf("dtui %s %s: want an error", c[0], c[1])
		}
	}
}

func TestResolveLaunchHandoffFlags(t *testing.T) {
	p, err := resolveLaunch("logs", "", nil, launchFlags{
		searches: []string{"timeout", " gateway "},
		from:     "2026-01-02T14:00Z",
		to:       "2026-01-02T15:30:00+01:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Searches) != 2 || p.Searches[1] != "gateway" {
		t.Errorf("searches = %q", p.Searches)
	}
	wantFrom := time.Date(2026, 1, 2, 14, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	if !p.From.Equal(wantFrom) || !p.To.Equal(wantTo) || p.Timeframe != "" {
		t.Errorf("window = %v → %v (tf %q)", p.From, p.To, p.Timeframe)
	}

	p, err = resolveLaunch("problems", "", nil, launchFlags{timeframe: "24h"})
	if err != nil || p.Timeframe != "24h" {
		t.Errorf("--timeframe = %+v, %v", p, err)
	}

	p, err = resolveLaunch("tr", "", nil, launchFlags{lens: "genai"})
	if err != nil || p.Target != "traces" || p.Lens != "genai" {
		t.Errorf("--lens = %+v, %v", p, err)
	}

	p, err = resolveLaunch("", "", nil, launchFlags{dql: "  fetch logs | limit 5\n"})
	if err != nil || p.Target != "query" || p.DQL != "fetch logs | limit 5" {
		t.Errorf("--dql without a view = %+v, %v", p, err)
	}

	file := filepath.Join(t.TempDir(), "q.dql")
	if err := os.WriteFile(file, []byte("fetch spans\n| limit 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err = resolveLaunch("dql", "", nil, launchFlags{file: file})
	if err != nil || p.DQL != "fetch spans\n| limit 3" {
		t.Errorf("--file = %+v, %v", p, err)
	}

	for name, tc := range map[string]struct {
		view, arg string
		f         launchFlags
	}{
		"search without a view":     {"", "", launchFlags{searches: []string{"x"}}},
		"empty search":              {"logs", "", launchFlags{searches: []string{" "}}},
		"search on API view":        {"slos", "", launchFlags{searches: []string{"x"}}},
		"search on a trace id":      {"trace", "4bf92f3577b34da6a3ce929d0e0e4736", launchFlags{searches: []string{"x"}}},
		"unknown lens":              {"traces", "", launchFlags{lens: "nope"}},
		"lens on a trace id":        {"trace", "4bf92f3577b34da6a3ce929d0e0e4736", launchFlags{lens: "genai"}},
		"lens on a bespoke view":    {"home", "", launchFlags{lens: "genai"}},
		"lens without a view":       {"", "", launchFlags{lens: "genai"}},
		"bad timeframe":             {"logs", "", launchFlags{timeframe: "yesterday"}},
		"timeframe with from":       {"logs", "", launchFlags{timeframe: "2h", from: "2026-01-02T14:00Z"}},
		"to without from":           {"logs", "", launchFlags{to: "2026-01-02T14:00Z"}},
		"from without zone":         {"logs", "", launchFlags{from: "2026-01-02T14:00"}},
		"to before from":            {"logs", "", launchFlags{from: "2026-01-02T14:00Z", to: "2026-01-02T13:00Z"}},
		"dql on a table":            {"logs", "", launchFlags{dql: "fetch logs"}},
		"dql and file":              {"", "", launchFlags{dql: "fetch logs", file: file}},
		"positional and dql":        {"query", "fetch logs", launchFlags{dql: "fetch spans"}},
		"query with a window":       {"query", "fetch logs", launchFlags{timeframe: "2h"}},
		"dql with a window":         {"", "", launchFlags{dql: "fetch logs", timeframe: "2h"}},
		"empty dql":                 {"query", "", launchFlags{dql: "   "}},
		"file from stdin":           {"query", "", launchFlags{file: "-"}},
		"missing file":              {"query", "", launchFlags{file: filepath.Join(t.TempDir(), "nope.dql")}},
		"dql with preset":           {"", "", launchFlags{dql: "fetch logs", preset: "x"}},
		"facet on the query view":   {"query", "", launchFlags{facets: []string{"a=b"}, dql: "fetch logs"}},
		"window on the query view":  {"query", "", launchFlags{from: "2026-01-02T14:00Z"}},
		"search on the query view":  {"dql", "fetch logs", launchFlags{searches: []string{"x"}}},
		"argument on home with dql": {"home", "x", launchFlags{dql: "fetch logs"}},
	} {
		if _, err := resolveLaunch(tc.view, tc.arg, nil, tc.f); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func writeWorkspaceFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), workspace.FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewTenantClientNamesAuthLoginOnMissingToken(t *testing.T) {
	cfg := &session.Config{
		CurrentContext: "demo",
		Contexts: []session.NamedContext{
			{Name: "demo", Context: session.Context{Environment: "https://abc12345.apps.dynatrace.com", TokenRef: "missing-token"}},
		},
	}

	_, err := newTenantClient(cfg)

	if err == nil {
		t.Fatal("expected an error for a context whose token does not exist")
	}
	for _, want := range []string{`"demo"`, "dtctl auth login", "--context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}
