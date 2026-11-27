package exckey

import (
	"regexp"
	"slices"
	"strings"
)

// Stack-frame parsing for exception.stack_trace text. A trace is read in the
// notation of the runtime that wrote it (Java, .NET, JavaScript, Python, Go)
// and each frame is reduced to the module and function it names. Line
// numbers, addresses, argument values and generated names are dropped,
// because they change between builds and processes while the bug stays the
// same. Text in no known notation yields no frames.

// StackSyntax is the notation a frame was written in.
type StackSyntax int

// The zero StackSyntax names no notation, so a frame built without one
// follows no notation's rules.
const (
	// StackJava is "at pkg.Class.method(File.java:1)", with or without "at".
	StackJava StackSyntax = iota + 1
	// StackDotNet is "at Ns.Type.Method(String arg) in File.cs:line 1".
	StackDotNet
	// StackJS is "at fn (/path/file.js:1:2)" or "fn@https://host/file.js:1:2".
	StackJS
	// StackPython is `File "/path/file.py", line 1, in fn`.
	StackPython
	// StackGo is "pkg/path.(*Type).Method(0x…)" from a goroutine dump.
	StackGo
)

// StackFrame is one normalised frame: the class, package or file that holds
// the code, and the function in it.
type StackFrame struct {
	Module   string
	Function string
	Syntax   StackSyntax
	// Dependency is set where the trace itself shows that the frame is
	// third-party code and the module cannot: a Go package under a vendor
	// directory or with its file in the module cache.
	Dependency bool
}

// FrameAuto stands in for a generated class name (proxies, reflection
// accessors) whose real name differs in every process.
const FrameAuto = "<auto>"

// FrameAnonymous is the function of a frame that names none.
const FrameAnonymous = "<anonymous>"

// stackNotation is how one runtime writes a trace. Everything specific to a
// runtime sits behind this type in the runtime's own file
// (excframes_<runtime>.go), so supporting another one takes that file, a
// StackSyntax constant and an entry in stackNotations.
type stackNotation struct {
	// frame reads one line, already trimmed, as a frame.
	frame func(line string) (StackFrame, bool)
	// owns reports whether a line, already trimmed, is one that only this
	// notation writes. Those lines decide which notation a trace is in;
	// frame may read more, once it is known to be this one.
	owns func(line string) bool
	// rootCause reads a trace written in this notation and returns the
	// frames of its root cause, throw site first, and that exception's type
	// where the trace is a chain and names it; a trace of one exception
	// does not have to say what it is, so its type is "". atFault reports
	// whether the frames lead to where the exception arose. They always do
	// where the runtime throws, since it records the stack at the throw.
	rootCause func(lines []string) (frames []StackFrame, typ string, atFault bool)
	// atFault, where a notation has traces that are not at fault, is what
	// rootCause reports as atFault, found without reading the frames.
	atFault func(lines []string) bool
	// inApp reports whether a frame is application code rather than the
	// runtime's or a framework's.
	inApp func(f StackFrame) bool
	// passThrough are the functions that only hand a call on: a link of a
	// filter or middleware chain, async plumbing. Such a frame is on the
	// stack of every request, whatever fails below it. The lists match by
	// name alone, so they hold only names that say what the function is; a
	// name an application gives its own entry point ("invoke", "step",
	// "dispatch", "__call__") is not on them.
	passThrough []string
}

// stackNotations is indexed by StackSyntax. A trace is read in the notation
// that owns most of its lines (stackNotationOf); the order, from the
// strictest notation to the loosest, decides only between two that own as
// many and start on the same line.
var stackNotations = [...]stackNotation{
	StackJava: {frame: javaFrame, owns: javaOwns, rootCause: javaRootCause, inApp: javaInApp,
		passThrough: javaPassThrough},
	StackDotNet: {frame: dotnetFrame, owns: dotnetOwns, rootCause: dotnetRootCause, inApp: dotnetInApp,
		passThrough: dotnetPassThrough},
	StackJS: {frame: jsFrame, owns: jsOwns, rootCause: jsRootCause, inApp: jsInApp,
		passThrough: jsPassThrough},
	StackPython: {frame: pythonFrame, owns: pythonOwns, rootCause: pythonRootCause, inApp: pythonInApp,
		passThrough: pythonPassThrough},
	StackGo: {frame: goFrame, owns: goOwns, rootCause: goRootCause, atFault: goAtFault, inApp: goInApp,
		passThrough: goPassThrough},
}

// ParseStackFrames extracts the normalised frames of a stack trace, throw
// site first, with adjacent repeats (recursion) collapsed. Where the text
// holds a chain of exceptions the frames are those of the root cause: the
// last "Caused by:" section of a Java trace (completed from the enclosing
// one where it ends in "... N more"), the innermost .NET exception, the
// first Python traceback, the first goroutine. Unparseable text yields nil.
func ParseStackFrames(stack string) []StackFrame {
	frames, _, _ := parseStack(stack)
	return frames
}

// parseStack is ParseStackFrames plus the type of the root cause, where the
// trace is a chain of exceptions and names it, and whether the frames lead
// to where the exception arose (stackNotation.rootCause). Where they do not,
// they are no evidence of which exception this is.
func parseStack(stack string) (frames []StackFrame, typ string, atFault bool) {
	lines := stackLines(stack)
	notation := stackNotationOf(lines)
	if notation == nil {
		return nil, "", false
	}
	frames, typ, atFault = notation.rootCause(lines)
	return collapseFrames(frames), typ, atFault
}

// faultStack is what a key takes of a trace: the frames and the type
// parseStack gives where the trace is at fault, and nothing where it is
// not. A trace that is known not to be at fault before its frames are read
// (stackNotation.atFault) is not read.
func faultStack(stack string) (frames []StackFrame, typ string) {
	lines := stackLines(stack)
	notation := stackNotationOf(lines)
	if notation == nil || notation.atFault != nil && !notation.atFault(lines) {
		return nil, ""
	}
	frames, typ, atFault := notation.rootCause(lines)
	if !atFault {
		return nil, ""
	}
	return collapseFrames(frames), typ
}

// stackNotationOf picks the notation a trace is written in: the one with
// the most lines that only it writes. Of two with as many, the one whose
// first such line comes first, then the one listed first. The first line
// that some notation recognises does not decide alone. A line of the
// message above the frames may read like a call ("cart.validate()"), and a
// frame of one notation like another's: a .NET parameter list like a Java
// source location ("(System.String)"), a Java frame like a .NET one. nil
// when no notation finds a line of its own.
//
// The first stackVoteLines such lines decide. The lines that mislead stand
// above the frames, and are few; reading every line of a long trace twice
// would only cost.
func stackNotationOf(lines []string) *stackNotation {
	var count, first [len(stackNotations)]int
	owned := 0
	for at, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for i := StackJava; int(i) < len(stackNotations); i++ {
			if stackNotations[i].owns(line) {
				if count[i] == 0 {
					first[i] = at
				}
				count[i]++
				owned++
				// A line two notations own is the stricter one's.
				break
			}
		}
		if owned == stackVoteLines {
			break
		}
	}
	best := StackJava
	for i := range count {
		if count[i] > count[best] || count[i] == count[best] && first[i] < first[best] {
			best = StackSyntax(i)
		}
	}
	if count[best] == 0 {
		return nil
	}
	return &stackNotations[best]
}

const (
	// stackVoteLines is how many lines that a notation owns decide which
	// one a trace is in.
	stackVoteLines = 16
	// stackTextMax caps how much of a trace is read, far above what a
	// runtime prints: the text is cut at the last line that ends inside it
	// (stackLines).
	stackTextMax = 1 << 20
	// stackLineMax is the longest line that can be a frame. A longer one is
	// read as an empty line.
	stackLineMax = 4096
)

var (
	stackLineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")
	// stackEscapedBreaks are line breaks and tabs that arrived escaped.
	stackEscapedBreaks = []struct{ escaped, plain string }{{`\r\n`, "\n"}, {`\n`, "\n"}, {`\r`, "\n"}, {`\t`, "\t"}}
	// A Windows path starts with a drive or, on a share, two backslashes.
	stackWindowsPathRe = regexp.MustCompile(`^(?:[A-Za-z]:\\|\\\\)`)
)

// stackIndents are what a line that continues a trace starts with.
var stackIndents = []string{" ", "\t", `\t`}

// stackWordEnds are what ends the word a Windows path is: a path with a
// space in it is not seen whole.
const stackWordEnds = " \t\"'("

// stackLines splits a trace into lines at "\n", "\r\n" or "\r". A trace
// without any line break whose breaks arrived escaped (a backslash and an
// "n") is split at those instead. Only an escaped break followed by
// indentation shows that this happened: a backslash and an "n" alone are
// also part of "C:\app\node_modules". Real breaks at the ends of the text,
// which a shipper of logs adds, say nothing about the ones inside it.
//
// The text is cut to stackTextMax bytes as it arrived, before any pass over
// it, and the line the cut goes through is not read.
func stackLines(stack string) []string {
	cut := len(stack) > stackTextMax
	if cut {
		stack = stack[:stackTextMax]
	}
	if !strings.ContainsAny(strings.Trim(stack, "\r\n"), "\r\n") && (strings.Contains(stack, `\n `) || strings.Contains(stack, `\n\t`)) {
		stack = unescapeBreaks(stack)
	}
	if strings.Contains(stack, "\r") {
		stack = stackLineBreaks.Replace(stack)
	}
	if cut {
		stack = stack[:strings.LastIndexByte(stack, '\n')+1]
	}
	lines := strings.Split(stack, "\n")
	for i, line := range lines {
		if len(line) > stackLineMax {
			lines[i] = ""
		}
	}
	return lines
}

// unescapeBreaks turns escaped line breaks and tabs into real ones. Inside
// a Windows path a backslash before an "n", an "r" or a "t" separates two
// directories ("C:\app\node_modules", "C:\src\tools"), so there only a
// break that indentation follows is one. A path ends at a colon that is not
// its drive's, which stands before a line number ("C:\app\cart.go:10").
func unescapeBreaks(stack string) string {
	var b strings.Builder
	b.Grow(len(stack))
	word := 0 // where the word around i starts
scan:
	for i := 0; i < len(stack); {
		c := stack[i]
		if c == '\\' {
			inPath := stackWindowsPathRe.MatchString(stack[word : i+1])
			for _, br := range stackEscapedBreaks {
				rest, ok := strings.CutPrefix(stack[i:], br.escaped)
				if !ok || inPath && (br.plain != "\n" || !hasAnyPrefix(rest, stackIndents)) {
					continue
				}
				b.WriteString(br.plain)
				i += len(br.escaped)
				word = i
				continue scan
			}
		}
		b.WriteByte(c)
		i++
		if strings.IndexByte(stackWordEnds, c) >= 0 || c == ':' && i-word != len("C:") {
			word = i
		}
	}
	return b.String()
}

// framesOf reads every line that is a frame.
func framesOf(lines []string, frame func(string) (StackFrame, bool)) []StackFrame {
	var out []StackFrame
	for _, line := range lines {
		if f, ok := frame(strings.TrimSpace(line)); ok {
			out = append(out, f)
		}
	}
	return out
}

var (
	// A URL's scheme and host, or a Windows drive, which a file URL has
	// behind a slash ("file:///C:/app").
	sourceOriginRe = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]+://[^/]*(?:/[A-Za-z]:)?|[A-Za-z]:)`)
	// A directory named by a release, not by the code: a timestamp or build
	// number, a version, a commit hash.
	sourceReleaseRe = regexp.MustCompile(`^(?:\d{6,}|v?\d+(?:\.\d+)+(?:[-+][\w.]+)?|[0-9a-f]{7,40})$`)
)

const (
	// sourceCurrentDir is the link a release layout keeps to the live
	// release.
	sourceCurrentDir = "current"
	// sourceURL marks an origin as a URL's, sourceFileScheme as that of a
	// URL which names a file on the host.
	sourceURL        = "://"
	sourceFileScheme = "file:"
)

// sourceReleaseDirs are the directories a deployment keeps its releases in,
// one directory each.
var sourceReleaseDirs = []string{"releases", "versions", "builds", "deployments", "revisions"}

// deployRoots are the directories an application is commonly deployed into,
// each before any shorter root it starts with. deployHomes hold one
// directory per user above the application.
var (
	deployRoots = []string{
		"/usr/src/app/", "/usr/app/", "/home/site/wwwroot/", "/inetpub/wwwroot/", "/var/www/html/", "/var/www/",
		"/var/task/", "/var/app/", "/opt/app/", "/opt/", "/app/", "/srv/", "/workspace/", "/function/", "/code/",
	}
	deployHomes = []string{"/home/", "/Users/"}
)

// sourceModule reduces the path of an application's source file (with "/"
// as its separator) to a module that is the same wherever the application is
// deployed, so that a release does not start a new issue for an old bug:
//
//  1. A URL loses its scheme, host and query, a Windows path its drive, and
//     "./" steps go.
//  2. A path on the host loses the deploy root it starts with: one of
//     deployRoots, or a user's directory under one of deployHomes. The
//     path of a URL other than a file's is not one on the host, and only
//     loses its leading "/".
//  3. If a directory is named by a release — six or more digits (a
//     timestamp, a build number), a version ("1.4.2", "v2.14.0-rc.1"), a
//     hex hash of seven or more characters, or "current" — everything up to
//     and including the last such directory goes. On the host a directory
//     is a release only where one is put: directly below the deploy root,
//     or in one of sourceReleaseDirs. Anywhere else it is the code's own
//     ("api/1.0/handlers.js", "billing/current/invoice.js"). A URL has no
//     such place, so there it counts anywhere ("assets/1.4.2/main.js").
//
// What is left is the path below the deployment: "shop/cart.py" for
// "/app/shop/cart.py" and for "/srv/releases/20261126101112/shop/cart.py".
// A path under a root that is not listed stays whole, which is stable as
// long as the application keeps being deployed there.
func sourceModule(path string) string {
	origin := sourceOriginRe.FindString(path)
	url := strings.Contains(origin, sourceURL) && !strings.HasPrefix(origin, sourceFileScheme)
	path, _, _ = strings.Cut(path[len(origin):], "?")
	path = strings.TrimPrefix(strings.ReplaceAll(path, "/./", "/"), "./")

	rooted := url
	if url {
		path = strings.TrimPrefix(path, "/")
	} else if below, ok := belowDeployRoot(path); ok {
		path, rooted = below, true
	}
	cut, parent := 0, ""
	for start := 0; ; {
		length := strings.IndexByte(path[start:], '/')
		if length < 0 {
			break
		}
		dir := path[start : start+length]
		placed := url || rooted && start == 0 || slices.Contains(sourceReleaseDirs, parent)
		if placed && (dir == sourceCurrentDir || sourceReleaseRe.MatchString(dir) && hasDigit(dir)) {
			cut = start + length + 1
		}
		parent, start = dir, start+length+1
	}
	return path[cut:]
}

// belowDeployRoot cuts the deploy root a path starts with.
func belowDeployRoot(path string) (string, bool) {
	for _, root := range deployRoots {
		if rest, ok := strings.CutPrefix(path, root); ok {
			return rest, true
		}
	}
	for _, home := range deployHomes {
		if rest, ok := strings.CutPrefix(path, home); ok {
			if _, below, ok := strings.Cut(rest, "/"); ok {
				return below, true
			}
		}
	}
	return path, false
}

// lastDirectory returns where the last directory of that name ("name/")
// starts in a path, or -1: a directory whose name only ends that way
// ("custom_node_modules/") is another one.
func lastDirectory(path, dir string) int {
	for end := len(path); ; {
		i := strings.LastIndex(path[:end], dir)
		if i <= 0 || path[i-1] == '/' {
			return i
		}
		end = i
	}
}

// collapseFrames drops a frame that names the same code as the one before
// it, so a recursion counts once however deep it went. Whether the trace
// showed it to be a dependency is not part of what it names: a wrapper the
// compiler generated for a dependency's method has no file to show it by,
// and is a dependency like the method.
func collapseFrames(frames []StackFrame) []StackFrame {
	if len(frames) == 0 {
		return nil
	}
	out := make([]StackFrame, 0, len(frames))
	for _, f := range frames {
		if last := len(out) - 1; last >= 0 && out[last].Module == f.Module && out[last].Function == f.Function &&
			out[last].Syntax == f.Syntax {
			out[last].Dependency = out[last].Dependency || f.Dependency
			continue
		}
		out = append(out, f)
	}
	return out
}

// frameInApp reports whether a frame is application code. The fingerprint
// prefers those: the frames of a runtime or framework are shared by
// unrelated bugs. The known runtime and framework modules of each notation
// are not the application's, everything else is. The rules are part of the
// key algorithm and of ExceptionKeyVersion, and there are no others: a key
// is the same for everybody.
func frameInApp(f StackFrame) bool {
	if f.Syntax < StackJava || int(f.Syntax) >= len(stackNotations) {
		return false
	}
	return stackNotations[f.Syntax].inApp(f)
}

// isPassThrough reports whether a frame only hands the call on: its
// function is one the notation lists, compared whole and without the type
// it is a method of ("Auth.ServeHTTP" is "ServeHTTP").
func isPassThrough(f StackFrame) bool {
	if f.Syntax < StackJava || int(f.Syntax) >= len(stackNotations) {
		return false
	}
	method := f.Function[strings.LastIndexByte(f.Function, '.')+1:]
	for _, name := range stackNotations[f.Syntax].passThrough {
		if name == f.Function || name == method {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsAny(s string, parts []string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}
