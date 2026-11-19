// Package workspace discovers and parses the per-project .dtui.yaml
// workspace file: committable defaults (filter segments, timeframe, initial
// view, preferred environment, named view presets) that tools apply on top of
// the user's personal dtctl config. Unlike the local .dtctl.yaml (which
// replaces the global config wholesale), a workspace file carries no contexts,
// no credentials, and no executable keys by construction — it can only narrow
// what a session shows, never change what it can do. Consumed by dtui; dtctl
// CLI adoption is planned.
package workspace

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the per-project workspace file, discovered by walking up from
// the working directory (the same walk as dtctl's local .dtctl.yaml).
const FileName = ".dtui.yaml"

// MaxSegments mirrors the Grail limit of filter segments per query.
const MaxSegments = 10

// MaxPrimaryTags is the maximum number of primary_tags.* keys the workspace
// may declare (globally and per-segment combined). Keys beyond this cap return
// a validation error so the file fails loudly instead of silently projecting
// an unexpectedly long column set.
const MaxPrimaryTags = 5

// MaxFacetsPerPreset is the maximum number of facet filters one named preset
// may carry. Each facet becomes one | filter stage on the view's query.
const MaxFacetsPerPreset = 10

// Version is the highest schema version this build understands.
const Version = 1

// Workspace is the parsed .dtui.yaml. All fields are optional; unknown
// keys are ignored so older builds keep working with newer committed files.
// Values are used verbatim — no environment-variable expansion, because a
// committed file must not behave differently per machine.
type Workspace struct {
	Version     int       `yaml:"version,omitempty"`     // schema version; 0 (absent) and 1 accepted
	Environment string    `yaml:"environment,omitempty"` // preferred environment URL (context is picked by match)
	View        string    `yaml:"view,omitempty"`        // initial dtui view (CLI argument wins)
	Timeframe   string    `yaml:"timeframe,omitempty"`   // default timeframe: "30m", "2h", "3d"
	Segments    []Segment `yaml:"segments,omitempty"`    // filter segments applied to every DQL query
	// PrimaryTags are customer-defined tag keys (without the primary_tags.
	// prefix) added as columns on every Smartscape-node and signal view, and as
	// facet candidates. Keys stored bare; the prefix is applied at query-build
	// time.
	PrimaryTags []string `yaml:"primary-tags,omitempty"`
	// Presets are named facet/search sets for a catalog view. They are never
	// applied automatically — the CLI --preset flag or :preset selects one.
	// Personal copies live in ~/.local/state/dtui/presets/<context>.yaml. Keys are
	// kebab-case names.
	Presets map[string]Preset `yaml:"presets,omitempty"`

	path string // discovered file path; never serialized
}

// Preset is a named narrowing of one catalog view: server-side facets and
// optional | search terms, plus an optional timeframe override. View is
// required; at least one facet or search is required.
type Preset struct {
	View      string        `yaml:"view"`
	Timeframe string        `yaml:"timeframe,omitempty"`
	Facets    []PresetFacet `yaml:"facets,omitempty"`
	Searches  []string      `yaml:"searches,omitempty"`
}

// PresetFacet is one field==value (or wildcard / token / map-key) filter.
// Shape matches catalog.Facet so the TUI can apply it without translation
// beyond copying fields — this package must not import the TUI.
type PresetFacet struct {
	Field    string `yaml:"field"`
	Value    string `yaml:"value"`
	Tokens   bool   `yaml:"tokens,omitempty"`
	MapField string `yaml:"map-field,omitempty"`
	MapKey   string `yaml:"map-key,omitempty"`
}

// Segment references a filter segment by name or UID, with optional variable
// bindings for segments that define variables. YAML accepts a bare string
// (the common case) or the map form:
//
//	segments:
//	  - payments-prod
//	  - segment: 4lpVjcpcsjd
//	    variables:
//	      environment: [production]
//	    primary-tags:
//	      - app
//	      - env
type Segment struct {
	Ref       string              `yaml:"segment"`
	Variables map[string][]string `yaml:"variables,omitempty"`
	// PrimaryTags are additional primary_tags.* keys contributed by this
	// segment when it is active. They extend (not replace) the global
	// primary-tags set. Keys stored bare; prefix applied at query-build time.
	PrimaryTags []string `yaml:"primary-tags,omitempty"`
}

// UnmarshalYAML accepts the scalar shorthand ("- payments-prod") alongside
// the map form.
func (s *Segment) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&s.Ref)
	}
	type plain Segment // shed the method to avoid recursion
	return node.Decode((*plain)(s))
}

// Path returns where the workspace file was loaded from.
func (w *Workspace) Path() string {
	return w.path
}

// Find walks up from the current working directory looking for FileName.
func Find() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return findFrom(cwd)
}

// findFrom walks from dir to the filesystem root, mirroring dtctl's local
// config discovery (config.findLocalConfigFrom).
func findFrom(dir string) (string, bool) {
	for {
		path := filepath.Join(dir, FileName)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Discover finds and loads the workspace file for the current working
// directory. Returns (nil, nil) when no file exists; an error means a file
// was found but is unusable — callers should warn and continue without it.
func Discover() (*Workspace, error) {
	path, ok := Find()
	if !ok {
		return nil, nil
	}
	return Load(path)
}

// Load reads, parses, and validates one workspace file.
func Load(path string) (*Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var w Workspace
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := w.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	w.path = path
	return &w, nil
}

// timeframeRe matches the supported timeframe labels: minutes, hours, days.
var timeframeRe = regexp.MustCompile(`^[0-9]+[mhd]$`)

func (w *Workspace) validate() error {
	if w.Version > Version {
		return fmt.Errorf("version %d is newer than this build supports (max %d)", w.Version, Version)
	}
	if len(w.Segments) > MaxSegments {
		return fmt.Errorf("%d segments listed — Grail applies at most %d per query", len(w.Segments), MaxSegments)
	}
	for i, s := range w.Segments {
		if strings.TrimSpace(s.Ref) == "" {
			return fmt.Errorf("segments[%d]: missing segment name or UID", i)
		}
		for name, values := range s.Variables {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("segments[%d] (%s): variable with empty name", i, s.Ref)
			}
			if len(values) == 0 {
				return fmt.Errorf("segments[%d] (%s): variable %q has no values", i, s.Ref, name)
			}
		}
		if err := validatePrimaryTagKeys(s.PrimaryTags, fmt.Sprintf("segments[%d] (%s)", i, s.Ref)); err != nil {
			return err
		}
	}
	if err := validatePrimaryTagKeys(w.PrimaryTags, "primary-tags"); err != nil {
		return err
	}
	if w.Timeframe != "" && !timeframeRe.MatchString(w.Timeframe) {
		return fmt.Errorf("timeframe %q: expected <n>m, <n>h, or <n>d", w.Timeframe)
	}
	if w.Environment != "" {
		if err := validateEnvironment(w.Environment); err != nil {
			return fmt.Errorf("environment %q: %w", w.Environment, err)
		}
	}
	if err := validatePresets(w.Presets); err != nil {
		return err
	}
	// Deduplication is silent — order is preserved, later duplicates dropped.
	w.PrimaryTags = dedupeTrimKeys(w.PrimaryTags)
	for i := range w.Segments {
		w.Segments[i].PrimaryTags = dedupeTrimKeys(w.Segments[i].PrimaryTags)
	}
	return nil
}

// validatePrimaryTagKeys checks a primary-tag key list for empty strings and
// the MaxPrimaryTags cap (per-list, not combined). Empty strings are always
// rejected; the cap is applied to the count of unique trimmed keys so
// duplicate entries don't produce a spurious "exceeded max" error (dedup runs
// after validate and silently drops them).
func validatePrimaryTagKeys(keys []string, context string) error {
	seen := map[string]bool{}
	unique := 0
	for i, k := range keys {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" {
			return fmt.Errorf("%s: primary-tags[%d]: empty key", context, i)
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			unique++
		}
	}
	if unique > MaxPrimaryTags {
		return fmt.Errorf("%s: %d primary tag keys listed — max %d", context, unique, MaxPrimaryTags)
	}
	return nil
}

// dedupeTrimKeys trims whitespace from each key and removes later duplicates,
// preserving order so the user-defined column ordering is respected.
func dedupeTrimKeys(keys []string) []string {
	seen := map[string]bool{}
	out := keys[:0:0] // reuse backing array only if non-nil
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// validateEnvironment accepts an http(s) URL or a bare hostname.
func validateEnvironment(env string) error {
	if strings.ContainsAny(env, " \t") {
		return fmt.Errorf("must not contain whitespace")
	}
	u, err := url.Parse(env)
	if err != nil {
		return err
	}
	if u.Scheme == "" {
		// Bare host form ("abc12345.apps.dynatrace.com").
		if strings.ContainsAny(env, "/@?") {
			return fmt.Errorf("expected an https:// URL or a bare hostname")
		}
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("must not contain credentials")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

// presetNameRe is kebab-case: a letter, then letters, digits or hyphens.
var presetNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func validatePresets(presets map[string]Preset) error {
	for name, p := range presets {
		if !presetNameRe.MatchString(name) {
			return fmt.Errorf("presets: %q: name must be kebab-case (a-z, digits, hyphens)", name)
		}
		if strings.TrimSpace(p.View) == "" {
			return fmt.Errorf("presets.%s: view is required", name)
		}
		if p.Timeframe != "" && !timeframeRe.MatchString(p.Timeframe) {
			return fmt.Errorf("presets.%s: timeframe %q: expected <n>m, <n>h, or <n>d", name, p.Timeframe)
		}
		if len(p.Facets) > MaxFacetsPerPreset {
			return fmt.Errorf("presets.%s: %d facets listed — max %d", name, len(p.Facets), MaxFacetsPerPreset)
		}
		if len(p.Facets)+len(p.Searches) == 0 {
			return fmt.Errorf("presets.%s: needs at least one facet or search", name)
		}
		for i, f := range p.Facets {
			if strings.TrimSpace(f.Field) == "" || strings.TrimSpace(f.Value) == "" {
				return fmt.Errorf("presets.%s: facets[%d]: field and value are required", name, i)
			}
			if unsafeFacetIdent(f.Field) {
				return fmt.Errorf("presets.%s: facets[%d]: field contains unsafe characters", name, i)
			}
			if f.MapField != "" && strings.TrimSpace(f.MapKey) == "" {
				return fmt.Errorf("presets.%s: facets[%d]: map-key is required when map-field is set", name, i)
			}
			if f.MapField != "" && unsafeFacetIdent(f.MapField) {
				return fmt.Errorf("presets.%s: facets[%d]: map-field contains unsafe characters", name, i)
			}
			if f.MapKey != "" && unsafeFacetIdent(f.MapKey) {
				return fmt.Errorf("presets.%s: facets[%d]: map-key contains unsafe characters", name, i)
			}
		}
		for i, s := range p.Searches {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("presets.%s: searches[%d]: empty search", name, i)
			}
		}
	}
	return nil
}

// ValidPresetName reports whether name is kebab-case (a-z, digits, hyphens).
func ValidPresetName(name string) bool {
	return presetNameRe.MatchString(name)
}

// unsafeFacetIdent is the workspace-side copy of catalog.SafeFacetField's
// rejection: this package cannot import catalog. Identifiers are pasted into
// DQL inside backticks with no escaping.
func unsafeFacetIdent(s string) bool {
	return strings.ContainsAny(s, "`|\n\r")
}

// MergePresetCatalog returns a workspace that contains only presets: project
// file first, personal overlay winning on the same name. Either argument may
// be nil. The result is never nil.
func MergePresetCatalog(project, personal *Workspace) *Workspace {
	out := &Workspace{Version: Version, Presets: map[string]Preset{}}
	for _, src := range []*Workspace{project, personal} {
		if src == nil {
			continue
		}
		for name, p := range src.Presets {
			out.Presets[name] = p
		}
	}
	return out
}

func loadPersonalOrEmpty(path string) (*Workspace, error) {
	w := &Workspace{Version: Version, Presets: map[string]Preset{}}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return w, nil
		}
		return nil, err
	}
	loaded, err := Load(path)
	if err != nil {
		return nil, err
	}
	if loaded.Presets == nil {
		loaded.Presets = map[string]Preset{}
	}
	return loaded, nil
}

func writePersonalPresets(path string, w *Workspace) error {
	if w.Presets == nil {
		w.Presets = map[string]Preset{}
	}
	w.Version = Version
	if err := validatePresets(w.Presets); err != nil {
		return err
	}
	type personalFile struct {
		Version int               `yaml:"version"`
		Presets map[string]Preset `yaml:"presets"`
	}
	data, err := yaml.Marshal(personalFile{Version: w.Version, Presets: w.Presets})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// SavePreset writes (or replaces) one named preset in a personal presets
// file. The file holds only version + presets — it is not a full workspace.
func SavePreset(path, name string, p Preset) error {
	if !ValidPresetName(name) {
		return fmt.Errorf("preset name %q must be kebab-case (a-z, digits, hyphens)", name)
	}
	w, err := loadPersonalOrEmpty(path)
	if err != nil {
		return err
	}
	w.Presets[name] = p
	return writePersonalPresets(path, w)
}

// DeletePreset removes one named preset from a personal presets file. It
// does not edit a committed .dtui.yaml.
func DeletePreset(path, name string) error {
	if !ValidPresetName(name) {
		return fmt.Errorf("preset name %q must be kebab-case (a-z, digits, hyphens)", name)
	}
	w, err := loadPersonalOrEmpty(path)
	if err != nil {
		return err
	}
	if _, ok := w.Presets[name]; !ok {
		return fmt.Errorf("preset %q is not in the personal file", name)
	}
	delete(w.Presets, name)
	return writePersonalPresets(path, w)
}

// LookupPreset returns the named preset. Names are matched exactly.
func (w *Workspace) LookupPreset(name string) (Preset, bool) {
	if w == nil || name == "" {
		return Preset{}, false
	}
	p, ok := w.Presets[name]
	return p, ok
}

// PresetNames returns the preset keys in sorted order.
func (w *Workspace) PresetNames() []string {
	if w == nil || len(w.Presets) == 0 {
		return nil
	}
	names := make([]string, 0, len(w.Presets))
	for n := range w.Presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
