package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const modulePath = "github.com/dynatrace-oss/dtui"

// dtctlModulePath is the dtctl module dtui builds against, and the prefix of
// the import allowlist below. An allowlist keyed on a stale prefix allows
// nothing, so TestDtctlModulePathIsWiredEverywhere holds it to go.mod.
const dtctlModulePath = "github.com/Dynatrace/dtctl"

// allowedTUIDtctlImports are the only dtctl packages internal/tui may use:
// the DQL executor and the renderers. Anything else (resource handlers,
// clients) would be a path to the tenant that bypasses the sources.go seam
// and, with it, the read-only guarantee (ADR-0011).
var allowedTUIDtctlImports = map[string]bool{
	dtctlModulePath + "/pkg/exec":   true,
	dtctlModulePath + "/pkg/output": true,
}

// dtctlImport matches dtctl or one of its packages (the sdk module included)
// under any owner. dtctl still resolves under the path it moved from, so a
// guard that knew one owner would not see a second dtctl in the build.
var dtctlImport = regexp.MustCompile(`^github\.com/[^/]+/dtctl(?:/|$)`)

// isDtctlImport reports whether imp is dtctl or one of its packages.
func isDtctlImport(imp string) bool {
	return dtctlImport.MatchString(imp)
}

// TestIsDtctlImport pins what the import guards count as dtctl.
func TestIsDtctlImport(t *testing.T) {
	for imp, want := range map[string]bool{
		dtctlModulePath:                             true,
		dtctlModulePath + "/pkg/exec":               true,
		dtctlModulePath + "/sdk/session":            true,
		"github.com/dynatrace-oss/dtctl":            true,
		"github.com/dynatrace-oss/dtctl/pkg/client": true,
		"github.com/Dynatrace/dtctlx/pkg/exec":      false,
		modulePath + "/internal/tui":                false,
		"net/http":                                  false,
	} {
		if got := isDtctlImport(imp); got != want {
			t.Errorf("isDtctlImport(%q) = %v, want %v", imp, got, want)
		}
	}
}

// importsByFile parses the imports of every non-test Go file under root,
// regardless of build tags (the wasm entry points must be covered too).
func importsByFile(t *testing.T, root string) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".deps" || d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			// The literal may be quoted or a raw string; both name one path.
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: import %s: %w", path, imp.Path.Value, err)
			}
			result[path] = append(result[path], importPath)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestImportsByFileReadsEverySpelling pins the walk the import guards rest on:
// an import path written as a raw string is the same import as a quoted one.
func TestImportsByFileReadsEverySpelling(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	src := "package a\n\nimport (\n\t_ `net/http`\n\t_ \"os\"\n)\n"
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{"net/http", "os"}
	if got := importsByFile(t, dir)[file]; !slices.Equal(got, want) {
		t.Errorf("imports of %q are %q, want %q", src, got, want)
	}
}

// TestTUIHasNoDirectTenantAccess pins "no HTTP inside internal/tui" and the
// read-only import graph: every API call goes through an injected Source.
func TestTUIHasNoDirectTenantAccess(t *testing.T) {
	seen := 0
	for file, imports := range importsByFile(t, "internal/tui") {
		for _, imp := range imports {
			if imp == "net/http" || strings.HasPrefix(imp, "net/http/") {
				t.Errorf("%s imports %s: internal/tui must not do HTTP; add a Source in sources.go", file, imp)
			}
			if !isDtctlImport(imp) {
				continue
			}
			seen++
			if !allowedTUIDtctlImports[imp] {
				t.Errorf("%s imports %s: only pkg/exec and pkg/output are allowed in internal/tui", file, imp)
			}
		}
	}
	// internal/tui runs its queries through dtctl's executor, so a walk that
	// meets no dtctl import is not reading the tree, not a clean tree.
	if seen == 0 {
		t.Error("no import under internal/tui is a dtctl package: the allowlist checks nothing")
	}
}

// TestTUIOpensNoListener pins remote requests' shape (ADR-0019): the
// transport lives in the main package, and internal/tui only exposes
// Remote, which carries a page and an optional preset name.
func TestTUIOpensNoListener(t *testing.T) {
	err := filepath.WalkDir("internal/tui", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "net" &&
				(strings.HasPrefix(sel.Sel.Name, "Listen") || strings.HasPrefix(sel.Sel.Name, "Dial")) {
				t.Errorf("%s uses net.%s: internal/tui must not listen or dial; serve remote navigation from the main package", path, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestModulePathMatchesGoMod holds modulePath to go.mod's module line. The
// layering guards below match this module's packages by that prefix, and a
// stale prefix matches nothing and passes whatever the code imports.
func TestModulePathMatchesGoMod(t *testing.T) {
	declared := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(readText(t, "go.mod"))
	if declared == nil || declared[1] != modulePath {
		t.Errorf("go.mod's module line is %q, modulePath is %s", declared, modulePath)
	}
}

// TestOnlyEntryPointsImportTUI pins the layering rule: the TUI shell is
// imported by the main package and the browser entry point (cmd/web) only.
func TestOnlyEntryPointsImportTUI(t *testing.T) {
	tuiPath := modulePath + "/internal/tui"
	for file, imports := range importsByFile(t, ".") {
		dir := filepath.ToSlash(filepath.Dir(file))
		if dir == "." || dir == "cmd/web" || strings.HasPrefix(dir, "internal/tui") {
			continue
		}
		for _, imp := range imports {
			if imp == tuiPath {
				t.Errorf("%s imports internal/tui: only the main package and cmd/web may", file)
			}
		}
	}
}

// TestCatalogIsFrameworkFree pins the catalog as declarative data (ADR-0003):
// no terminal framework and nothing from this module, so a Spec stays a pure
// value any front end can render and test.
func TestCatalogIsFrameworkFree(t *testing.T) {
	for file, imports := range importsByFile(t, "internal/tui/catalog") {
		for _, imp := range imports {
			if strings.HasPrefix(imp, "github.com/charmbracelet/") || strings.HasPrefix(imp, modulePath+"/") {
				t.Errorf("%s imports %s: the catalog is data; rendering and app state live in internal/tui", file, imp)
			}
		}
	}
}

// TestWorkspaceHasNoTenantAccess pins the workspace file as display defaults
// only (ADR-0005): it cannot reach a tenant, so it cannot carry credentials or
// actions.
func TestWorkspaceHasNoTenantAccess(t *testing.T) {
	for file, imports := range importsByFile(t, "internal/workspace") {
		for _, imp := range imports {
			if imp == "net/http" || strings.HasPrefix(imp, "net/http/") ||
				isDtctlImport(imp) || strings.HasPrefix(imp, modulePath+"/") {
				t.Errorf("%s imports %s: internal/workspace holds display defaults only", file, imp)
			}
		}
	}
}

// agentsMDMaxLines keeps AGENTS.md a routing table, not a manual: every agent
// session pays for each line of it.
const agentsMDMaxLines = 150

var (
	mdLink      = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	codePath    = regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:go|md|sh|ya?ml))`")
	codeTest    = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
	testFuncDef = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
)

// TestAgentDocsStayAccurate fails when an agent-facing doc names a file or a
// test that no longer exists, so the docs agents start from cannot silently
// drift from the code.
func TestAgentDocsStayAccurate(t *testing.T) {
	docs := []string{"AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md", ".github/pull_request_template.md",
		"docs/README.md", "docs/dev/tech-debt.md"}
	skills, err := filepath.Glob(".agents/skills/*/SKILL.md")
	if err != nil || len(skills) == 0 {
		t.Fatalf("no skills under .agents/skills: %v", err)
	}
	docs = append(docs, skills...)

	baseNames, tests := map[string]bool{}, map[string]bool{}
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".deps" || d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		baseNames[d.Name()] = true
		if strings.HasSuffix(path, "_test.go") {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range testFuncDef.FindAllStringSubmatch(string(src), -1) {
				tests[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	for _, doc := range docs {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Errorf("%s: %v", doc, err)
			continue
		}
		text := string(raw)
		if doc == "AGENTS.md" {
			if n := strings.Count(text, "\n"); n > agentsMDMaxLines {
				t.Errorf("AGENTS.md has %d lines (max %d): move detail into a routed doc or skill", n, agentsMDMaxLines)
			}
		}
		for _, m := range mdLink.FindAllStringSubmatch(text, -1) {
			target := strings.SplitN(m[1], "#", 2)[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if !exists(filepath.Join(filepath.Dir(doc), target)) {
				t.Errorf("%s links to %s, which does not exist", doc, m[1])
			}
		}
		for _, m := range codePath.FindAllStringSubmatch(text, -1) {
			p := m[1]
			if !strings.Contains(p, "/") && !strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, ".md") {
				continue // a bare config name such as .dtui.yaml is a user file, not a repo path
			}
			if exists(p) || exists(filepath.Join(filepath.Dir(doc), p)) || (!strings.Contains(p, "/") && baseNames[p]) {
				continue
			}
			t.Errorf("%s names %s, which does not exist", doc, p)
		}
		for _, m := range codeTest.FindAllStringSubmatch(text, -1) {
			if !tests[m[1]] {
				t.Errorf("%s names %s, which is not a test in this repo", doc, m[1])
			}
		}
	}
}

// readText returns a repository file with LF line endings: Windows checkouts
// may use CRLF, and the patterns these tests match are newline-sensitive.
func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// TestLintVersionMatchesCI keeps `make lint` and CI on the same golangci-lint:
// a drift lets code pass locally and fail the CI gate, or the reverse.
func TestLintVersionMatchesCI(t *testing.T) {
	makeVer := regexp.MustCompile(`golangci-lint/v2/cmd/golangci-lint@(v[0-9.]+)`).FindStringSubmatch(readText(t, "makefile"))
	ciVer := regexp.MustCompile(`golangci-lint-action@\S+.*\n\s+with:\n\s+version: (v[0-9.]+)`).FindStringSubmatch(readText(t, ".github/workflows/ci.yml"))
	if makeVer == nil || ciVer == nil {
		t.Fatalf("golangci-lint pin not found (makefile %v, ci.yml %v)", makeVer, ciVer)
	}
	if makeVer[1] != ciVer[1] {
		t.Errorf("makefile pins golangci-lint %s, ci.yml %s", makeVer[1], ciVer[1])
	}
}

// TestDtctlModulePathIsWiredEverywhere holds dtctl's module path to the one
// go.mod requires wherever a stale spelling fails silently: the import guards'
// constant, the -X flags of makefile and .goreleaser.yaml, the module copies
// and replaces of wasm/build.sh, and the dependabot group. The linker accepts
// -X for a package that is not in the build and stamps nothing, an allowlist
// keyed on a stale prefix allows nothing, and a group that matches no module
// lets dtctl move without its sdk.
func TestDtctlModulePathIsWiredEverywhere(t *testing.T) {
	// The other checks compare against dtctlModulePath, so their findings
	// mean nothing until go.mod and the constant agree.
	if problems := goModProblems(readText(t, "go.mod")); len(problems) > 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
	var problems []string
	for _, file := range []string{"makefile", ".goreleaser.yaml"} {
		problems = append(problems, ldflagProblems(file, readText(t, file))...)
	}
	// No workflow runs wasm/build.sh, so a stale path there fails only when
	// someone next builds the extension.
	problems = append(problems, wasmScriptProblems(readText(t, "wasm/build.sh"))...)
	problems = append(problems, dependabotProblems(readText(t, ".github/dependabot.yml"))...)
	for _, p := range problems {
		t.Error(p)
	}
}

// withoutCommentLines drops the lines of a makefile, a YAML file or a shell
// script that are a comment from their first character on, so a flag or a
// command that is commented out is not read as a live one.
func withoutCommentLines(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// dtctlModule matches the path of a dtctl module: the root, its sdk, or either
// under a major version suffix such as /dtctl/v2.
var dtctlModule = regexp.MustCompile(`/dtctl(?:/|$)`)

// goModProblems reports a go.mod that requires anything but dtctlModulePath
// and its sdk as a dtctl module. A second dtctl under another owner or major
// version builds, and would be a path to the tenant that the import allowlist
// does not name. Only requirements count: a replace or an exclude names a
// module without adding it to the build.
func goModProblems(gomod string) []string {
	var got []string
	block := "" // the directive of the parenthesised block the line is in
	for _, line := range strings.Split(gomod, "\n") {
		line, _, _ = strings.Cut(line, "//")
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
			continue
		case f[0] == ")":
			block = ""
			continue
		case block == "" && len(f) == 2 && f[1] == "(":
			block = f[0]
			continue
		}
		directive := block
		if block == "" {
			directive, f = f[0], f[1:]
		}
		if directive == "require" && len(f) > 0 && dtctlModule.MatchString(f[0]) {
			got = append(got, f[0])
		}
	}
	slices.Sort(got)
	if want := []string{dtctlModulePath, dtctlModulePath + "/sdk"}; !slices.Equal(got, want) {
		return []string{fmt.Sprintf("go.mod requires the dtctl modules %v, dtctlModulePath expects exactly %v", got, want)}
	}
	return nil
}

var (
	makeVersionPkg = regexp.MustCompile(`(?m)^DTCTL_VERSION_PKG\s*[:?]?=\s*(\S+)`)
	xFlag          = regexp.MustCompile(`-X (\S+)\.(\w+)=`)
	wasmDtctlLine  = regexp.MustCompile(`(?m)^(?:copy_mod|replace) (\S+/dtctl(?:/\S+)?) `)
)

// ldflagProblems reports the -X flags of a makefile or a GoReleaser config
// that do not stamp dtctl's pkg/version. The makefile's variable being right
// proves nothing about the flags meant to use it, so each flag for a dtctl
// package is checked where it is written.
func ldflagProblems(file, text string) []string {
	versionPkg := dtctlModulePath + "/pkg/version"
	text = withoutCommentLines(text)
	var problems []string
	if file == "makefile" {
		switch v := makeVersionPkg.FindStringSubmatch(text); {
		case v == nil:
			problems = append(problems, "makefile: DTCTL_VERSION_PKG not found")
		case v[1] != versionPkg:
			problems = append(problems, fmt.Sprintf("makefile stamps %s, go.mod builds %s", v[1], versionPkg))
		}
	}
	stamped := map[string]bool{}
	for _, m := range xFlag.FindAllStringSubmatch(text, -1) {
		pkg, name := m[1], m[2]
		switch {
		case file == "makefile" && pkg == "$(DTCTL_VERSION_PKG)", pkg == versionPkg:
			stamped[name] = true
		case strings.Contains(pkg, "/dtctl/"):
			problems = append(problems, fmt.Sprintf("%s stamps %s.%s, go.mod builds %s", file, pkg, name, versionPkg))
		}
	}
	for _, name := range []string{"Version", "Commit", "Date"} {
		if !stamped[name] {
			problems = append(problems, fmt.Sprintf("%s does not stamp %s.%s", file, versionPkg, name))
		}
	}
	return problems
}

// wasmScriptProblems reports the module copies and workspace replaces of
// wasm/build.sh that are missing or name another dtctl.
func wasmScriptProblems(text string) []string {
	text = withoutCommentLines(text)
	// Each wanted line must be a whole line of the script: inside another
	// one it may be an echo, or a replace the workspace file comments out
	// with //.
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, strings.TrimRight(line, " \t"))
	}
	var problems []string
	for _, want := range []string{
		"copy_mod " + dtctlModulePath + " dtctl",
		"copy_mod " + dtctlModulePath + "/sdk dtctl-sdk",
		"replace " + dtctlModulePath + " => ./.deps/dtctl",
		"replace " + dtctlModulePath + "/sdk => ./.deps/dtctl-sdk",
	} {
		if !slices.Contains(lines, want) {
			problems = append(problems, fmt.Sprintf("wasm/build.sh lacks %q", want))
		}
	}
	for _, m := range wasmDtctlLine.FindAllStringSubmatch(text, -1) {
		if m[1] != dtctlModulePath && m[1] != dtctlModulePath+"/sdk" {
			problems = append(problems, fmt.Sprintf("wasm/build.sh names %s, go.mod requires %s", m[1], dtctlModulePath))
		}
	}
	return problems
}

// dependabotProblems reports a dependabot config in which no group of the
// gomod update moves dtctlModulePath and its sdk in one pull request: one
// group must name the wildcard that matches both, or both paths. A dtctl
// pattern of any other spelling, one path without the other, and a group
// that excludes dtctl are reported. Which group Dependabot picks when two
// match the same module is not evaluated.
func dependabotProblems(text string) []string {
	var config struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Groups    map[string]struct {
				Patterns []string `yaml:"patterns"`
				Excludes []string `yaml:"exclude-patterns"`
			} `yaml:"groups"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal([]byte(text), &config); err != nil {
		return []string{fmt.Sprintf(".github/dependabot.yml: %v", err)}
	}
	wildcard, sdk := dtctlModulePath+"*", dtctlModulePath+"/sdk"
	partner := map[string]string{dtctlModulePath: sdk, sdk: dtctlModulePath}
	var problems []string
	found := false
	for _, update := range config.Updates {
		if update.Ecosystem != "gomod" {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(update.Groups)) {
			group := update.Groups[name]
			for _, p := range group.Patterns {
				switch {
				case p == wildcard, partner[p] != "" && slices.Contains(group.Patterns, partner[p]):
					found = true
				case partner[p] != "":
					problems = append(problems, fmt.Sprintf(".github/dependabot.yml group %s names %s without %s", name, p, partner[p]))
				case strings.Contains(p, "/dtctl"):
					problems = append(problems, fmt.Sprintf(".github/dependabot.yml group %s names %s, go.mod requires %s", name, p, dtctlModulePath))
				}
			}
			for _, p := range group.Excludes {
				if strings.Contains(p, "/dtctl") {
					problems = append(problems, fmt.Sprintf(".github/dependabot.yml group %s excludes %s", name, p))
				}
			}
		}
	}
	if !found && len(problems) == 0 {
		problems = append(problems, fmt.Sprintf(".github/dependabot.yml has no group with the pattern %q in its gomod update", wildcard))
	}
	return problems
}

// TestDtctlPathChecksReadOnlyLiveLines pins the checks behind
// TestDtctlModulePathIsWiredEverywhere on the inputs they exist for: a stale
// or commented-out spelling is reported, and a valid one that is merely
// written differently is not.
func TestDtctlPathChecksReadOnlyLiveLines(t *testing.T) {
	const old = "github.com/dynatrace-oss/dtctl"
	versionPkg := dtctlModulePath + "/pkg/version"
	flags := func(pkg string) string {
		return "-X main.version=1 -X " + pkg + ".Version=1 -X " + pkg + ".Commit=2 -X " + pkg + ".Date=3\n"
	}
	wasm := "copy_mod " + dtctlModulePath + " dtctl\ncopy_mod " + dtctlModulePath + "/sdk dtctl-sdk\n" +
		"replace " + dtctlModulePath + " => ./.deps/dtctl\nreplace " + dtctlModulePath + "/sdk => ./.deps/dtctl-sdk\n"
	requires := "require (\n\t" + dtctlModulePath + " v1.0.0\n\t" + dtctlModulePath + "/sdk v1.0.0\n)\n"
	// dependabot is a config whose gomod update has the given groups.
	dependabot := func(groups string) string {
		return "version: 2\nupdates:\n  - package-ecosystem: gomod\n    labels:\n      - area/dtctl\n    groups:\n" + groups
	}
	patterns := func(group, key string, items ...string) string {
		return "      " + group + ":\n        " + key + ":\n          - " + strings.Join(items, "\n          - ") + "\n"
	}
	group := patterns("dtctl", "patterns", `"`+dtctlModulePath+`*"`)
	explicit := patterns("dtctl", "patterns", dtctlModulePath, dtctlModulePath+"/sdk")

	for _, tc := range []struct {
		name string
		got  []string
		want string // a substring of the one problem expected; empty for none
	}{
		{"go.mod block", goModProblems("require (\n\t" + dtctlModulePath + " v1.0.0\n\t" + dtctlModulePath + "/sdk v1.0.0 // indirect\n)\n"), ""},
		{"go.mod single-line requires", goModProblems("require " + dtctlModulePath + " v1.0.0\nrequire " + dtctlModulePath + "/sdk v1.0.0\n"), ""},
		{"go.mod replace is not a requirement", goModProblems("require (\n\t" + dtctlModulePath + " v1.0.0\n\t" + dtctlModulePath + "/sdk v1.0.0\n)\nreplace (\n\t" + dtctlModulePath + " v1.0.0 => ../dtctl\n)\n"), ""},
		{"go.mod second dtctl", goModProblems("require (\n\t" + dtctlModulePath + " v1.0.0\n\t" + dtctlModulePath + "/sdk v1.0.0\n\t" + old + " v0.9.0\n)\n"), old},
		{"go.mod without the sdk", goModProblems("require " + dtctlModulePath + " v1.0.0\n"), "expects exactly"},
		{"go.mod exclude is not a requirement", goModProblems(requires + "exclude (\n\t" + old + " v0.9.0\n)\n"), ""},
		{"go.mod second dtctl major", goModProblems(requires + "require (\n\t" + dtctlModulePath + "/v2 v2.0.0\n\t" + dtctlModulePath + "/v2/sdk v2.0.0\n)\n"), dtctlModulePath + "/v2/sdk"},

		{"makefile", ldflagProblems("makefile", "DTCTL_VERSION_PKG = "+versionPkg+"\n"+flags("$(DTCTL_VERSION_PKG)")), ""},
		{"makefile simple assignment", ldflagProblems("makefile", "DTCTL_VERSION_PKG := "+versionPkg+"\n"+flags("$(DTCTL_VERSION_PKG)")), ""},
		{"makefile stale variable", ldflagProblems("makefile", "DTCTL_VERSION_PKG = "+old+"/pkg/version\n"+flags("$(DTCTL_VERSION_PKG)")), "makefile stamps " + old},
		{"makefile flags without Version", ldflagProblems("makefile", "DTCTL_VERSION_PKG = "+versionPkg+"\n-X main.version=1 -X $(DTCTL_VERSION_PKG).Commit=2 -X $(DTCTL_VERSION_PKG).Date=3\n"), "does not stamp " + versionPkg + ".Version"},
		{"makefile stale literal flag", ldflagProblems("makefile", "DTCTL_VERSION_PKG = "+versionPkg+"\n"+flags("$(DTCTL_VERSION_PKG)")+"-X "+old+"/pkg/version.Version=1\n"), "makefile stamps " + old},
		{"makefile commented-out variable", ldflagProblems("makefile", "# DTCTL_VERSION_PKG = "+versionPkg+"\n"+flags("$(DTCTL_VERSION_PKG)")), "DTCTL_VERSION_PKG not found"},
		{"goreleaser", ldflagProblems(".goreleaser.yaml", flags(versionPkg)), ""},
		{"goreleaser flag for a dtui package", ldflagProblems(".goreleaser.yaml", flags(versionPkg)+"-X "+modulePath+"/internal/tui.Build=1\n"), ""},
		{"goreleaser stale flag", ldflagProblems(".goreleaser.yaml", flags(versionPkg)+"-X "+old+"/pkg/version.Version=1\n"), ".goreleaser.yaml stamps " + old},
		{"goreleaser commented-out flag", ldflagProblems(".goreleaser.yaml", "-X "+versionPkg+".Commit=2 -X "+versionPkg+".Date=3\n  # - -X "+versionPkg+".Version=1\n"), "does not stamp " + versionPkg + ".Version"},

		{"wasm", wasmScriptProblems(wasm), ""},
		{"wasm commented-out copy", wasmScriptProblems(strings.Replace(wasm, "copy_mod", "# copy_mod", 1)), `lacks "copy_mod ` + dtctlModulePath + ` dtctl"`},
		{"wasm stale replace", wasmScriptProblems(wasm + "replace " + old + " => ./.deps/old\n"), "names " + old},
		{"wasm replace commented out in the workspace", wasmScriptProblems(strings.Replace(wasm, "replace", "// replace", 1)), `lacks "replace ` + dtctlModulePath + ` => ./.deps/dtctl"`},
		{"wasm copy that is only echoed", wasmScriptProblems(strings.Replace(wasm, "copy_mod", "echo copy_mod", 1)), `lacks "copy_mod ` + dtctlModulePath + ` dtctl"`},
		{"wasm without a final newline", wasmScriptProblems(strings.TrimSuffix(wasm, "\n")), ""},

		{"dependabot", dependabotProblems(dependabot(group)), ""},
		{"dependabot stale group", dependabotProblems(dependabot(strings.Replace(group, dtctlModulePath, old, 1))), "names " + old},
		{"dependabot commented-out group", dependabotProblems(dependabot(strings.Replace(group, "- ", "# - ", 1))), "has no group"},
		{"dependabot single-quoted pattern", dependabotProblems(dependabot(strings.ReplaceAll(group, `"`, "'"))), ""},
		{"dependabot pattern before a comment", dependabotProblems(dependabot(strings.Replace(group, "*\"", "*\" # keep", 1))), ""},
		{"dependabot explicit pair", dependabotProblems(dependabot(explicit)), ""},
		{"dependabot explicit pattern without the sdk", dependabotProblems(dependabot(patterns("dtctl", "patterns", dtctlModulePath))), "without " + dtctlModulePath + "/sdk"},
		{"dependabot pattern under exclude-patterns", dependabotProblems(dependabot(group + patterns("rest", "exclude-patterns", `"`+dtctlModulePath+`*"`))), "group rest excludes"},
		{"dependabot pattern under another ecosystem", dependabotProblems(strings.Replace(dependabot(group), "gomod", "github-actions", 1)), "has no group"},
		{"dependabot not YAML", dependabotProblems("updates: ["), ".github/dependabot.yml: "},
	} {
		switch {
		case tc.want == "" && len(tc.got) > 0:
			t.Errorf("%s: unexpected problems %q", tc.name, tc.got)
		case tc.want != "" && (len(tc.got) != 1 || !strings.Contains(tc.got[0], tc.want)):
			t.Errorf("%s: problems %q, want one containing %q", tc.name, tc.got, tc.want)
		}
	}

	// The explicit pair moves dtctl and its sdk together only as patterns of
	// one group, so each half under a group of its own is reported.
	split := dependabot(patterns("dtctl", "patterns", dtctlModulePath) + patterns("sdk", "patterns", dtctlModulePath+"/sdk"))
	if got := dependabotProblems(split); len(got) != 2 {
		t.Errorf("dependabot explicit pair in two groups: problems %q, want one for each pattern", got)
	}
}
