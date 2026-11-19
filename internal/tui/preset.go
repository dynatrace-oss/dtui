package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
	"github.com/dynatrace-oss/dtui/internal/workspace"
)

// NamedPreset is one saved facet/search set for a catalog view. The TUI
// keeps these in memory; persistence is Options.SavePreset (personal YAML).
type NamedPreset struct {
	Name      string
	View      string
	Timeframe string
	Facets    []catalog.Facet
	Searches  []string
	// Personal is true when this copy lives in the context's personal file
	// and can be deleted from the TUI / delete-preset. Workspace (.dtui.yaml)
	// presets stay false.
	Personal bool
}

// requestSavePresetMsg asks the app to prompt for a name and persist the
// current table's server-side narrowing.
type requestSavePresetMsg struct {
	view     string
	tf       string
	facets   []catalog.Facet
	searches []string
}

type presetSaveOverlay struct {
	app      *app
	input    textinput.Model
	view     string
	tf       string
	facets   []catalog.Facet
	searches []string
	err      string
}

func (a *app) openPresetSave(msg requestSavePresetMsg) tea.Cmd {
	if a.opts.SavePreset == nil {
		return statusErr("preset save is not wired in this session")
	}
	if len(msg.facets)+len(msg.searches) == 0 {
		return statusErr("nothing to save — add a facet or server search first")
	}
	ti := newTextInput()
	ti.Prompt = "name: "
	ti.PromptStyle = theme.Crumb
	ti.CharLimit = 48
	ti.Focus()
	a.overlay = &presetSaveOverlay{
		app:      a,
		input:    ti,
		view:     msg.view,
		tf:       msg.tf,
		facets:   append([]catalog.Facet{}, msg.facets...),
		searches: append([]string{}, msg.searches...),
	}
	return textinput.Blink
}

func (o *presetSaveOverlay) Hints() []keyHint {
	return []keyHint{{"enter", "save"}, {"esc", "cancel"}}
}

func (o *presetSaveOverlay) HandleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		o.app.overlay = nil
		return nil
	case "enter":
		name := strings.TrimSpace(o.input.Value())
		draft := requestSavePresetMsg{view: o.view, tf: o.tf, facets: o.facets, searches: o.searches}
		if err := o.app.savePreset(name, draft); err != nil {
			o.err = err.Error()
			return nil
		}
		o.app.overlay = nil
		return status(fmt.Sprintf("saved preset %s — dtui --preset %s", name, name))
	}
	var cmd tea.Cmd
	o.input, cmd = o.input.Update(msg)
	o.err = ""
	return cmd
}

// savePreset persists a personal preset and makes it available to :preset
// in this session. The W dialog and remote saves both go through it.
func (a *app) savePreset(name string, d requestSavePresetMsg) error {
	if a.opts.SavePreset == nil {
		return errors.New("preset save is not wired in this session")
	}
	if !workspace.ValidPresetName(name) {
		return errors.New("name must be kebab-case (a-z, digits, hyphens)")
	}
	p := NamedPreset{Name: name, View: d.view, Timeframe: d.tf, Facets: d.facets, Searches: d.searches, Personal: true}
	if err := a.opts.SavePreset(p); err != nil {
		return err
	}
	if a.presets == nil {
		a.presets = map[string]NamedPreset{}
	}
	a.presets[name] = p
	return nil
}

func (o *presetSaveOverlay) View(width, height int) string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("save preset — "+o.view) + "\n\n")
	b.WriteString(" " + o.input.View() + "\n")
	if o.err != "" {
		b.WriteString("\n " + theme.Error.Render(o.err) + "\n")
	}
	var parts []string
	for _, f := range o.facets {
		parts = append(parts, f.Label())
	}
	for _, s := range o.searches {
		parts = append(parts, "⌕"+s)
	}
	b.WriteString("\n " + theme.Dim.Render(strings.Join(parts, "  ")) + "\n")
	b.WriteString("\n" + theme.Dim.Render("saved for context "+o.app.opts.ContextName+" only — not .dtui.yaml; does not pin the session timeframe"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Position(0.2),
		theme.OverlayBox.Render(b.String()))
}

func (p NamedPreset) source() string {
	if p.Personal {
		return "personal"
	}
	return "workspace"
}

func (p NamedPreset) narrowing() string {
	var parts []string
	if p.Timeframe != "" {
		parts = append(parts, p.Timeframe)
	}
	for _, f := range p.Facets {
		parts = append(parts, f.Label())
	}
	for _, s := range p.Searches {
		parts = append(parts, "⌕"+s)
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, "  ")
}

// presetPicker is the overlay behind `:preset` with no name: name, target
// view, and the saved facets/searches. Enter applies the highlighted preset.
type presetPicker struct {
	app *app
	sel int
}

func (a *app) openPresetPicker() tea.Cmd {
	names := a.presetNames()
	if len(names) == 0 {
		return statusErr("no presets — open f and press W to save one")
	}
	a.overlay = &presetPicker{app: a}
	return nil
}

func (p *presetPicker) Hints() []keyHint {
	return []keyHint{{"enter/1-9", "open"}, {"d", "delete personal"}, {"j/k", "move"}, {"esc", "cancel"}}
}

func (p *presetPicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	names := a.presetNames()
	n := len(names)
	key := msg.String()
	switch key {
	case "esc":
		a.overlay = nil
		return nil
	case "enter":
		a.overlay = nil
		if p.sel >= n || p.sel < 0 {
			return nil
		}
		return a.applyNamedPreset(names[p.sel])
	case "d", "ctrl+x", "delete":
		if p.sel >= n || p.sel < 0 {
			return nil
		}
		return a.deleteNamedPreset(names[p.sel], p)
	}
	if moveSel(key, &p.sel, n) {
		return nil
	}
	if idx, ok := digitIndex(key, n); ok {
		a.overlay = nil
		return a.applyNamedPreset(names[idx])
	}
	return nil
}

func (p *presetPicker) View(width, height int) string {
	a := p.app
	names := a.presetNames()
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("presets") + "  " +
		theme.Dim.Render("name · view · source · saved filters") + "\n\n")
	nameW, viewW, srcW, narrW := 22, 12, 10, 36
	limit := max(height-7, 4)
	overlayRows(&b, len(names), p.sel, limit, func(i int) string {
		pr := a.presets[names[i]]
		// Only 1-9 open a row (digitIndex); later rows stay unnumbered.
		label := "    " + pr.Name
		if i < 9 {
			label = fmt.Sprintf("%d · %s", i+1, pr.Name)
		}
		row := pad(label, nameW) + " " +
			pad(pr.View, viewW) + " " + pad(pr.source(), srcW) + " " + pad(pr.narrowing(), narrW)
		if i == p.sel {
			return theme.Selected.Render(" " + row)
		}
		return " " + theme.HeaderVal.Render(pad(label, nameW)) + " " +
			theme.Badge.Render(pad(pr.View, viewW)) + " " +
			theme.Dim.Render(pad(pr.source(), srcW)) + " " +
			theme.Dim.Render(pad(pr.narrowing(), narrW))
	})
	b.WriteString("\n" + theme.Dim.Render("enter/1-9 open · d delete personal · j/k move · W saves"))
	return centerOverlay(width, height, b.String())
}

func (a *app) deleteNamedPreset(name string, picker *presetPicker) tea.Cmd {
	p, ok := a.presets[name]
	if !ok {
		return nil
	}
	if !p.Personal {
		return statusErr(fmt.Sprintf("preset %s is in .dtui.yaml — edit that file", name))
	}
	if a.opts.DeletePreset == nil {
		return statusErr("preset delete is not wired in this session")
	}
	if err := a.opts.DeletePreset(name); err != nil {
		return statusErr(err.Error())
	}
	delete(a.presets, name)
	detail := strings.TrimSpace(p.View + " " + p.narrowing())
	msg := fmt.Sprintf("deleted preset %s (%s)", name, detail)
	if wp, ok := a.workspacePresets[name]; ok {
		a.presets[name] = wp
		msg = fmt.Sprintf("deleted personal overlay %s (%s) — workspace remains", name, detail)
	}
	names := a.presetNames()
	if len(names) == 0 {
		a.overlay = nil
		return status(msg)
	}
	if picker.sel >= len(names) {
		picker.sel = len(names) - 1
	}
	return status(msg)
}

func (a *app) applyNamedPreset(name string) tea.Cmd {
	name = strings.TrimSpace(name)
	if name == "" {
		return a.openPresetPicker()
	}
	p, ok := a.presets[name]
	if !ok {
		names := a.presetNames()
		if len(names) == 0 {
			return statusErr(fmt.Sprintf("unknown preset %q", name))
		}
		return statusErr(fmt.Sprintf("unknown preset %q (available: %s)", name, strings.Join(names, ", ")))
	}
	spec := catalog.Lookup(p.View)
	if spec == nil {
		return statusErr(fmt.Sprintf("preset %q: unknown view %q", name, p.View))
	}
	tf := a.tf
	if p.Timeframe != "" {
		if parsed, ok := catalog.ParseTimeframe(p.Timeframe); ok {
			tf = parsed
			a.tf = tf
		}
	}
	return func() tea.Msg {
		return pushViewMsg{spec: spec, scope: catalog.Scope{Timeframe: tf}, replace: true, facets: p.Facets, searches: p.Searches}
	}
}

func (a *app) presetNames() []string {
	if len(a.presets) == 0 {
		return nil
	}
	names := make([]string, 0, len(a.presets))
	for n := range a.presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
