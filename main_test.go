package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/sdk/session"
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

func TestListPresetsIsACommand(t *testing.T) {
	need := map[string]bool{"list-presets": false, "delete-preset": false}
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

func TestResolveLaunchNarrowing(t *testing.T) {
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
	n, err := resolveLaunchNarrowing("", ws, "payments-errors", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n.view != "logs" || len(n.facets) != 1 || n.facets[0].Value != "ERROR" {
		t.Errorf("preset launch = %+v", n)
	}
	n, err = resolveLaunchNarrowing("logs", ws, "payments-errors", []string{"loglevel=WARN", "k8s.namespace.name=payments"})
	if err != nil {
		t.Fatal(err)
	}
	if len(n.facets) != 2 || n.facets[0].Value != "WARN" || n.facets[1].Field != "k8s.namespace.name" {
		t.Errorf("cli facet merge = %+v", n.facets)
	}
	if _, err := resolveLaunchNarrowing("pods", ws, "payments-errors", nil); err == nil {
		t.Fatal("view mismatch should error")
	}
	if _, err := resolveLaunchNarrowing("", nil, "payments-errors", nil); err == nil {
		t.Fatal("missing catalog should error")
	}
	if _, err := resolveLaunchNarrowing("query", nil, "", []string{"loglevel=ERROR"}); err == nil {
		t.Fatal("query --facet should error")
	}
	if _, err := resolveLaunchNarrowing("slos", nil, "", []string{"id=x"}); err == nil {
		t.Fatal("API-backed --facet should error")
	}
	if _, err := resolveLaunchNarrowing("nav", nil, "", []string{"id=x"}); err == nil {
		t.Fatal("nav --facet should error")
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
