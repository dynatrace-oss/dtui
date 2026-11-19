package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/workspace"
)

// Remote navigation (ADR-0019): `dtui --listen` serves a Unix socket that
// accepts one JSON NavigateMsg per connection and answers one remoteReply.
// The socket carries page descriptors only — never tenant data, credentials
// or keystrokes — and is reachable only by the user who owns it.

const defaultSession = "default"

// maxRemoteRequest bounds one request; a query is capped well below it.
const maxRemoteRequest = 1 << 20

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

var sendLaunch launchFlags

var sendCmd = &cobra.Command{
	Use:   "send [view] [arg]",
	Short: "Open a page in a running dtui --listen and exit",
	Long: `send opens a page in the dtui the user started with --listen, on top
of what they are looking at (esc returns). It takes the same view, argument
and flags as a launch, so the command an agent would hand over can drive the
open TUI instead. It renders nothing and does not need a TTY, so it also runs
in agent sessions. It never switches the TUI's context.

Output: "opened <page>" and, with --save, "saved preset <name>"; exit 0.
On error the reason goes to stderr and the exit code is 1; if a filter was
dropped (e.g. an unsafe field) the page still opens, unsaved. If it reports
that no dtui is listening, ask the user to run dtui --listen, or hand them
the same command as dtui <view> ... to run themselves.`,
	Example: `  # In the user's terminal
  dtui --listen

  # From the agent's shell
  dtui send logs --facet loglevel=ERROR --search timeout --timeframe 2h
  dtui send query 'fetch spans | filter request.is_failed | limit 50'
  dtui send trace 4bf92f3577b34da6a3ce929d0e0e4736

  # Open and save as a personal preset, as W would (:preset lists it at once)
  dtui send logs --facet loglevel=ERROR --search timeout --save timeout-errors`,
	Args:              cobra.MaximumNArgs(2),
	ValidArgsFunction: viewCompletion,
	SilenceUsage:      true,
	RunE: func(cmd *cobra.Command, args []string) error {
		var view, arg string
		if len(args) >= 1 {
			view = args[0]
		}
		if len(args) == 2 {
			arg = args[1]
		}
		var catalogWS *workspace.Workspace
		if sendLaunch.preset != "" {
			ctx, err := presetContext()
			if err != nil {
				return err
			}
			var warnings []string
			catalogWS, warnings = loadPresetCatalog(ctx)
			writeLoadWarnings(cmd.ErrOrStderr(), warnings)
		}
		nav, err := resolveLaunch(view, arg, catalogWS, sendLaunch)
		if err != nil {
			return err
		}
		// An explicit --context must match the listening TUI's (it never
		// switches); without one the page opens on the user's context.
		nav.Context = contextOverride()
		path, err := socketPath(remoteSession)
		if err != nil {
			return err
		}
		reply, err := sendRemote(path, tui.RemoteRequest{Page: nav, Open: true, SaveAs: sendSaveAs})
		if err != nil {
			return err
		}
		return reportRemote(cmd.OutOrStdout(), reply)
	},
}

var sendSaveAs string

func init() {
	addLaunchFlags(sendCmd, &sendLaunch)
	sendCmd.Flags().StringVar(&sendSaveAs, "save", "", "also save the opened page's narrowing as this personal preset, as W would")
	rootCmd.AddCommand(sendCmd)
}

// remoteReply is the wire answer to one request.
type remoteReply struct {
	tui.RemoteResult
	Error string `json:"error,omitempty"`
}

// reportRemote prints what the TUI did and returns its error, if any.
func reportRemote(w io.Writer, r remoteReply) error {
	if r.Page != "" {
		fmt.Fprintf(w, "opened %s\n", r.Page)
	}
	if r.Saved != "" {
		fmt.Fprintf(w, "saved preset %s (context %s) — dtui --preset %s\n", r.Saved, r.Context, r.Saved)
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// socketPath is the per-user socket for a session name. The name is
// validated so it cannot walk out of the runtime directory.
func socketPath(name string) (string, error) {
	if !sessionName.MatchString(name) {
		return "", fmt.Errorf("bad --session %q: use 1-32 letters, digits, - or _", name)
	}
	return xdg.RuntimeFile(filepath.Join("dtui", name+".sock"))
}

// listenRemote returns the Options.Listen hook for a session.
func listenRemote(name string) func(tui.Remote) (func(), error) {
	return func(handle tui.Remote) (func(), error) {
		path, err := socketPath(name)
		if err != nil {
			return nil, err
		}
		ln, err := listenSocket(path)
		if err != nil {
			return nil, err
		}
		go serveRemote(ln, handle)
		return func() {
			_ = ln.Close()
			_ = os.Remove(path)
		}, nil
	}
}

// listenSocket binds path with owner-only permissions. A socket left by a
// crashed dtui is replaced; one with a live listener is an error, so two
// TUIs never fight over the same session.
func listenSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, dialErr := net.DialTimeout("unix", path, time.Second); dialErr == nil {
			_ = c.Close()
			return nil, fmt.Errorf("another dtui is listening on session %q — pick another with --session", filepath.Base(path))
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func serveRemote(ln net.Listener, handle tui.Remote) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		serveConn(c, handle)
	}
}

// serveConn serves one request. Connections are handled one at a time:
// requests are ordered, and the TUI shows one page at a time anyway.
func serveConn(c net.Conn, handle tui.Remote) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	var req tui.RemoteRequest
	var reply remoteReply
	if err := json.NewDecoder(io.LimitReader(c, maxRemoteRequest)).Decode(&req); err != nil {
		reply.Error = "bad request: " + err.Error()
	} else {
		res, err := handle(req)
		reply.RemoteResult = res
		if err != nil {
			reply.Error = err.Error()
		}
	}
	_ = json.NewEncoder(c).Encode(reply)
}

// errNoListener means no TUI is serving the session's socket.
var errNoListener = errors.New("no dtui is listening")

// sendRemote delivers one request and returns the TUI's answer.
func sendRemote(path string, req tui.RemoteRequest) (remoteReply, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return remoteReply{}, fmt.Errorf("%w on %s — start one with dtui --listen", errNoListener, path)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return remoteReply{}, err
	}
	var reply remoteReply
	if err := json.NewDecoder(c).Decode(&reply); err != nil {
		return remoteReply{}, fmt.Errorf("reading dtui's answer: %w", err)
	}
	return reply, nil
}
