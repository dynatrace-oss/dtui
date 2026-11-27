package exckey

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// .NET traces: "at Ns.Type.Method(String arg) in File.cs:line 1" frames. An
// exception that wraps another prints the inner one first, closed by an
// "End of inner exception stack trace" line. Mono writes a space before the
// parameters: "at Ns.Type.Method (System.String arg) [0x0001a] in <…>:0".

var (
	// The method name, the space Mono puts after it, the parameters, and
	// what follows them. A hyphen stands in a generated name only
	// (dotnetInterfaceDot).
	dotnetFrameRe = regexp.MustCompile("^at ([\\p{L}\\p{N}_.<>|$`\\[\\],+-]+)( ?)\\(([^()]*)\\)(.*)$")
	// Mono's notation is close to prose ("at checkout (final step)") and to
	// V8's "at fn (location)". So there the name must be qualified, and
	// either the offset Mono prints after the parameters follows
	// (dotnetMonoOffset) or every parameter starts with a type name.
	dotnetMonoParamsRe = regexp.MustCompile("^(?:[A-Z][\\w.`\\[\\]<>&*+]*(?: \\w+)?(?:, |$))*$")
	dotnetTypeArgsRe   = regexp.MustCompile(`\[[^\]]*\]`)
	// An inner exception's type, after the arrow that introduces it. An
	// arrow may stand in a message too ("moved Open ---> Pending"), so the
	// type is qualified and a colon or the end of the line follows it.
	dotnetInnerRe = regexp.MustCompile(
		"---> (?:\\(Inner Exception #\\d+\\) )?((?:[\\p{L}\\p{N}_+`]+\\.)+[\\p{L}\\p{N}_+`]+)(?:\\[[^\\]]*\\])?(?::|$)")
	// Since .NET Core 3.0 an inner exception starts a line of its own
	// (Exception.ToString writes a line break before the arrow). There a
	// type needs no namespace to be told from a word of the message, as
	// long as the colon before its message follows it.
	dotnetInnerLineRe = regexp.MustCompile(
		"^---> (?:\\(Inner Exception #\\d+\\) )?([\\p{L}\\p{N}_+`]+)(?:\\[[^\\]]*\\])?:")
	// The method of top-level statements ("<Main>$"); see dotnetLambdaMark
	// for the generated names that nest.
	dotnetMainRe = regexp.MustCompile(`<([\p{L}\p{N}_]+)>\$`)
	// The closure class a lambda is compiled into ("<>c.",
	// "<>c__DisplayClass5_0`1.", and "<>c__5`1." in a generic method),
	// which stands before the lambda's name.
	dotnetClosureRe = regexp.MustCompile("<>c(?:__DisplayClass\\d+_\\d+|__\\d+)?(?:`\\d+)?\\.")
	// A delegate compiled from an expression tree is numbered per process.
	dotnetDelegateRe = regexp.MustCompile(`^lambda_method\d+$`)
)

// dotnetVendorPrefixes are the namespaces that are never the application's:
// the runtime and the frameworks, database drivers, the cloud SDKs, and the
// platform bindings of Android. Matching is case-sensitive: "System." is
// .NET, "system." is somebody's namespace.
//
// The table is part of the key algorithm: once a release has shipped keys
// it only changes together with ExceptionKeyVersion (see
// javaVendorPrefixes).
var dotnetVendorPrefixes = []string{
	"System.", "Microsoft.", "Newtonsoft.", "Grpc.", "OpenTelemetry.", "Dynatrace.",
	// Further frameworks and libraries.
	"Polly.", "MediatR.", "MassTransit.", "Castle.", "Autofac.", "Serilog.", "NLog.", "AutoMapper.",
	"RabbitMQ.Client.", "Confluent.Kafka.",
	// Database drivers.
	"Npgsql.", "MySql.", "MySqlConnector.", "Oracle.ManagedDataAccess.", "MongoDB.", "StackExchange.Redis.", "Dapper.",
	// Cloud SDKs.
	"Amazon.", "Azure.", "Google.",
	// Android, and what binds it.
	"Android.", "AndroidX.", "Java.", "Xamarin.", "Mono.",
}

// dotnetPassThrough: middleware, by the name ASP.NET finds it by. A plain
// "Invoke" is a delegate's, which is the runtime's, or a method of the
// application's.
var dotnetPassThrough = []string{"InvokeAsync"}

const dotnetInnerEnd = "--- End of inner exception stack trace"

// dotnetSibling starts the line of an AggregateException's second and
// later inner exceptions. They stand beside the first, which is printed
// like any inner exception, and are no cause of it or of the aggregate.
const dotnetSibling = "---> (Inner Exception #"

const (
	// dotnetNested joins a nested type to its outer one in Mono's traces.
	dotnetNested = "+"
	// dotnetGenerated ends the method a compiler-generated name wraps.
	dotnetGenerated = ">"
)

// Compiler-generated names wrap the method they are compiled from in
// brackets, and say behind the closing one what they are: a lambda
// ("<Run>b__5_0"), a local function ("<Run>g__Validate|5_0"), an async
// state machine ("<Run>d__12.MoveNext"). Their ordinals move with every edit of the
// class, so none stays in a name; a generic one carries its arity behind
// them ("d__12`1"). A local function keeps the method it is declared in,
// since two methods may each have one of the same name. The names nest
// ("<<Run>b__0>d"), so the method inside the brackets may be one already
// unwrapped. A method's name is letters and numbers of any script, as the
// frame's is (dotnetFrameRe).
const (
	dotnetLambdaMark = "b__"
	dotnetLocalMark  = "g__"
	// dotnetLocalEnd stands between a local function's name and its
	// ordinals.
	dotnetLocalEnd = '|'
	// dotnetAsyncMark starts what follows an async method, dotnetAsyncEnd
	// ends it, and with it the name.
	dotnetAsyncMark     = "d"
	dotnetAsyncOrdinals = "__"
	dotnetAsyncEnd      = ".MoveNext"
	// dotnetInterfaceDot is what the compiler writes for a dot inside the
	// brackets: the state machine of a method that implements an
	// interface explicitly is "<Shop-ICart-Add>d__12", and the method is
	// "Shop.ICart.Add" wherever it is printed itself.
	dotnetInterfaceDot = '-'
	// dotnetArity precedes the number of type parameters.
	dotnetArity = '`'
	// dotnetUnwrapped marks, while a name is unwrapped, the place of an
	// opening bracket that is gone. No frame's name holds the byte.
	dotnetUnwrapped = 0
)

// dotnetMonoOffset starts what Mono prints after a frame's parameters: the
// IL offset, "[0x0001a] in <module>:0".
const dotnetMonoOffset = " [0x"

// dotnetFrameParts reads a frame line as its method name and parameters.
func dotnetFrameParts(line string) (name, params string, ok bool) {
	if !strings.HasPrefix(line, "at ") {
		return "", "", false
	}
	m := dotnetFrameRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	if m[2] != "" && (!strings.Contains(m[1], ".") ||
		!strings.HasPrefix(m[4], dotnetMonoOffset) && !dotnetMonoParamsRe.MatchString(m[3])) {
		return "", "", false
	}
	return m[1], m[3], true
}

// dotnetOwns: a frame that is not Java's. Every Java frame with "at" in
// front reads as a .NET frame whose parameters are its source location.
func dotnetOwns(line string) bool {
	_, _, ok := dotnetFrameParts(line)
	return ok && !javaOwns(line)
}

func dotnetFrame(line string) (StackFrame, bool) {
	name, _, ok := dotnetFrameParts(line)
	if !ok {
		return StackFrame{}, false
	}
	name = dotnetTypeArgsRe.ReplaceAllString(name, "")
	// Mono joins a nested type to its outer one with "+", .NET with ".".
	name = strings.ReplaceAll(name, dotnetNested, ".")
	// Few names are generated ones, and every one of them holds the
	// bracket that closes the method it wraps.
	if strings.Contains(name, dotnetGenerated) {
		name = dotnetMainRe.ReplaceAllString(name, "$1")
		name = dotnetUnwrap(dotnetClosureRe.ReplaceAllString(name, ""))
	}

	module, function := "", name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		module, function = name[:dot], name[dot+1:]
	}
	// Constructors are "Type..ctor".
	if trimmed, ok := strings.CutSuffix(module, "."); ok {
		module, function = trimmed, "."+function
	}
	if function == "" {
		return StackFrame{}, false
	}
	function = dotnetDelegateRe.ReplaceAllString(function, "lambda_method")
	return StackFrame{Module: module, Function: function, Syntax: StackDotNet}, true
}

// dotnetUnwrap replaces every generated name by the method it wraps, from
// the inside out: "<<AddItem>b__5_0>d__12.MoveNext" is "AddItem", and
// "<Run>g__Validate|5_0" is "Run.Validate". It reads the name once, however
// deep the brackets nest. Brackets that hold no method name, or that
// nothing generated follows, stay as they are written.
func dotnetUnwrap(name string) string {
	// open is a bracket not closed yet: where it stands in out, whether
	// all that followed it so far is a method's name, and how many
	// hyphens were seen before it.
	type open struct {
		at      int
		method  bool
		hyphens int
	}
	out := make([]byte, 0, len(name))
	var opens []open
	var hyphens []int // where in out the hyphens inside open brackets stand
	unwrapped := false
	for i, size := 0, 0; i < len(name); i += size {
		c, top := name[i], len(opens)-1
		size = 1
		switch {
		case c == '<':
			opens = append(opens, open{at: len(out), method: true, hyphens: len(hyphens)})
		case c == '>' && top >= 0:
			o := opens[top]
			opens = opens[:top]
			top--
			if local, n := dotnetGeneratedSuffix(name[i+1:]); n > 0 && o.method && len(out) > o.at+1 {
				out[o.at], unwrapped = dotnetUnwrapped, true
				for _, at := range hyphens[o.hyphens:] {
					out[at] = '.'
				}
				if local != "" {
					out = append(append(out, '.'), local...)
				}
				size += n
				continue
			}
			// The brackets stay, and are part of no method's name.
			if top >= 0 {
				opens[top].method = false
			}
		case c == dotnetInterfaceDot && top >= 0:
			hyphens = append(hyphens, len(out))
		case c >= utf8.RuneSelf:
			var r rune
			if r, size = utf8.DecodeRuneInString(name[i:]); top >= 0 && !isNameRune(r) {
				opens[top].method = false
			}
		case top >= 0 && c != '.' && !isNameRune(rune(c)):
			opens[top].method = false
		}
		out = append(out, name[i:i+size]...)
	}
	if !unwrapped {
		return name
	}
	kept := out[:0]
	for _, c := range out {
		if c != dotnetUnwrapped {
			kept = append(kept, c)
		}
	}
	return string(kept)
}

// dotnetGeneratedSuffix reads what follows the bracket that closes a
// method's name. n is how many bytes of it say that the name is a generated
// one, 0 where it is not; local is the name of a local function.
func dotnetGeneratedSuffix(s string) (local string, n int) {
	switch {
	case strings.HasPrefix(s, dotnetLambdaMark):
		if ordinals := dotnetOrdinals(s[len(dotnetLambdaMark):]); ordinals > 0 {
			return "", len(dotnetLambdaMark) + ordinals
		}
	case strings.HasPrefix(s, dotnetLocalMark):
		rest := s[len(dotnetLocalMark):]
		end := 0
		for end < len(rest) {
			r, size := utf8.DecodeRuneInString(rest[end:])
			if !isNameRune(r) {
				break
			}
			end += size
		}
		if end == 0 || end == len(rest) || rest[end] != dotnetLocalEnd {
			break
		}
		if ordinals := dotnetOrdinals(rest[end+1:]); ordinals > 0 {
			return rest[:end], len(dotnetLocalMark) + end + 1 + ordinals
		}
	case strings.HasPrefix(s, dotnetAsyncMark):
		// The ordinals are optional, and the name ends with the state
		// machine's method.
		n = len(dotnetAsyncMark)
		if rest, ok := strings.CutPrefix(s[n:], dotnetAsyncOrdinals); ok {
			digits := countDigits(rest)
			if digits == 0 {
				break
			}
			n += len(dotnetAsyncOrdinals) + digits
		}
		n += dotnetArityLen(s[n:])
		if s[n:] == dotnetAsyncEnd {
			return "", len(s)
		}
	}
	return "", 0
}

// dotnetOrdinals reads the ordinals of a generated name at the start of s,
// "5", "5_0" or "5_0`1", and returns how long they are; 0 without any.
func dotnetOrdinals(s string) int {
	n := countDigits(s)
	if n == 0 {
		return 0
	}
	if n < len(s) && s[n] == '_' {
		if more := countDigits(s[n+1:]); more > 0 {
			n += 1 + more
		}
	}
	return n + dotnetArityLen(s[n:])
}

// dotnetArityLen is the length of a generic arity ("`1") at the start of s.
func dotnetArityLen(s string) int {
	if len(s) > 1 && s[0] == dotnetArity {
		if n := countDigits(s[1:]); n > 0 {
			return 1 + n
		}
	}
	return 0
}

// isNameRune reports whether r can stand in a name: a letter or a number
// of any script, or "_".
func isNameRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// countDigits is how many ASCII digits s starts with.
func countDigits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// dotnetRootCause returns the frames of the innermost exception, which is
// printed first, and its type: the last one an arrow introduces ("--->")
// above the first frame, qualified anywhere in a line (dotnetInnerRe) or
// without a namespace at the start of one (dotnetInnerLineRe). An inner exception thrown without a stack has no
// frames before the line that closes it, so the frames that follow belong
// to the exception around it. The frames end where a sibling of an
// aggregate's first inner exception starts (dotnetSibling).
func dotnetRootCause(lines []string) (frames []StackFrame, typ string, atFault bool) {
	var inner []string // the types the arrows introduced, outermost first
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, dotnetInnerEnd) {
			if len(frames) > 0 {
				break
			}
			inner = inner[:max(0, len(inner)-1)]
			continue
		}
		if len(frames) > 0 && strings.HasPrefix(line, dotnetSibling) {
			break
		}
		if f, ok := dotnetFrame(line); ok {
			frames = append(frames, f)
		} else if len(frames) == 0 {
			// A type without a namespace is not one dotnetInnerRe reads,
			// so no arrow is read twice.
			if m := dotnetInnerLineRe.FindStringSubmatch(line); m != nil {
				inner = append(inner, m[1])
			}
			for _, m := range dotnetInnerRe.FindAllStringSubmatch(line, -1) {
				inner = append(inner, m[1])
			}
		}
	}
	if len(inner) > 0 {
		typ = inner[len(inner)-1]
	}
	return frames, typ, true
}

func dotnetInApp(f StackFrame) bool {
	return f.Module != "" && !hasAnyPrefix(f.Module, dotnetVendorPrefixes)
}
