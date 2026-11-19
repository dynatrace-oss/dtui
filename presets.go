package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/workspace"
)

var listPresetsCmd = &cobra.Command{
	Use:   "list-presets",
	Short: "Print named facet presets and exit",
	Long:  "Print presets from .dtui.yaml and the active context's personal presets (~/.local/state/dtui/presets/<context>.yaml). Does not need a TTY.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := presetContext()
		if err != nil {
			return err
		}
		project, personal, warnings := loadPresetFiles(ctx)
		writeLoadWarnings(cmd.ErrOrStderr(), warnings)
		fmt.Fprintf(cmd.OutOrStdout(), "context: %s\n", catalog.SafeText(ctx))
		writePresetList(cmd.OutOrStdout(), workspace.MergePresetCatalog(project, personal), personal)
		return nil
	},
}

var deletePresetCmd = &cobra.Command{
	Use:               "delete-preset NAME",
	Short:             "Delete a personal named facet preset and exit",
	Long:              "Removes NAME from the active context's personal presets (~/.local/state/dtui/presets/<context>.yaml). Workspace presets in .dtui.yaml are not edited. Does not need a TTY.",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: personalPresetCompletion,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ctx, err := presetContext()
		if err != nil {
			return err
		}
		project, personal, warnings := loadPresetFiles(ctx)
		writeLoadWarnings(cmd.ErrOrStderr(), warnings)
		if _, ok := personal.LookupPreset(name); !ok {
			if _, inWS := project.LookupPreset(name); inWS {
				return fmt.Errorf("preset %q is in .dtui.yaml — edit that file (context %s has no personal %q)", name, ctx, name)
			}
			return fmt.Errorf("unknown personal preset %q in context %s", name, ctx)
		}
		if err := workspace.DeletePreset(personalPresetsPath(ctx), name); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "deleted preset %s (context %s)\n", name, ctx)
		return nil
	},
}

var saveLaunch launchFlags

var savePresetCmd = &cobra.Command{
	Use:   "save-preset NAME VIEW",
	Short: "Save a personal named facet preset and exit",
	Long: `Saves VIEW narrowed by --facet and --search as the personal preset NAME
of the active context, exactly as W would on that page: no timeframe is
pinned, and an existing personal preset of the same name is replaced. When a
dtui --listen is running (see --session) the save goes through it, so its
:preset list has the new preset at once; it saves to that TUI's context and
refuses if --context names another. Otherwise
~/.local/state/dtui/presets/<context>.yaml is written directly. .dtui.yaml
is never edited. Does not need a TTY.`,
	Example: `  dtui save-preset payments-errors logs --facet loglevel=ERROR --search payment
  dtui logs --preset payments-errors`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: savePresetCompletion,
	SilenceUsage:      true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, view := args[0], args[1]
		if !workspace.ValidPresetName(name) {
			return fmt.Errorf("preset name %q must be kebab-case (a-z, digits, hyphens)", name)
		}
		if len(saveLaunch.facets) == 0 && len(saveLaunch.searches) == 0 {
			return fmt.Errorf("a preset needs at least one --facet or --search")
		}
		nav, err := resolveLaunch(view, "", nil, saveLaunch)
		if err != nil {
			return err
		}
		path, err := socketPath(remoteSession)
		if err != nil {
			return err
		}
		// An explicit --context must match the listening TUI's; otherwise the
		// save follows whatever context the user is looking at.
		nav.Context = contextOverride()
		reply, err := sendRemote(path, tui.RemoteRequest{Page: nav, SaveAs: name})
		if !errors.Is(err, errNoListener) {
			if err != nil {
				return err
			}
			return reportRemote(cmd.OutOrStdout(), reply)
		}
		ctx, err := presetContext()
		if err != nil {
			return err
		}
		p := tui.NamedPreset{Name: name, View: canonicalViewName(nav.Target),
			Facets: catalogFacetsFromNav(nav.Facets), Searches: nav.Searches, Personal: true}
		if err := savePersonalPreset(personalPresetsPath(ctx))(p); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "saved preset %s (context %s) — dtui --preset %s\n", name, ctx, name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listPresetsCmd)
	rootCmd.AddCommand(deletePresetCmd)
	savePresetCmd.Flags().StringArrayVar(&saveLaunch.facets, "facet", nil, "server-side field=value filter (repeatable)")
	savePresetCmd.Flags().StringArrayVar(&saveLaunch.searches, "search", nil, "server-side text search (repeatable)")
	rootCmd.AddCommand(savePresetCmd)
}

// savePresetCompletion completes VIEW (the second argument) only.
func savePresetCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 1 {
		return viewCompletion(cmd, nil, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// presetContext is the context a preset subcommand acts on: the same one a
// launch would use (applyWorkspaceContext), so list, save and delete agree
// with the TUI.
func presetContext() (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", err
	}
	ws, _ := loadWorkspace()
	applyWorkspaceContext(cfg, ws)
	if cfg.CurrentContext == "" {
		return "", fmt.Errorf("no current context — personal presets are per context; pick one with --context <name>")
	}
	return cfg.CurrentContext, nil
}

func loadPresetFiles(ctx string) (project, personal *workspace.Workspace, warnings []string) {
	project, warnings = loadWorkspace()
	personal, personalWarn := loadPersonalPresets(ctx)
	if personalWarn != "" {
		warnings = append(warnings, personalWarn)
	}
	return project, personal, warnings
}

func loadPresetCatalog(ctx string) (*workspace.Workspace, []string) {
	project, personal, warnings := loadPresetFiles(ctx)
	return workspace.MergePresetCatalog(project, personal), warnings
}

func writeLoadWarnings(w io.Writer, warnings []string) {
	for _, msg := range warnings {
		fmt.Fprintln(w, msg)
	}
}

// launchFlags is the raw CLI narrowing of the starting page.
type launchFlags struct {
	preset    string
	facets    []string
	searches  []string
	lens      string
	timeframe string
	from, to  string
	dql       string
	file      string
}

// resolveLaunch turns the CLI view, its positional argument and the launch
// flags into the first page — the same descriptor a deep link sends. The
// argument keeps its command-bar meaning; a flag that cannot apply is an
// error, never silently dropped (ADR-0007, ADR-0018). An empty Target means
// no view was named (the caller falls back to the workspace default).
func resolveLaunch(view, arg string, ws *workspace.Workspace, f launchFlags) (tui.NavigateMsg, error) {
	msg := tui.NavigateMsg{Target: view}
	var facets []catalog.Facet

	if f.preset != "" {
		p, err := lookupLaunchPreset(ws, f.preset)
		if err != nil {
			return msg, err
		}
		presetView := canonicalViewName(p.View)
		if err := requireFacetTable(presetView); err != nil {
			return msg, fmt.Errorf("preset %q: %w", f.preset, err)
		}
		if msg.Target != "" && canonicalViewName(msg.Target) != presetView {
			return msg, fmt.Errorf("preset %q is for view %s, not %s", f.preset, p.View, msg.Target)
		}
		msg.Target = presetView
		msg.Timeframe = p.Timeframe
		facets = catalogFacets(p.Facets)
		msg.Searches = append([]string{}, p.Searches...)
	}

	extra, err := parseFacetFlags(f.facets)
	if err != nil {
		return msg, err
	}
	facets = mergeFacets(facets, extra)
	searches, err := parseSearchFlags(f.searches)
	if err != nil {
		return msg, err
	}
	msg.Searches = append(msg.Searches, searches...)

	if err := resolveLaunchDQL(&msg, arg, f); err != nil {
		return msg, err
	}
	if msg.Target == "" && (len(extra) > 0 || len(searches) > 0 || f.lens != "") {
		return msg, fmt.Errorf("--facet, --search and --lens need a view (e.g. dtui logs --facet loglevel=ERROR)")
	}
	if err := resolveLaunchArg(&msg, arg); err != nil {
		return msg, err
	}
	if len(facets) > 0 || len(msg.Searches) > 0 {
		if msg.Target == "trace" {
			return msg, fmt.Errorf("dtui trace <id> opens the trace waterfall; --facet/--search/--preset do not apply")
		}
		if err := requireFacetTable(msg.Target); err != nil {
			return msg, err
		}
	}
	if f.lens != "" {
		if msg.Target == "trace" && msg.ID != "" {
			return msg, fmt.Errorf("dtui trace <id> opens the trace waterfall; --lens does not apply")
		}
		if err := checkLens(msg.Target, f.lens); err != nil {
			return msg, err
		}
		msg.Lens = f.lens
	}
	if err := resolveLaunchWindow(&msg, f); err != nil {
		return msg, err
	}
	msg.Facets = navFacets(facets)
	return msg, nil
}

func lookupLaunchPreset(ws *workspace.Workspace, name string) (workspace.Preset, error) {
	if ws == nil || len(ws.Presets) == 0 {
		return workspace.Preset{}, fmt.Errorf("preset %q: no presets in .dtui.yaml or this context's personal presets", name)
	}
	p, ok := ws.LookupPreset(name)
	if !ok {
		return p, fmt.Errorf("unknown preset %q (available: %s)", name, strings.Join(ws.PresetNames(), ", "))
	}
	if catalog.Lookup(canonicalViewName(p.View)) == nil && !isBespokeView(canonicalViewName(p.View)) {
		return p, fmt.Errorf("preset %q: unknown view %q", name, p.View)
	}
	return p, nil
}

// resolveLaunchArg gives the positional argument its command-bar meaning:
// nav routes it, trace opens the waterfall, a catalog table pre-fills its
// filter. Home takes none; the query view's was consumed as DQL.
func resolveLaunchArg(msg *tui.NavigateMsg, arg string) error {
	switch {
	case msg.Target == "", msg.Target == "home":
		if arg != "" {
			return fmt.Errorf("home takes no argument")
		}
	case canonicalViewName(msg.Target) == "query":
	case isNavView(msg.Target):
		msg.Target, msg.ID = "nav", arg
	case msg.Target == "trace" && arg != "":
		msg.ID = arg
	default:
		spec := catalog.Lookup(msg.Target)
		if spec == nil {
			return fmt.Errorf("unknown view %q (available: home, query, nav, %s)", msg.Target, strings.Join(catalog.Names(), ", "))
		}
		// The record sampler without a table would just re-browse the table
		// catalog — that browser already exists as tables (as in viewFor).
		// Deep links keep reaching records itself (TestEveryViewHasBothExits).
		if spec.Name == "records" {
			spec = catalog.Lookup("tables")
		}
		msg.Target, msg.Filter = spec.Name, arg
	}
	return nil
}

// checkLens rejects a lens the view does not have, naming the ones it does.
func checkLens(view, lens string) error {
	spec := catalog.Lookup(view)
	if spec == nil || isBespokeView(view) {
		return fmt.Errorf("--lens: %s is not a catalog table", view)
	}
	names := make([]string, 0, len(spec.Lenses))
	for _, l := range spec.Lenses {
		if l.Name == lens {
			return nil
		}
		names = append(names, l.Name)
	}
	if len(names) == 0 {
		return fmt.Errorf("--lens: %s has no lenses", spec.Name)
	}
	return fmt.Errorf("--lens %q: %s has %s", lens, spec.Name, strings.Join(names, ", "))
}

// resolveLaunchWindow applies --timeframe or --from/--to; either wins over a
// preset's timeframe. The query view's window belongs in its DQL.
func resolveLaunchWindow(msg *tui.NavigateMsg, f launchFlags) error {
	if f.timeframe == "" && f.from == "" && f.to == "" {
		return nil
	}
	if canonicalViewName(msg.Target) == "query" {
		return fmt.Errorf("the query view ignores --timeframe/--from/--to; put from:/to: in the query instead")
	}
	if f.timeframe != "" && (f.from != "" || f.to != "") {
		return fmt.Errorf("--timeframe and --from/--to are mutually exclusive")
	}
	if f.timeframe != "" {
		if _, ok := catalog.ParseTimeframe(f.timeframe); !ok {
			return fmt.Errorf("--timeframe %q: expected a relative window like 45m, 2h, 7d", f.timeframe)
		}
		msg.Timeframe = f.timeframe
		return nil
	}
	if f.from == "" {
		return fmt.Errorf("--to requires --from")
	}
	from, err := parseLaunchTime("--from", f.from)
	if err != nil {
		return err
	}
	var to time.Time
	if f.to != "" {
		if to, err = parseLaunchTime("--to", f.to); err != nil {
			return err
		}
		if !to.After(from) {
			return fmt.Errorf("--to %s must be after --from %s", f.to, f.from)
		}
	}
	msg.Timeframe, msg.From, msg.To = "", from, to
	return nil
}

// launchTimeLayouts are the accepted --from/--to forms. A zone is required:
// a handed-over command must mean the same instant on every machine.
var launchTimeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"}

func parseLaunchTime(flag, s string) (time.Time, error) {
	for _, layout := range launchTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s %q: expected RFC 3339 with a zone (2026-01-02T15:04Z or 2026-01-02T15:04:05+01:00)", flag, s)
}

// resolveLaunchDQL takes the query the way `dtctl query` does — positional,
// --dql, or -f/--file — and opens the query view with it already running.
func resolveLaunchDQL(msg *tui.NavigateMsg, arg string, f launchFlags) error {
	isQuery := msg.Target != "" && canonicalViewName(msg.Target) == "query"
	given := 0
	for _, s := range []string{f.dql, f.file} {
		if s != "" {
			given++
		}
	}
	if isQuery && arg != "" {
		given++
	}
	if given == 0 {
		return nil
	}
	if given > 1 {
		return fmt.Errorf("give the query once: as an argument, --dql, or -f/--file")
	}
	if msg.Target != "" && !isQuery {
		return fmt.Errorf("--dql and -f/--file open the query view, not %s", msg.Target)
	}
	if f.preset != "" {
		return fmt.Errorf("a query cannot be combined with --preset")
	}
	dql := f.dql
	if isQuery && arg != "" {
		dql = arg
	}
	if f.file != "" {
		if f.file == "-" {
			return fmt.Errorf("-f -: dtui needs the terminal for keyboard input; pass a file path or --dql")
		}
		data, err := os.ReadFile(f.file)
		if err != nil {
			return fmt.Errorf("--file: %w", err)
		}
		dql = string(data)
	}
	if strings.TrimSpace(dql) == "" {
		return fmt.Errorf("the query is empty")
	}
	msg.Target, msg.DQL = "query", strings.TrimSpace(dql)
	return nil
}

func navFacets(in []catalog.Facet) []tui.NavFacet {
	if len(in) == 0 {
		return nil
	}
	out := make([]tui.NavFacet, 0, len(in))
	for _, f := range in {
		out = append(out, tui.NavFacet{Field: f.Field, Value: f.Value, Tokens: f.Tokens,
			MapField: f.MapField, MapKey: f.MapKey})
	}
	return out
}

func catalogFacetsFromNav(in []tui.NavFacet) []catalog.Facet {
	out := make([]catalog.Facet, 0, len(in))
	for _, f := range in {
		out = append(out, catalog.Facet{Field: f.Field, Value: f.Value, Tokens: f.Tokens,
			MapField: f.MapField, MapKey: f.MapKey})
	}
	return out
}

func parseSearchFlags(flags []string) ([]string, error) {
	out := make([]string, 0, len(flags))
	for _, s := range flags {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("--search: term is empty")
		}
		out = append(out, s)
	}
	return out, nil
}

// requireFacetTable rejects views that cannot apply server-side facets: the
// bespoke screens, the navigator, and API-backed catalog specs.
func requireFacetTable(name string) error {
	if isBespokeView(name) || isNavView(name) {
		return fmt.Errorf("view %q is not a catalog table", name)
	}
	spec := catalog.Lookup(name)
	if spec == nil {
		return fmt.Errorf("unknown view %q", name)
	}
	if spec.API != "" {
		return fmt.Errorf("view %q is API-backed and has no DQL facets", name)
	}
	return nil
}

func canonicalViewName(name string) string {
	switch name {
	case "dql":
		return "query"
	case "smartscape", "navigator":
		return "nav"
	}
	if spec := catalog.Lookup(name); spec != nil {
		return spec.Name
	}
	return name
}

func catalogFacets(in []workspace.PresetFacet) []catalog.Facet {
	out := make([]catalog.Facet, 0, len(in))
	for _, f := range in {
		out = append(out, catalog.Facet{
			Field:    f.Field,
			Value:    f.Value,
			Tokens:   f.Tokens,
			MapField: f.MapField,
			MapKey:   f.MapKey,
		})
	}
	return out
}

func parseFacetFlags(flags []string) ([]catalog.Facet, error) {
	var out []catalog.Facet
	for _, s := range flags {
		field, value, ok := strings.Cut(s, "=")
		field, value = strings.TrimSpace(field), strings.TrimSpace(value)
		if !ok || field == "" || value == "" {
			return nil, fmt.Errorf("--facet %q: expected field=value", s)
		}
		if !catalog.SafeFacetField(field) {
			return nil, fmt.Errorf("--facet %q: field contains unsafe characters", s)
		}
		out = append(out, catalog.Facet{Field: field, Value: value})
	}
	return out, nil
}

// mergeFacets ANDs extra onto base; a CLI facet whose Field matches a preset
// facet replaces it (CLI wins on the same field).
func mergeFacets(base, extra []catalog.Facet) []catalog.Facet {
	out := append([]catalog.Facet{}, base...)
	for _, e := range extra {
		replaced := false
		for i, b := range out {
			if b.Field == e.Field {
				out[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}

func writePresetList(w io.Writer, merged, personal *workspace.Workspace) {
	if merged == nil || len(merged.Presets) == 0 {
		fmt.Fprintln(w, "no presets")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVIEW\tSOURCE\tNARROWING")
	for _, name := range merged.PresetNames() {
		p := merged.Presets[name]
		src := "workspace"
		if personal != nil {
			if _, ok := personal.LookupPreset(name); ok {
				src = "personal"
			}
		}
		var parts []string
		for _, f := range p.Facets {
			parts = append(parts, catalog.SafeText(f.Field)+"="+catalog.SafeText(f.Value))
		}
		for _, s := range p.Searches {
			parts = append(parts, "/"+catalog.SafeText(s))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, p.View, src, strings.Join(parts, ", "))
	}
	_ = tw.Flush()
}

func namedPresets(ws *workspace.Workspace, personal bool) []tui.NamedPreset {
	if ws == nil {
		return nil
	}
	out := make([]tui.NamedPreset, 0, len(ws.Presets))
	for _, name := range ws.PresetNames() {
		p := ws.Presets[name]
		out = append(out, tui.NamedPreset{
			Name:      name,
			View:      p.View,
			Timeframe: p.Timeframe,
			Facets:    catalogFacets(p.Facets),
			Searches:  append([]string{}, p.Searches...),
			Personal:  personal,
		})
	}
	return out
}

func mergedNamedPresets(project, personal *workspace.Workspace) []tui.NamedPreset {
	merged := workspace.MergePresetCatalog(project, personal)
	out := namedPresets(merged, false)
	personalNames := map[string]bool{}
	if personal != nil {
		for _, name := range personal.PresetNames() {
			personalNames[name] = true
		}
	}
	for i, p := range out {
		out[i].Personal = personalNames[p.Name]
	}
	return out
}

func savePersonalPreset(path string) func(tui.NamedPreset) error {
	return func(p tui.NamedPreset) error {
		facets := make([]workspace.PresetFacet, 0, len(p.Facets))
		for _, f := range p.Facets {
			facets = append(facets, workspace.PresetFacet{
				Field: f.Field, Value: f.Value, Tokens: f.Tokens,
				MapField: f.MapField, MapKey: f.MapKey,
			})
		}
		return workspace.SavePreset(path, p.Name, workspace.Preset{
			View: p.View, Timeframe: p.Timeframe, Facets: facets, Searches: p.Searches,
		})
	}
}

func deletePersonalPreset(path string) func(name string) error {
	return func(name string) error {
		return workspace.DeletePreset(path, name)
	}
}

// personalPresetsPath is the personal preset file of one dtctl context:
// presets carry tenant-specific values (namespaces, hosts), so each context
// keeps its own, as navigation history does.
func personalPresetsPath(ctx string) string {
	return filepath.Join(xdg.StateHome, "dtui", "presets", presetFileName(ctx))
}

// presetFileName escapes a context name into one safe path element: no
// separators, and no leading dot (so "." or ".." cannot walk the tree).
func presetFileName(ctx string) string {
	name := url.PathEscape(ctx)
	if strings.HasPrefix(name, ".") {
		name = "%2E" + name[1:]
	}
	return name + ".yaml"
}

func loadPersonalPresets(ctx string) (*workspace.Workspace, string) {
	path := personalPresetsPath(ctx)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, ""
		}
		return nil, "personal presets ignored: " + err.Error()
	}
	w, err := workspace.Load(path)
	if err != nil {
		return nil, "personal presets ignored: " + err.Error()
	}
	return w, ""
}
