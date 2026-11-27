package exckey

import (
	"regexp"
	"strings"
)

// Python tracebacks: `File "/path/file.py", line 1, in fn` frames, each
// followed by its source line, the throw site last. An exception group
// prints its own traceback and then one per sub-exception, in a gutter of
// "|" under numbered "+---- 1 ----" rules.

var (
	pythonFrameRe = regexp.MustCompile(`^File "([^"]+)", line \d+(?:, in (.+))?$`)
	// The standard library's directory, as Unix and Windows installs name it.
	pythonStdlibRe = regexp.MustCompile(`^.*?(?:^|/)(?:lib(?:64)?/python\d+(?:\.\d+)*|[Pp]ython\d+/[Ll]ib)/`)
	// The rule above a sub-exception of a group, numbered, and the
	// unnumbered one that closes the group.
	pythonGroupRuleRe = regexp.MustCompile(`^\+[-+]{3,}(?: (\d+) -+)?$`)
	// The line under a traceback: what was raised, and its message. A
	// class is named in any script, and one defined in a function with it
	// ("op.<locals>.Failure").
	pythonRaisedRe = regexp.MustCompile(`^([\p{L}_][\p{L}\p{N}_.<>]*)(?::|$)`)
)

// pythonPackageDirs are where installed packages live, and what their
// modules start with; pythonStdlibDir is what the standard library's start
// with.
var pythonPackageDirs = []string{"site-packages/", "dist-packages/"}

const pythonStdlibDir = "lib/python/"

// pythonTracebacks are the lines a traceback starts with, an exception
// group's last. Such a line is the whole of its line behind the gutter: the
// words inside a message or a line of source text start nothing.
var pythonTracebacks = []string{
	"Traceback (most recent call last):", "Exception Group Traceback (most recent call last):",
}

// pythonPassThrough: the closures decorators wrap a function in. A
// callable's "__call__" and a view's "dispatch" are the application's own
// entry points.
var pythonPassThrough = []string{"wrapper", "wrapped", "inner"}

const (
	// pythonFramePrefix is what pythonFrameRe starts with.
	pythonFramePrefix = `File "`
	// pythonGutter is what an exception group puts before each line.
	pythonGutter = "|+ "
	// pythonIndent is what stands before the text of a line: the gutter
	// and the indentation, in spaces or tabs.
	pythonIndent = pythonGutter + "\t"
)

func pythonFrame(line string) (StackFrame, bool) {
	line = strings.TrimLeft(line, pythonGutter)
	if !strings.HasPrefix(line, pythonFramePrefix) {
		return StackFrame{}, false
	}
	m := pythonFrameRe.FindStringSubmatch(line)
	if m == nil {
		return StackFrame{}, false
	}
	return StackFrame{Module: pythonModule(m[1]), Function: firstNonEmpty(m[2], "<module>"), Syntax: StackPython}, true
}

func pythonOwns(line string) bool {
	_, ok := pythonFrame(line)
	return ok
}

// pythonModule reduces a file to the module it names: an installed package
// is addressed from its site-packages directory, the standard library from
// "lib/python/" whatever the interpreter's version and prefix, a name that
// is no file ("<frozen importlib._bootstrap>") is kept, and anything else is
// the application's and goes through sourceModule.
func pythonModule(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	for _, dir := range pythonPackageDirs {
		if i := lastDirectory(path, dir); i >= 0 {
			return path[i:]
		}
	}
	if loc := pythonStdlibRe.FindStringIndex(path); loc != nil {
		return pythonStdlibDir + path[loc[1]:]
	}
	if strings.HasPrefix(path, "<") {
		return path
	}
	return sourceModule(path)
}

// pythonRootCause returns the frames of the first traceback, which is the
// first exception raised, reversed so that the throw site comes first. An
// exception group is a wrapper around the exceptions it collected, so there
// the first sub-exception is the root, at any depth of nesting; one that
// was never raised has no traceback, and leaves the group itself as the
// root. A traceback's header ends the one before it wherever it stands, so
// an exception handled by raising a group is still the root. Where the root
// is not the last exception of the trace, its type is the one the line
// under its traceback names.
//
// That line stands at the column of its traceback, left of the frames; the
// source text of a frame, with the markers Python draws under it, stands
// right of them. A frame may have several source lines or none, so the
// columns tell the two apart. Only where the indentation was stripped does
// the order have to: there the line after a frame is its source text.
func pythonRootCause(lines []string) (frames []StackFrame, typ string, atFault bool) {
	chained := false // another exception follows the root's, or wraps it
	after := false   // the line follows a frame directly
	column := 0      // of the last frame, behind the gutter of a group
	// Inside the first sub-exception of a group: the group's own traceback.
	var group []StackFrame
	groupType, first := "", false
scan:
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		text := strings.TrimLeft(raw, pythonIndent)
		at := len(raw) - len(text)
		if f, ok := pythonFrame(line); ok {
			// A frame after the line that looked like the end of the
			// traceback: it was not the end.
			frames, typ = append(frames, f), ""
			after, column = true, at
			continue
		}
		source := at > column || at == column && after
		after = false
		rule := pythonGroupRuleRe.FindStringSubmatch(line)
		switch {
		case rule != nil && rule[1] == "1":
			// A member that is a group which was never raised has no
			// traceback either: the group saved above it stays.
			if len(frames) > 0 {
				group, groupType = frames, typ
			}
			frames, typ, chained, first = nil, "", true, true
		case rule != nil && first && len(frames) == 0:
			frames, typ = group, groupType
			break scan
		case len(frames) > 0 && (rule != nil || isPythonTraceback(text)):
			chained = chained || rule == nil
			break scan
		case len(frames) > 0 && !source && typ == "":
			if m := pythonRaisedRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
				typ = m[1]
			}
		}
	}
	if !chained {
		typ = ""
	}
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	return frames, typ, true
}

// isPythonTraceback reports whether a line, without its gutter and
// indentation, starts a traceback.
func isPythonTraceback(text string) bool {
	text = strings.TrimSpace(text)
	for _, header := range pythonTracebacks {
		if text == header {
			return true
		}
	}
	return false
}

func pythonInApp(f StackFrame) bool {
	return !strings.HasPrefix(f.Module, "<") && !strings.HasPrefix(f.Module, pythonStdlibDir) &&
		!hasAnyPrefix(f.Module, pythonPackageDirs)
}
