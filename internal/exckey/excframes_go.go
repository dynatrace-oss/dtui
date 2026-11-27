package exckey

import (
	"regexp"
	"sort"
	"strings"
)

// Go goroutine dumps: "pkg/path.(*Type).Method(0x1, 0x2)" frames, each
// followed by its file line, under a "goroutine N [state]:" header.
//
// Which frames are the application's cannot be read from an import path:
// the dump does not name the main module, and a module path needs no host
// ("shop/internal/cart"). The rule is by elimination. A frame is in-app
// unless
//
//   - its package is the standard library's: the first element of the path
//     is one of goStdlib;
//   - the trace shows it is a dependency: the package sits under a vendor
//     directory, or the file line after the frame is in the module cache
//     (a "module@version" directory, kept by -trimpath) or under vendor/;
//   - its module host is one of goVendorPrefixes.
//
// This takes a dependency for the application where a trace has no file
// lines and the host is not listed, and an application package for the
// standard library where its path starts with one of the library's names.

var (
	// "goroutine 1 [running]:", and the header of a system dump, which
	// names the goroutine's pointers first ("goroutine 1 gp=0x… m=0 […").
	goGoroutineRe = regexp.MustCompile(`^goroutine \d+ (?:[\w=]+ )*\[`)
	goNameRe      = regexp.MustCompile(`^[\p{L}\p{N}_./~%·*()\[\],-]+$`)
	// Arguments are printed as hex words ("0x1f", "0xc000012345?"), in
	// braces where they are the fields of one value, "..." when elided and
	// "_" where the runtime cannot read one.
	goArgsRe     = regexp.MustCompile(`^(?:0x[0-9a-f]+\??|\.\.\.|[{},\s_])*$`)
	goTypeArgsRe = regexp.MustCompile(`\[[^\]]*\]`)
	// The file line under a frame, and what in its path shows a dependency.
	// An absolute path may hold spaces; a system dump prints the frame's
	// pointers after it. The file is Go or, for a frame written in
	// assembly, ".s".
	goFileRe = regexp.MustCompile(
		`^((?:/|[A-Za-z]:[\\/]).*\.(?:go|s)|\S+\.(?:go|s)):\d+(?: \+0x[0-9a-f]+)?(?: fp=0x[0-9a-f]+ sp=0x[0-9a-f]+ pc=0x[0-9a-f]+)?$`)
	goDependencyRe = regexp.MustCompile(`@v\d|/pkg/mod/|(?:^|/)vendor/`)
	// The runtime's entry points of a panic. "panic" itself is printed
	// without a package.
	goPanicRe = regexp.MustCompile(`^(?:gopanic|goPanic\w+|panic\w*|sigpanic\w*|fatalpanic|throw)$`)
	// What the compiler appends to a function for the code it generates
	// from it: a closure ("run.func1", and "glob..func1" for one of a
	// package-level variable), the wrapper of a go or defer statement
	// ("run.gowrap1", "run.deferwrap2"), a closure in a closure
	// ("run.func1.2") and one init function of several ("init.0"). The
	// numbers follow the order in the source, so they move when a closure
	// is added above; the code is the function's, and none stays in a name.
	goGeneratedRe = regexp.MustCompile(`(?:\.+(?:func|gowrap|deferwrap)\d+|\.\d+)+$`)
	// A method's receiver is written "(*Type)" or "(Type)".
	goReceiver = strings.NewReplacer("(*", "", "(", "", ")", "")
)

// goStdlib are the top-level names of the standard library (go list std,
// Go 1.26), sorted.
var goStdlib = []string{
	"archive", "bufio", "bytes", "cmp", "compress", "container", "context", "crypto", "database", "debug", "embed",
	"encoding", "errors", "expvar", "flag", "fmt", "go", "hash", "html", "image", "index", "internal", "io", "iter",
	"log", "maps", "math", "mime", "net", "os", "path", "plugin", "reflect", "regexp", "runtime", "slices", "sort",
	"strconv", "strings", "structs", "sync", "syscall", "testing", "text", "time", "unicode", "unique", "unsafe",
	"vendor", "weak",
}

// goVendorPrefixes are widely used modules and module hosts that are never
// the application itself: the Go project's own, database drivers and the
// cloud SDKs. A trace with file lines shows a dependency without them. An
// entry that ends in "/" is a host or an organisation and matches all
// below it; one that does not is a module, and matches that module and its
// packages ("github.com/lib/pq", not "github.com/lib/pq-tools").
//
// The table is part of the key algorithm: once a release has shipped keys
// it only changes together with ExceptionKeyVersion (see
// javaVendorPrefixes).
var goVendorPrefixes = []string{
	"golang.org/", "google.golang.org/", "go.opentelemetry.io/", "go.uber.org/", "gopkg.in/",
	// Database drivers.
	"github.com/lib/pq", "github.com/jackc/", "github.com/go-sql-driver/", "github.com/mattn/go-sqlite3",
	"go.mongodb.org/", "github.com/redis/", "github.com/go-redis/",
	// Cloud SDKs.
	"github.com/aws/", "github.com/Azure/", "cloud.google.com/", "github.com/googleapis/",
}

// goPassThrough: a handler that wraps the next handler.
var goPassThrough = []string{"ServeHTTP"}

const (
	// goGoroutine is what goGoroutineRe starts with: most lines of most
	// traces do not.
	goGoroutine = "goroutine "
)

// goFileSuffixes are what a line goFileRe matches holds, one of them.
var goFileSuffixes = []string{".go:", ".s:"}

// goVendorDir is the directory a module keeps its dependencies' packages in.
const goVendorDir = "vendor/"

// goFrame reads a frame or a "created by" line. The file line that follows
// each frame matches nothing. A call written in prose ("decode(abc)",
// "failed()") is no frame: a frame names a package, or prints its arguments
// the way the runtime does.
func goFrame(line string) (StackFrame, bool) {
	name, created := strings.CutPrefix(line, "created by ")
	args := ""
	if created {
		name, _, _ = strings.Cut(name, " in goroutine ")
	} else {
		open := strings.LastIndexByte(line, '(')
		if open == -1 || !strings.HasSuffix(line, ")") {
			return StackFrame{}, false
		}
		name, args = line[:open], line[open+1:len(line)-1]
	}
	if !goNameRe.MatchString(name) || !goArgsRe.MatchString(args) ||
		!strings.Contains(name, ".") && args == "" {
		return StackFrame{}, false
	}
	name = goTypeArgsRe.ReplaceAllString(name, "")
	// The package path ends at the first dot after its last slash.
	slash := strings.LastIndexByte(name, '/') + 1
	module, function := "", name
	if dot := strings.IndexByte(name[slash:], '.'); dot >= 0 {
		module, function = name[:slash+dot], name[slash+dot+1:]
	}
	function = goReceiver.Replace(function)
	// Every generated name ends in a digit; few names do.
	if end := len(function) - 1; end >= 0 && function[end] >= '0' && function[end] <= '9' {
		if named := goGeneratedRe.ReplaceAllString(function, ""); named != "" {
			function = named
		}
	}
	if function == "" {
		return StackFrame{}, false
	}
	// "shop/vendor/golang.org/x/net/http2", and the standard library's own
	// "vendor/golang.org/x/net/http2/hpack": the package is the vendored one.
	vendored := false
	if i := strings.LastIndex("/"+module, "/"+goVendorDir); i >= 0 {
		module, vendored = module[i+len(goVendorDir):], true
	}
	return StackFrame{Module: module, Function: function, Syntax: StackGo, Dependency: vendored}, true
}

// goOwns: a goroutine header, the file line under a frame, a "created by"
// line, or a frame that prints arguments. A frame without any
// ("main.main()") reads the same as a call in a line of source text or of
// a message.
func goOwns(line string) bool {
	if strings.HasSuffix(line, "()") {
		return false
	}
	if _, ok := goFrame(line); ok {
		return true
	}
	return strings.HasPrefix(line, goGoroutine) && goGoroutineRe.MatchString(line) ||
		containsAny(line, goFileSuffixes) && goFileRe.MatchString(line)
}

// goRootCause returns the frames of the first goroutine, the one that
// panicked, from the throw site down, and whether the trace is a panic's.
// A Go error carries no stack: a trace recorded next to one (debug.Stack in
// a handler that logs it) says where the error was seen, and every error
// that passes there has the same. A panic's trace starts where the fault
// is. It shows by a panic entry of that goroutine: a frame of the runtime's
// (isGoPanic), as a recovered trace has them, or the line a crash starts
// with above it (goPanicHeaders). What another goroutine of the dump is
// doing says nothing about this one.
func goRootCause(lines []string) (frames []StackFrame, typ string, atFault bool) {
	frames, atFault = goFirstGoroutine(lines)
	for i := 0; i < len(frames) && !atFault; i++ {
		atFault = isGoPanic(frames[i])
	}
	return goThrowSite(frames), "", atFault
}

// goAtFault is what goRootCause reports as atFault, without the frames:
// most Go traces are no panic's (an error logged with debug.Stack), and
// their frames go into no key. Up to the first frame every line is read as
// goGoroutineFrames reads it. Below it only a line that can be a panic
// entry is: one that holds what such a frame's name is made of
// (goPanicMarks).
func goAtFault(lines []string) bool {
	framed, headed := false, false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !headed && !framed && hasAnyPrefix(line, goPanicHeaders) {
			return true
		}
		if framed && !containsAny(line, goPanicMarks) {
			continue
		}
		if f, ok := goFrame(line); ok {
			if isGoPanic(f) {
				return true
			}
			framed = true
		} else if strings.HasPrefix(line, goGoroutine) && goGoroutineRe.MatchString(line) {
			if framed {
				return false
			}
			headed = true
		}
	}
	return false
}

// goPanicMarks are what the line of a panic entry holds, one of them
// (isGoPanic): the word, the runtime's package, or the bracket of type
// arguments, which are cut from a name before it is read. The header of a
// goroutine holds a bracket too.
var goPanicMarks = []string{"panic", goRuntimePackage, "["}

// goPanicHeaders start the first line of a crash: a panic, or an error the
// runtime does not let a program recover from.
var goPanicHeaders = []string{"panic: ", "fatal error: "}

// goThrowSite drops the frames above the throw site: the deferred calls that
// ran while panicking, which are printed first, and the runtime's entry into
// the panic. A trace that ends there is kept whole.
func goThrowSite(frames []StackFrame) []StackFrame {
	for i := range frames {
		if !isGoPanic(frames[i]) {
			continue
		}
		for i < len(frames) && isGoPanic(frames[i]) {
			i++
		}
		if i < len(frames) {
			return frames[i:]
		}
		break
	}
	return frames
}

func isGoPanic(f StackFrame) bool {
	return f.Module == "" && f.Function == goPanicWord || f.Module+"." == goRuntimePackage && goPanicRe.MatchString(f.Function)
}

const (
	// goPanicWord is the frame of the built-in, which has no package.
	goPanicWord = "panic"
	// goRuntimePackage is what the name of every function of the runtime
	// starts with.
	goRuntimePackage = "runtime."
)

// goFirstGoroutine reads the frames up to the first header that follows
// one: a dump may start without the header of the goroutine it is about.
// crash reports whether the line a crash starts with stands above them. The
// value of a panic follows that line and may take several, down to the
// first empty line or the goroutine's header; a line of it is no frame,
// though it may read like a call. Where that leaves no frame, the text was
// joined some other way, and every line below the first is read.
func goFirstGoroutine(lines []string) (out []StackFrame, crash bool) {
	if out, crash = goGoroutineFrames(lines, true); crash && len(out) == 0 {
		out, crash = goGoroutineFrames(lines, false)
	}
	return out, crash
}

// goGoroutineFrames is goFirstGoroutine for a panic's value of several
// lines (multiline) or of one.
func goGoroutineFrames(lines []string, multiline bool) (out []StackFrame, crash bool) {
	framed := false // the line before was a frame; this one may be its file
	value := false  // inside the value of a panic
	headed := false // a goroutine header was read
	for _, line := range lines {
		line = strings.TrimSpace(line)
		header := strings.HasPrefix(line, goGoroutine) && goGoroutineRe.MatchString(line)
		if value && !header {
			value = line != ""
			continue
		}
		value = false
		if !headed && len(out) == 0 && hasAnyPrefix(line, goPanicHeaders) {
			crash, value = true, multiline
			continue
		}
		f, ok := goFrame(line)
		switch {
		case ok:
			out = append(out, f)
		case header:
			if len(out) > 0 {
				return out, crash
			}
			headed = true
		// Few file lines hold what shows a dependency; only those are read
		// for where their path ends.
		case framed && goDependencyRe.MatchString(line):
			if m := goFileRe.FindStringSubmatch(line); m != nil && goDependencyRe.MatchString(m[1]) {
				out[len(out)-1].Dependency = true
			}
		}
		framed = ok
	}
	return out, crash
}

func goInApp(f StackFrame) bool {
	return f.Module != "" && !f.Dependency && !isGoStdlib(f.Module) && !isGoVendor(f.Module)
}

// isGoVendor reports whether a package is of one of goVendorPrefixes.
func isGoVendor(module string) bool {
	for _, prefix := range goVendorPrefixes {
		if rest, ok := strings.CutPrefix(module, prefix); ok &&
			(strings.HasSuffix(prefix, "/") || rest == "" || strings.HasPrefix(rest, "/")) {
			return true
		}
	}
	return false
}

// isGoStdlib reports whether a package path starts with a top-level name of
// the standard library.
func isGoStdlib(module string) bool {
	first, _, _ := strings.Cut(module, "/")
	i := sort.SearchStrings(goStdlib, first)
	return i < len(goStdlib) && goStdlib[i] == first
}
