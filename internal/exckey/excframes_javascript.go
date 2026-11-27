package exckey

import (
	"regexp"
	"strings"
)

// JavaScript traces. V8 (Node, Chromium) writes "at fn (/path/file.js:1:2)",
// or "at /path/file.js:1:2" for a frame without a function name; Firefox and
// Safari write "fn@https://host/file.js:1:2".

var (
	// "at fn (location)": the function is one name, with "new" before a
	// constructor, "get" or "set" before an accessor and "[as alias]" after
	// a renamed method. Prose after "at" ("at least 3 items (max 5)") has
	// more words. The closing parenthesis may be cut off.
	jsCallRe = regexp.MustCompile(
		`^at (?:async )?((?:new |(?:[\p{L}\p{N}_$.<>#]+\.)?[gs]et )?[\p{L}\p{N}_$.<>#]+(?: \[as [^\]]+\])?) \((.*?)\)?$`)
	// "at name (location)" where the name is any text: V8 prints a
	// function by its display name, which for a method with a string or a
	// symbol for its key, or a bound function, is no identifier ("at
	// cart-added (…)", "at [evt] (…)", "at bound add (…)"). Read only in a
	// trace already known to be JavaScript's, and only before a location
	// with a line and a column or one of V8's words.
	jsDisplayRe = regexp.MustCompile(`^at (?:async )?(.+) \(([^\d\s].*:\d+:\d+|<anonymous>|native)\)$`)
	// "at location": no function name.
	jsBareRe = regexp.MustCompile(`^at (?:async )?(\S+)$`)
	// A location is a file with its line, or one of V8's words for code
	// that has no file. No file starts with a digit; a clock time does.
	jsLocationRe = regexp.MustCompile(`^(?:[^\d\s].*:\d+|<anonymous>|native|index \d+)$`)
	// "fn@location:line:column". Firefox marks an async caller "async*fn".
	jsAtSignRe = regexp.MustCompile(`^(?:[\w ]+\*)?([\p{L}\p{N}_$.<>/\[\]]*)@(\S+:\d+:\d+)$`)

	// A location in a file JavaScript runs, before its line and column.
	jsSourceFileRe = regexp.MustCompile(`\.(?:[cm]?js|jsx|[cm]?ts|tsx)(?:\?[^:]*)?(?::\d+)+$`)

	jsAliasRe = regexp.MustCompile(` \[as [^\]]*\]$`)
	jsIndexRe = regexp.MustCompile(`^index \d+$`)
	jsLinesRe = regexp.MustCompile(`(?::\d+)+`)
	// Node's own files, as it printed them before it had the "node:"
	// scheme: a bare name ("events.js") or one under "internal/". The
	// application's are named by a path.
	jsRuntimeFileRe = regexp.MustCompile(`^(?:internal/[\w/.-]+|[\w-]+)\.js$`)
	// The content hash a bundler puts into a file name ("main.8f3a2b1c.js").
	jsContentHashRe = regexp.MustCompile(`^(.*[^/.-])[.-][0-9a-f]{8,}((?:\.\w+)+)$`)
)

const (
	// jsRuntimeScheme names Node's own modules ("node:events").
	jsRuntimeScheme = "node:"
	// jsDependencyDir is where installed packages live.
	jsDependencyDir = "node_modules/"
)

// jsNoFile are the locations of code that has no file.
var jsNoFile = []string{FrameAnonymous, "native", "index"}

// jsPassThrough: the continuation a middleware chain hands on, and the
// helpers a compiler emits into the application's own files for async
// functions and generators.
var jsPassThrough = []string{
	"next", "_next", "fulfilled", "rejected", "__awaiter", "asyncGeneratorStep", "new Promise",
}

// jsFrameParts reads a frame line as its function and location. named is
// false for "at location", which has no function.
func jsFrameParts(line string) (function, loc string, named, ok bool) {
	// Every form starts with "at " or holds an "@"; most lines of most
	// traces that are not JavaScript's do neither.
	if at := strings.HasPrefix(line, "at "); !at && !strings.Contains(line, "@") {
		return "", "", false, false
	} else if !at {
		if m := jsAtSignRe.FindStringSubmatch(line); m != nil {
			function, loc, named = m[1], m[2], true
		}
	} else if m := jsCallRe.FindStringSubmatch(line); m != nil {
		function, loc, named = m[1], m[2], true
	} else if m := jsBareRe.FindStringSubmatch(line); m != nil {
		loc = m[1]
	}
	if !jsLocationRe.MatchString(loc) {
		return "", "", false, false
	}
	return function, loc, named, true
}

// jsFrame reads more than jsOwns counts: a frame whose function has a
// display name (jsDisplayRe) is one, but does not show what notation a
// trace is in, since prose reads the same.
func jsFrame(line string) (StackFrame, bool) {
	function, loc, _, ok := jsFrameParts(line)
	if !ok {
		m := jsDisplayRe.FindStringSubmatch(line)
		if m == nil {
			return StackFrame{}, false
		}
		function, loc = m[1], m[2]
	}
	return StackFrame{Module: jsModule(loc), Function: jsFunction(function), Syntax: StackJS}, true
}

// jsOwns: a frame with a function, or one whose location is Node's own
// or a file JavaScript runs. "at location" alone is also the file line of a
// Rust or Swift frame, and prose ("at redis.internal:6379").
func jsOwns(line string) bool {
	_, loc, named, ok := jsFrameParts(line)
	return ok && (named || strings.HasPrefix(loc, jsRuntimeScheme) || jsSourceFileRe.MatchString(loc))
}

// jsFunction drops a "[as alias]" suffix; "Object.<anonymous>" is
// module-level code and reads as plain anonymous.
func jsFunction(name string) string {
	name = jsAliasRe.ReplaceAllString(name, "")
	if name == "" || name == "Object."+FrameAnonymous {
		return FrameAnonymous
	}
	return name
}

// jsModule reduces a location to the module it names, without line or
// column:
//
//   - Code without a file keeps V8's word for it, and "index N" (an element
//     of Promise.all) loses its number.
//   - A dependency is addressed from its innermost node_modules directory,
//     wherever that is installed, without the query of a URL.
//   - Node's own code is "node:<name>"; before Node had that scheme it
//     printed a bare file ("events.js") or one under "internal/".
//   - Anything else is the application's and goes through sourceModule,
//     then loses the content hash of a bundle's file name.
func jsModule(loc string) string {
	loc = strings.ReplaceAll(jsLinesRe.ReplaceAllString(loc, ""), `\`, "/")
	switch {
	case jsIndexRe.MatchString(loc):
		return "index"
	case loc == FrameAnonymous, loc == "native", strings.HasPrefix(loc, jsRuntimeScheme):
		return loc
	}
	// What a URL says behind its path names no module.
	loc, _, _ = strings.Cut(loc, "?")
	if i := lastDirectory(loc, jsDependencyDir); i >= 0 {
		return loc[i:]
	}
	if jsRuntimeFileRe.MatchString(loc) {
		return jsRuntimeScheme + strings.TrimSuffix(loc, ".js")
	}
	return jsContentHashRe.ReplaceAllString(sourceModule(loc), "$1$2")
}

func jsRootCause(lines []string) ([]StackFrame, string, bool) {
	return framesOf(lines, jsFrame), "", true
}

func jsInApp(f StackFrame) bool {
	for _, word := range jsNoFile {
		if f.Module == word {
			return false
		}
	}
	return !strings.HasPrefix(f.Module, jsRuntimeScheme) && !strings.HasPrefix(f.Module, jsDependencyDir)
}
