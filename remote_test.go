package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/tui"
)

// shortSocketPath stays under the ~104-byte Unix socket path limit that
// t.TempDir exceeds on macOS.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dtui")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func serveFake(t *testing.T, path string, handle tui.Remote) {
	t.Helper()
	ln, err := listenSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go serveRemote(ln, handle)
}

func TestSendDeliversRequestAndReply(t *testing.T) {
	path := shortSocketPath(t)
	got := make(chan tui.RemoteRequest, 1)
	serveFake(t, path, func(req tui.RemoteRequest) (tui.RemoteResult, error) {
		got <- req
		return tui.RemoteResult{Page: "logs", Saved: req.SaveAs}, nil
	})
	want := tui.RemoteRequest{Open: true, SaveAs: "errs", Page: tui.NavigateMsg{Target: "logs", Searches: []string{"timeout"},
		Facets: []tui.NavFacet{{Field: "loglevel", Value: "ERROR"}}}}
	reply, err := sendRemote(path, want)
	if err != nil || reply.Page != "logs" || reply.Saved != "errs" || reply.Error != "" {
		t.Fatalf("reply = %+v, err = %v", reply, err)
	}
	req := <-got
	if !req.Open || req.SaveAs != "errs" || req.Page.Target != "logs" || req.Page.Searches[0] != "timeout" || req.Page.Facets[0] != want.Page.Facets[0] {
		t.Errorf("server got %+v, want %+v", req, want)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v (err %v), want owner-only 0600", fi.Mode().Perm(), err)
	}
}

func TestSendSurfacesTUIErrors(t *testing.T) {
	path := shortSocketPath(t)
	serveFake(t, path, func(tui.RemoteRequest) (tui.RemoteResult, error) {
		return tui.RemoteResult{}, errors.New(`unknown view "nope"`)
	})
	reply, err := sendRemote(path, tui.RemoteRequest{Open: true, Page: tui.NavigateMsg{Target: "nope"}})
	if err != nil || !strings.Contains(reply.Error, "unknown view") {
		t.Fatalf("reply = %+v, err = %v; want the TUI's error passed back", reply, err)
	}
}

func TestSendWithoutListenerSaysHowToStartOne(t *testing.T) {
	_, err := sendRemote(shortSocketPath(t), tui.RemoteRequest{})
	if !errors.Is(err, errNoListener) || !strings.Contains(err.Error(), "dtui --listen") {
		t.Fatalf("err = %v, want a hint to run dtui --listen", err)
	}
}

func TestListenReplacesStaleSocketButNotALiveOne(t *testing.T) {
	path := shortSocketPath(t)
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()

	ln, err := listenSocket(path)
	if err != nil {
		t.Fatalf("stale socket should be replaced: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if _, err := listenSocket(path); err == nil || !strings.Contains(err.Error(), "another dtui") {
		t.Fatalf("err = %v, want a refusal while another dtui listens", err)
	}
}

func TestBadRequestGetsAnError(t *testing.T) {
	path := shortSocketPath(t)
	serveFake(t, path, func(tui.RemoteRequest) (tui.RemoteResult, error) {
		t.Error("the TUI must not see a malformed request")
		return tui.RemoteResult{}, nil
	})
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("not json\n"))
	buf := make([]byte, 256)
	n, _ := c.Read(buf)
	if !strings.Contains(string(buf[:n]), "bad request") {
		t.Fatalf("answer = %q, want bad request", buf[:n])
	}
}

func TestSessionNameCannotEscapeRuntimeDir(t *testing.T) {
	for _, name := range []string{"", "../x", "a/b", strings.Repeat("a", 33)} {
		if _, err := socketPath(name); err == nil {
			t.Errorf("socketPath(%q) accepted", name)
		}
	}
	if p, err := socketPath("work-2"); err != nil || !strings.HasSuffix(p, filepath.Join("dtui", "work-2.sock")) {
		t.Errorf("socketPath(work-2) = %q, %v", p, err)
	}
}
