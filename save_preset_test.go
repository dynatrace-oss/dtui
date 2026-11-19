package main

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/workspace"
)

// runSavePreset runs `dtui save-preset args...` against a throwaway state
// dir and no workspace file, returning stdout and the error.
func runSavePreset(t *testing.T, args ...string) (string, error) {
	t.Helper()
	saveLaunch = launchFlags{}
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"save-preset"}, args...))
	t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	err := rootCmd.Execute()
	return out.String(), err
}

func isolatePresetState(t *testing.T) {
	t.Helper()
	twoContexts(t)
	prev := remoteSession
	remoteSession = "test-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { remoteSession = prev })
}

func TestSavePresetWritesWhatLaunchReads(t *testing.T) {
	isolatePresetState(t)
	out, err := runSavePreset(t, "payments-errors", "logs", "--facet", "loglevel=ERROR", "--search", "payment")
	if err != nil {
		t.Fatalf("save-preset: %v\n%s", err, out)
	}
	w, err := workspace.Load(personalPresetsPath("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := w.LookupPreset("payments-errors")
	if !ok || p.View != "logs" || p.Timeframe != "" || len(p.Facets) != 1 || p.Facets[0].Value != "ERROR" || p.Searches[0] != "payment" {
		t.Fatalf("saved = %+v (found %v)", p, ok)
	}
	nav, err := resolveLaunch("", "", w, launchFlags{preset: "payments-errors"})
	if err != nil || nav.Target != "logs" || len(nav.Facets) != 1 || nav.Searches[0] != "payment" {
		t.Fatalf("--preset round trip = %+v, %v", nav, err)
	}
}

func TestSavePresetReplacesLikeW(t *testing.T) {
	isolatePresetState(t)
	if _, err := runSavePreset(t, "errs", "logs", "--facet", "loglevel=ERROR"); err != nil {
		t.Fatal(err)
	}
	if _, err := runSavePreset(t, "errs", "logs", "--facet", "loglevel=WARN"); err != nil {
		t.Fatal(err)
	}
	w, _ := workspace.Load(personalPresetsPath("alpha"))
	if p, _ := w.LookupPreset("errs"); p.Facets[0].Value != "WARN" {
		t.Errorf("value = %q, want the second save to replace the first", p.Facets[0].Value)
	}
}

func TestSavePresetRejectsWhatAPresetCannotHold(t *testing.T) {
	isolatePresetState(t)
	for name, args := range map[string][]string{
		"no narrowing":   {"x", "logs"},
		"bad name":       {"Not_Kebab", "logs", "--facet", "a=b"},
		"unknown view":   {"x", "nope", "--facet", "a=b"},
		"non-facet view": {"x", "query", "--search", "a"},
		"unsafe field":   {"x", "logs", "--facet", "a`|b=c"},
		"no lens":        {"x", "logs", "--facet", "a=b", "--lens", "genai"},
		"no timeframe":   {"x", "logs", "--facet", "a=b", "--timeframe", "2h"},
	} {
		if _, err := runSavePreset(t, args...); err == nil {
			t.Errorf("%s: save-preset %v succeeded", name, args)
		}
	}
}

func TestSavePresetGoesThroughAListeningTUI(t *testing.T) {
	isolatePresetState(t)
	path, err := socketPath(remoteSession)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan tui.RemoteRequest, 1)
	serveFake(t, path, func(req tui.RemoteRequest) (tui.RemoteResult, error) {
		got <- req
		return tui.RemoteResult{Saved: req.SaveAs}, nil
	})
	out, err := runSavePreset(t, "errs", "logs", "--facet", "loglevel=ERROR")
	if err != nil || !strings.Contains(out, "saved preset errs") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if req := <-got; req.Open || req.SaveAs != "errs" || req.Page.Target != "logs" || req.Page.Context != "" {
		t.Errorf("request = %+v, want a save-only request for logs on the TUI's own context", req)
	}
	if _, err := os.Stat(personalPresetsPath("alpha")); err == nil {
		t.Error("save-preset wrote the file itself; the listening TUI owns the save")
	}
}

func TestSavePresetForwardsAnExplicitContext(t *testing.T) {
	isolatePresetState(t)
	path, err := socketPath(remoteSession)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan tui.RemoteRequest, 1)
	serveFake(t, path, func(req tui.RemoteRequest) (tui.RemoteResult, error) {
		got <- req
		return tui.RemoteResult{}, errors.New("refused")
	})
	contextName = "beta"
	t.Cleanup(func() { contextName = "" })
	if _, err := runSavePreset(t, "errs", "logs", "--facet", "loglevel=ERROR"); err == nil {
		t.Fatal("want the TUI's refusal passed back")
	}
	if req := <-got; req.Page.Context != "beta" {
		t.Errorf("request context = %q, want beta so a TUI on another context refuses", req.Page.Context)
	}
}

func TestSavePresetWithoutListenerUsesTheActiveContext(t *testing.T) {
	isolatePresetState(t)
	contextName = "beta"
	t.Cleanup(func() { contextName = "" })
	out, err := runSavePreset(t, "errs", "logs", "--facet", "loglevel=ERROR")
	if err != nil || !strings.Contains(out, "context beta") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	beta, _ := loadPersonalPresets("beta")
	alpha, _ := loadPersonalPresets("alpha")
	if _, ok := beta.LookupPreset("errs"); !ok {
		t.Error("preset not saved under beta")
	}
	if _, ok := alpha.LookupPreset("errs"); ok {
		t.Error("preset leaked into alpha")
	}
}
