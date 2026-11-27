package exckey

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Java traces: "at pkg.Class.method(File.java:1)" frames under a
// "Type: message" line, the causes of an exception in "Caused by:" sections
// after it.

var (
	// The whole of its line: "... 2 more errors in payload" is a message's.
	javaMoreRe = regexp.MustCompile(`^\.\.\. (\d+) (?:more|common frames omitted)$`)
	// The type a "Caused by:" line names, before its message. Words that no
	// colon follows are prose ("Caused by: connection reset by peer"): no
	// type, and above the frames of a section a line of its message.
	// No type starts with a digit ("Caused by: 503: Service Unavailable").
	javaCauseRe = regexp.MustCompile(`^Caused by: ([\p{L}_$][\p{L}\p{N}_.$]*)(?::|$)`)

	// Classes generated at run time are named after the class they stand in
	// for plus a suffix that differs in every process: CGLIB subclasses
	// ("$$EnhancerBy…$$1a2b", "$$FastClassBy…$$3c4d", "$$SpringCGLIB$$0"),
	// Hibernate, ByteBuddy and Mockito proxies ("$HibernateProxy$kL2x9A"),
	// javassist ("_$$_jvst3c0_1f"); then reflection accessors, dynamic
	// proxies, and the numbered package the JDK puts proxies in.
	javaProxyRe = regexp.MustCompile(
		`(?:\$\$(?:Enhancer|FastClass|SpringCGLIB)|\$(?:HibernateProxy|ByteBuddy|MockitoMock)\$|_\$\$_).*$`)
	javaGeneratedRe = regexp.MustCompile(`(?:Generated(?:Method|Constructor)Accessor|\$Proxy)\d+`)
	javaProxyPkgRe  = regexp.MustCompile(`^jdk\.proxy\d+\.`)
	javaLambdaRe    = regexp.MustCompile(`^lambda\$(.+)\$\d+$`)
	// A Scala closure is a class "Owner$$anonfun$method$1" up to 2.11 and a
	// method "$anonfun$method$1" since; either way it is code of the method.
	javaScalaClassRe  = regexp.MustCompile(`^(.*?)\$\$anonfun\$([\p{L}\p{N}_]+)\$\d+`)
	javaScalaMethodRe = regexp.MustCompile(`^\$anonfun\$([\p{L}\p{N}_]+)\$\d+(?:\$adapted)?$`)
)

// javaVendorPrefixes are the packages that are never the application's:
// the runtime and the frameworks, database drivers, the cloud SDKs, and the
// platform and vendor libraries of Android.
//
// The table is part of the key algorithm. A prefix added to it moves every
// trace with such a frame to another key, so once a release has shipped
// keys the table only changes together with ExceptionKeyVersion.
var javaVendorPrefixes = []string{
	"java.", "javax.", "jakarta.", "jdk.", "sun.", "com.sun.", "kotlin.", "kotlinx.", "scala.", "groovy.",
	"org.springframework.", "org.apache.", "org.hibernate.", "org.jboss.", "org.eclipse.jetty.", "org.slf4j.",
	"io.netty.", "io.grpc.", "io.undertow.", "io.vertx.", "io.quarkus.", "io.micrometer.", "io.opentelemetry.",
	"reactor.", "akka.", "okhttp3.", "ch.qos.logback.", "com.fasterxml.", "com.google.common.", "com.zaxxer.",
	"com.dynatrace.",
	// Further frameworks and libraries.
	"io.micronaut.", "io.ktor.", "org.aspectj.", "net.bytebuddy.", "io.reactivex.", "com.google.gson.",
	"com.rabbitmq.", "com.stripe.", "io.sentry.", "com.squareup.", "retrofit2.",
	// Database drivers and mappers.
	"org.postgresql.", "com.mysql.", "org.mariadb.", "oracle.jdbc.", "com.microsoft.sqlserver.", "org.h2.",
	"com.mongodb.", "org.bson.", "redis.clients.", "io.lettuce.", "com.datastax.", "org.neo4j.driver.",
	"org.jooq.", "org.mybatis.",
	// Cloud SDKs.
	"software.amazon.", "com.amazonaws.", "com.azure.", "com.microsoft.azure.", "com.google.cloud.",
	"com.google.api.", "com.google.protobuf.",
	// Android.
	"android.", "androidx.", "dalvik.", "libcore.", "com.android.", "com.google.android.", "com.google.firebase.",
}

// javaPassThrough: servlet filters, interceptors and the aspects around a
// call. "invoke" is not one of them: a reflective or proxied call is the
// runtime's or a generated class's, which do not count anyway, and an
// application's own invoke is as often where the work is done.
var javaPassThrough = []string{"doFilter", "doFilterInternal", "intercept", "proceed"}

// javaNoSource are the JVM's words for code without a source file, what
// CGLIB writes for a class it generated, and the file name an obfuscator
// leaves, which stands alone where it dropped the line tables too.
// javaUnknownSource is the one that may carry a line all the same
// ("Unknown Source:12", after obfuscation).
var javaNoSource = []string{"Native Method", javaUnknownSource, "Compiled Code", "<generated>", "SourceFile"}

// javaSourceExtensions end the source files of the JVM's languages. A file
// with one of them is Java's even where it stands alone, as it does for a
// class compiled without line tables.
var javaSourceExtensions = []string{".java", ".kt", ".kts", ".scala", ".groovy", ".clj"}

const (
	javaUnknownSource = "Unknown Source"
	// javaNotInFile are the bytes no source file name holds: white space,
	// parentheses, a path separator, a second colon.
	javaNotInFile = " \t\f():/\\"
)

// javaCausePrefix is what javaCauseRe starts with.
const javaCausePrefix = "Caused by:"

// javaLogArchives start what a logger appends to a frame: the archive the
// class was loaded from (" ~[shop.jar:1.4]").
var javaLogArchives = []string{" [", " ~["}

// javaLocation reads the source location in a frame's parentheses, s being
// the text behind the opening one: a file with its line ("Cart.java:42"), a
// file alone, or one of javaNoSource. The file is whatever the compiler
// recorded: "SourceFile" after obfuscation, "cart.scala.html", a name beyond
// ASCII; a line may be negative. The frame ends with the closing
// parenthesis, or goes on with one of javaLogArchives. The location is what
// tells a frame from prose and from a .NET frame, whose parentheses hold
// parameters. It is read by hand because it is read for every line of
// every trace.
func javaLocation(s string) (location string, ok bool) {
	end := strings.IndexByte(s, ')')
	if end <= 0 || end+1 < len(s) && !hasAnyPrefix(s[end+1:], javaLogArchives) {
		return "", false
	}
	location = s[:end]
	for _, word := range javaNoSource {
		if location == word {
			return location, true
		}
	}
	file, line, hasLine := strings.Cut(location, ":")
	if !hasLine {
		// A file alone has an extension, and is no list of parameters.
		dot := strings.IndexByte(location[1:], '.') + 1
		return location, dot > 0 && dot < len(location)-1 && !strings.ContainsAny(location, javaNotInFile+",")
	}
	line = strings.TrimPrefix(line, "-")
	if line == "" || strings.Trim(line, "0123456789") != "" {
		return "", false
	}
	return location, file == javaUnknownSource || file != "" && !strings.ContainsAny(file, javaNotInFile)
}

// javaFrameName returns the qualified method name of a frame line:
// "pkg.Class.method" before a source location in parentheses, with or
// without "at" in front (OneAgent call stacks and some log formats leave it
// out) and a space between. It is read by hand, like the location.
func javaFrameName(line string) (string, bool) {
	name, _, ok := javaFrameParts(line)
	return name, ok
}

// javaFrameParts is javaFrameName plus the source location.
func javaFrameParts(line string) (name, location string, ok bool) {
	line = strings.TrimPrefix(line, "at ")
	paren := strings.IndexByte(line, '(')
	if paren < 0 {
		return "", "", false
	}
	if location, ok = javaLocation(line[paren+1:]); !ok {
		return "", "", false
	}
	name = strings.TrimSuffix(line[:paren], " ")
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot == len(name)-1 {
		return "", "", false
	}
	for i, r := range name {
		switch {
		case r == '_', r == '$', r == '<', r == '>', unicode.IsLetter(r), unicode.IsDigit(r):
		// The separators of a class path, and the hyphen Kotlin puts into
		// the mangled name of a method ("plus-LRDsOJo").
		case r == '-', i <= dot && (r == '.' || r == '/' || r == '@'):
		default:
			return "", "", false
		}
	}
	return name, location, true
}

// javaOwns: a frame whose location has a line, is one of the JVM's words,
// or is a source file of a JVM language (javaSourceExtensions). Any other
// file alone ("(cart.html)") reads the same as a .NET parameter
// ("(System.String)") and as an argument in a line of source text.
func javaOwns(line string) bool {
	_, location, ok := javaFrameParts(line)
	if !ok {
		return false
	}
	if strings.Contains(location, ":") || !strings.Contains(location, ".") {
		return true
	}
	for _, ext := range javaSourceExtensions {
		if strings.HasSuffix(location, ext) {
			return true
		}
	}
	return false
}

// javaSyntheticClass marks the class the JVM generates for a lambda
// ("Cart$$Lambda$14/0x0800.accept"), and is the module of its frame. Such a
// frame is plumbing between two real ones: it shows that the trace is Java's
// and is in no root cause.
const javaSyntheticClass = "$$Lambda"

// javaSyntheticClasses are what the name of such a class holds: the JVM's
// own, and the one Android's D8 generates
// ("Cart$$ExternalSyntheticLambda0.run").
var javaSyntheticClasses = []string{javaSyntheticClass, "$$ExternalSyntheticLambda"}

// javaSuppressedPrefix starts the line of a suppressed exception.
const javaSuppressedPrefix = "Suppressed:"

// javaFramePrefix starts a frame line as the JVM prints it. Such a line
// that the reader cannot read as a frame, below the first frame of its
// section, is still a frame the JVM printed: it holds a place in the
// section (as a frame of javaSyntheticClass) so that "... N more" counts
// right, and is in no root cause.
const javaFramePrefix = "at "

func javaFrame(line string) (StackFrame, bool) {
	name, ok := javaFrameName(line)
	if !ok {
		return StackFrame{}, false
	}
	if strings.Contains(name, "$$") && containsAny(name, javaSyntheticClasses) {
		return StackFrame{Module: javaSyntheticClass, Function: name[strings.LastIndexByte(name, '.')+1:], Syntax: StackJava}, true
	}
	// Module and class-loader prefixes: "java.base/", "app//".
	name = name[strings.LastIndexByte(name, '/')+1:]
	dot := strings.LastIndexByte(name, '.')
	module, function := name[:dot], name[dot+1:]
	// Few names are generated ones; the expressions run only on a name
	// that holds what each of them needs.
	if strings.Contains(name, "$") {
		function = javaLambdaRe.ReplaceAllString(function, "$1")
		function = javaScalaMethodRe.ReplaceAllString(function, "$1")
		if m := javaScalaClassRe.FindStringSubmatch(module); m != nil {
			module, function = m[1], m[2]
		}
		module = javaProxyRe.ReplaceAllString(module, FrameAuto)
	}
	if strings.Contains(module, "Generated") || strings.Contains(module, "$Proxy") {
		module = javaGeneratedRe.ReplaceAllString(module, FrameAuto)
	}
	if strings.HasPrefix(module, "jdk.proxy") {
		module = javaProxyPkgRe.ReplaceAllString(module, "jdk.proxy.")
	}
	return StackFrame{Module: module, Function: function, Syntax: StackJava}, true
}

// javaSection is one exception of a trace: its type where a "Caused by:"
// line names it, the frames printed under it, and how its whole trace is
// put together from those and the frames it shares with the exception
// around it.
type javaSection struct {
	typ string
	// The section's own frame lines are frames[start:end] of the trace, a
	// lambda's synthetic frame included: the JVM counts those too.
	start, end int
	// more is the N of a "... N more" line, -1 without one.
	more int
	// enclosing is the section the shared frames are taken from, shared how
	// many of its last frames those are, and total the length of this
	// section's whole trace: its own frames and the shared ones.
	enclosing, shared, total int
	// realFromEnd is how far the last frame that is not synthetic lies from
	// the end of the whole trace (1 for the last frame), 0 where there is
	// none.
	realFromEnd int
}

// javaRootCause returns the type and the frames of the last "Caused by:"
// section that has any, completed from the enclosing one where it ends in
// "... N more"; a last cause printed without frames (cut off, or thrown
// without a stack) leaves the one before it as the root, and a cause
// without frames between two others is not what the next one shares its
// frames with.
//
// "... N more" stands for the last N frames of the enclosing exception as
// the JVM printed them, so the frames of a lambda's synthetic class count
// there and are dropped only from the result. The scan keeps lengths, and
// the frames of the one section that is the root are put together at the
// end: a chain is read in time and space proportional to its length.
//
// A "Suppressed:" block is left out with everything in it, including its own
// "Caused by:" sections. The block reaches as far as the lines are indented
// at least as deep as its first: Java indents a suppressed exception and its
// causes one step further than the exception they belong to. Only a line of
// the trace's structure ends it (javaStructureLine): the second line of a
// suppressed exception's message stands at the left margin and ends
// nothing. A trace whose indentation was stripped therefore ends at its
// first suppressed block.
func javaRootCause(lines []string) (root []StackFrame, typ string, atFault bool) {
	var frames []StackFrame
	sections := []javaSection{{more: -1, enclosing: -1}}
	enclosing := -1  // the last section with frames or a "more" line
	suppressed := -1 // indentation of the suppressed block being skipped
	// closeSection settles the lengths of the last section once its lines
	// are read.
	closeSection := func() {
		at := len(sections) - 1
		sec := &sections[at]
		sec.end = len(frames)
		sec.enclosing = enclosing
		if sec.more > 0 && enclosing >= 0 {
			sec.shared = min(sec.more, sections[enclosing].total)
		}
		sec.total = sec.end - sec.start + sec.shared
		if sec.shared > 0 && sections[enclosing].realFromEnd > 0 && sections[enclosing].realFromEnd <= sec.shared {
			sec.realFromEnd = sections[enclosing].realFromEnd
		} else {
			for i := sec.end - 1; i >= sec.start; i-- {
				if frames[i].Module != javaSyntheticClass {
					sec.realFromEnd = sec.end - i + sec.shared
					break
				}
			}
		}
		if sec.end > sec.start || sec.more >= 0 {
			enclosing = at
		}
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if suppressed >= 0 {
			if line == "" || indent >= suppressed || !javaStructureLine(line) {
				continue
			}
			suppressed = -1
		}
		switch {
		case strings.HasPrefix(line, javaSuppressedPrefix):
			suppressed = indent
		case strings.HasPrefix(line, javaCausePrefix):
			cause := javaSection{more: -1, start: len(frames)}
			if m := javaCauseRe.FindStringSubmatch(line); m != nil {
				cause.typ = m[1]
			} else if last := sections[len(sections)-1]; last.more < 0 && last.start == len(frames) {
				// Prose above the frames of its section: a line of the
				// message.
				continue
			}
			closeSection()
			sections = append(sections, cause)
		default:
			if strings.HasPrefix(line, "...") {
				if m := javaMoreRe.FindStringSubmatch(line); m != nil {
					sections[len(sections)-1].more, _ = strconv.Atoi(m[1])
				}
			} else if f, ok := javaFrame(line); ok {
				frames = append(frames, f)
			} else if strings.HasPrefix(line, javaFramePrefix) && len(frames) > sections[len(sections)-1].start {
				frames = append(frames, StackFrame{Module: javaSyntheticClass, Syntax: StackJava})
			}
		}
	}
	closeSection()

	at := len(sections) - 1
	for at >= 0 && sections[at].realFromEnd == 0 {
		at--
	}
	if at < 0 {
		return nil, "", true
	}
	typ = sections[at].typ
	root = make([]StackFrame, 0, sections[at].total)
	// The whole trace of a section is its own frames and then the last
	// "shared" frames of the enclosing one, which are put together the same
	// way: take what is needed of each section's own frames, outwards.
	for need := sections[at].total; need > 0; at = sections[at].enclosing {
		sec := &sections[at]
		if own := need - sec.shared; own > 0 {
			for _, f := range frames[sec.end-own : sec.end] {
				if f.Module != javaSyntheticClass {
					root = append(root, f)
				}
			}
			need = sec.shared
		}
	}
	return root, typ, true
}

// javaStructureLine reports whether a line, already trimmed, is one the JVM
// builds the trace of an exception of: a frame, a count of shared frames,
// or the header of a cause. Anything else is a line of a message.
func javaStructureLine(line string) bool {
	if strings.HasPrefix(line, "...") || strings.HasPrefix(line, javaCausePrefix) {
		return true
	}
	_, ok := javaFrameName(line)
	return ok
}

func javaInApp(f StackFrame) bool {
	return !strings.Contains(f.Module, FrameAuto) && !hasAnyPrefix(f.Module, javaVendorPrefixes)
}
