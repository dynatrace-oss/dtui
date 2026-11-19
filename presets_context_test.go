package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"

	"github.com/dynatrace-oss/dtui/internal/tui"
)

// twoContexts points dtui at a throwaway dtctl config (current context
// "alpha", also "beta"), a throwaway state dir, and no workspace file.
func twoContexts(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	data := `apiVersion: v1
kind: Config
current-context: alpha
contexts:
  - name: alpha
    context:
      environment: https://alpha.example.invalid
  - name: beta
    context:
      environment: https://beta.example.invalid
`
	if err := os.WriteFile(cfg, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	prevCfg, prevCtx := cfgFile, contextName
	cfgFile, contextName = cfg, ""
	t.Cleanup(func() { cfgFile, contextName = prevCfg, prevCtx })
	t.Setenv("DTCTL_CONTEXT", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	t.Chdir(t.TempDir())
}

func savePreset(t *testing.T, ctx, name string) {
	t.Helper()
	p := tui.NamedPreset{Name: name, View: "logs", Searches: []string{name}}
	if err := savePersonalPreset(personalPresetsPath(ctx))(p); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalPresetsArePerContext(t *testing.T) {
	twoContexts(t)
	savePreset(t, "alpha", "alpha-only")
	savePreset(t, "beta", "beta-only")

	alpha, _ := loadPersonalPresets("alpha")
	beta, _ := loadPersonalPresets("beta")
	if _, ok := alpha.LookupPreset("beta-only"); ok {
		t.Error("alpha sees beta's preset")
	}
	if _, ok := beta.LookupPreset("alpha-only"); ok {
		t.Error("beta sees alpha's preset")
	}
	if _, ok := alpha.LookupPreset("alpha-only"); !ok {
		t.Error("alpha lost its own preset")
	}
}

func TestPresetCommandsFollowTheActiveContext(t *testing.T) {
	twoContexts(t)
	savePreset(t, "alpha", "alpha-only")
	savePreset(t, "beta", "beta-only")

	// --context is registered in main(); setting contextName is what it does.
	run := func(ctx string, args ...string) (string, error) {
		contextName = ctx
		var out bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&out)
		rootCmd.SetArgs(args)
		t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil); contextName = "" })
		err := rootCmd.Execute()
		return out.String(), err
	}

	out, err := run("", "list-presets")
	if err != nil || !strings.Contains(out, "context: alpha") || !strings.Contains(out, "alpha-only") || strings.Contains(out, "beta-only") {
		t.Fatalf("list-presets on the current context = %q, %v", out, err)
	}
	out, err = run("beta", "list-presets")
	if err != nil || !strings.Contains(out, "context: beta") || !strings.Contains(out, "beta-only") || strings.Contains(out, "alpha-only") {
		t.Fatalf("list-presets --context beta = %q, %v", out, err)
	}
	if _, err := run("", "delete-preset", "beta-only"); err == nil {
		t.Error("delete-preset on alpha removed beta's preset")
	}
	if _, err := run("beta", "delete-preset", "beta-only"); err != nil {
		t.Errorf("delete-preset --context beta: %v", err)
	}
}

func TestPresetFileNameStaysOneSafeElement(t *testing.T) {
	for _, ctx := range []string{"prod", "a/b", "..", ".", `a\b`, "prod eu"} {
		name := presetFileName(ctx)
		if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || filepath.Base(name) != name {
			t.Errorf("presetFileName(%q) = %q, not one safe path element", ctx, name)
		}
	}
	if presetFileName("a/b") == presetFileName("a%2Fb.yaml") {
		t.Error("distinct contexts collide")
	}
	if got := filepath.Dir(personalPresetsPath("..")); filepath.Base(got) != "presets" {
		t.Errorf("personalPresetsPath(..) escaped the presets dir: %s", got)
	}
}
