package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// RemoteRequest is one request to a listening TUI (ADR-0019): open Page on
// top of the stack, save Page's narrowing as a personal preset, or both.
// A save goes through the same path as W, so the preset lands in the file
// and in this session's :preset list.
type RemoteRequest struct {
	Page   NavigateMsg `json:"page"`
	Open   bool        `json:"open,omitempty"`
	SaveAs string      `json:"save_as,omitempty"`
}

// RemoteResult reports what a request did: the breadcrumb of the page
// opened and the name of the preset saved.
type RemoteResult struct {
	Page  string `json:"page,omitempty"`
	Saved string `json:"saved,omitempty"`
	// Context is the TUI's context: personal presets are saved under it.
	Context string `json:"context,omitempty"`
}

// Remote hands a request to the running TUI and waits for the outcome.
// Options.Listen receives one; the main package serves it on a local socket.
type Remote func(RemoteRequest) (RemoteResult, error)

// remoteReplyTimeout bounds how long a remote caller waits for the app to
// take the message — a TUI blocked behind a modal must not hang the agent.
const remoteReplyTimeout = 10 * time.Second

// remoteMsg carries a RemoteRequest into Update. reply is buffered, so
// answering never blocks Update.
type remoteMsg struct {
	req   RemoteRequest
	reply chan remoteReply
}

type remoteReply struct {
	res RemoteResult
	err error
}

func remoteHandler(p *tea.Program) Remote {
	return func(req RemoteRequest) (RemoteResult, error) {
		reply := make(chan remoteReply, 1)
		go p.Send(remoteMsg{req: req, reply: reply})
		select {
		case r := <-reply:
			return r.res, r.err
		case <-time.After(remoteReplyTimeout):
			return RemoteResult{}, errors.New("dtui did not answer in time")
		}
	}
}

// handleRemote opens and/or saves an agent-requested page. Opening pushes,
// so esc returns the user to where they were, and never switches context:
// the user chose the tenant this TUI shows. A save writes exactly what W
// would on that page, and is refused if any of the page's narrowing was
// dropped, so a preset never silently loses a filter.
func (a *app) handleRemote(msg remoteMsg) tea.Cmd {
	res := RemoteResult{Context: a.opts.ContextName}
	respond := func(err error) {
		if msg.reply != nil {
			msg.reply <- remoteReply{res: res, err: err}
		}
	}
	fail := func(err error) tea.Cmd {
		respond(err)
		return statusErr("agent: " + err.Error())
	}
	req := msg.req
	if !req.Open && req.SaveAs == "" {
		return fail(errors.New("request neither opens a page nor saves a preset"))
	}

	// Opening never switches context, and a save lands in this context's
	// presets, so a request naming another context is refused either way.
	if c := strings.TrimSpace(req.Page.Context); c != "" && c != a.opts.ContextName {
		return fail(fmt.Errorf("this dtui is on context %q, not %q; it will not switch", a.opts.ContextName, c))
	}
	var view viewModel
	var warns []string
	var cmd tea.Cmd
	if req.Open {
		prevTF := a.tf
		v, w, err := a.launchView(req.Page)
		if err != nil {
			return fail(err)
		}
		view, warns = v, w
		cmd = a.navigate(view, false)
		if a.tf != prevTF {
			a.prev = nil
		}
		res.Page = view.Crumb()
	} else {
		v, err := a.launchPage(req.Page, a.tf, &warns)
		if err != nil {
			return fail(err)
		}
		view = v
	}

	var done []string
	if res.Page != "" {
		done = append(done, "opened "+res.Page)
	}
	if req.SaveAs != "" {
		if err := a.saveRemotePreset(req.SaveAs, view, warns); err != nil {
			return tea.Batch(cmd, fail(err))
		}
		res.Saved = req.SaveAs
		done = append(done, "saved preset "+req.SaveAs)
	}
	if len(warns) > 0 {
		w := strings.Join(warns, "; ")
		respond(fmt.Errorf("with warnings: %s", w))
		return tea.Batch(cmd, statusErr("agent "+strings.Join(done, ", ")+" — "+w))
	}
	respond(nil)
	note := "agent " + strings.Join(done, ", ")
	if res.Page != "" {
		note += " (esc returns)"
	}
	return tea.Batch(cmd, status(note))
}

func (a *app) saveRemotePreset(name string, view viewModel, warns []string) error {
	if len(warns) > 0 {
		return fmt.Errorf("not saving preset %s: %s", name, strings.Join(warns, "; "))
	}
	tv, ok := view.(*tableView)
	if !ok {
		return errors.New("presets need a DQL catalog table")
	}
	draft, err := tv.presetDraft()
	if err != nil {
		return err
	}
	return a.savePreset(name, draft)
}
