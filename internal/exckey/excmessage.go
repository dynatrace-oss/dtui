package exckey

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Exception message parameterising: the variable parts of a message (ids,
// addresses, timestamps, numbers, quoted values, URLs, file paths) are
// replaced with fixed placeholders, so two throws of one bug that differ only
// in their data produce the same text. What tells one bug from another is
// kept: a status or error code after the wording that names it, and the
// words of a route. Pure string work — no tenant, no clock, no locale.

// Placeholders, one per kind of variable text.
const (
	msgQuoted = "<q>"
	msgURL    = "<url>"
	msgUUID   = "<uuid>"
	msgDate   = "<date>"
	msgMAC    = "<mac>"
	msgIP     = "<ip>"
	msgPath   = "<path>"
	msgHex    = "<hex>"
	msgID     = "<id>"
	msgNumber = "<n>"
)

const (
	// msgWinPathWord is one word of a segment of a Windows path.
	msgWinPathWord = `(?:[^\s"'<>:\\()\[\]{},;]|<\w+>|\(\w+\))+`
	// msgWinPathWords is how many words may follow the first in a segment:
	// "Documents and Settings" has two.
	msgWinPathWords = `2`
)

// msgPlaceholders are all of them, for the cut.
var msgPlaceholders = []string{msgQuoted, msgURL, msgUUID, msgMAC, msgDate, msgIP, msgPath, msgHex, msgID, msgNumber}

const (
	// exceptionMessageInput caps how much of a raw message is read at all.
	exceptionMessageInput = 4096
	// ExceptionMessageMax caps a parameterised message, in bytes.
	ExceptionMessageMax = 256
	// msgIPv6Max is the longest IPv6 text form (an IPv4-mapped address).
	msgIPv6Max = 45
	// msgIPv6Colons separate the eight groups of an address written in
	// full; one less where the last two groups are an IPv4 address.
	msgIPv6Colons = 7
	// msgIPv6WordColons is how many colons make an address of a bare
	// candidate without a digit.
	msgIPv6WordColons = 3
	// msgMACSeparators stand between the octets of the shortest MAC
	// address.
	msgMACSeparators = 5
	// msgPlaceholderMax is the longest placeholder; a cut never leaves a
	// shorter, unclosed one behind.
	msgPlaceholderMax = len(msgUUID)
)

var (
	msgURLRe = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s<>"']+`)
	// A UUID, also behind the prefix of an id ("order_<uuid>"), which is
	// kept.
	msgUUIDRe = regexp.MustCompile(
		`(\b|_)[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\b`)
	// A MAC address: six octets, or the eight of an EUI-64, with colons or
	// with hyphens. Eight octets with colons are also an IPv6 address in
	// full, which is not written with two digits a group.
	msgMACRe = regexp.MustCompile(
		`\b[0-9A-Fa-f]{2}(?:(?::[0-9A-Fa-f]{2}){5}(?:(?::[0-9A-Fa-f]{2}){2})?|(?:-[0-9A-Fa-f]{2}){5}(?:(?:-[0-9A-Fa-f]{2}){2})?)\b`)
	msgIPv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	// IPv6 candidates; netip decides which ones are addresses.
	msgIPv6BracketRe = regexp.MustCompile(`\[([0-9A-Fa-f:.]+)\](?::\d{1,5})?`)
	msgIPv6Re        = regexp.MustCompile(`[0-9A-Fa-f:.]+`)
	// An ISO date with an optional time and zone, or a bare clock time.
	msgDateRe = regexp.MustCompile(
		`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b` +
			`|\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	// A path needs two segments ("/users/42"), so a lone "/" or "and/or"
	// stays; the leading group is the character the path follows. A segment
	// may be a placeholder an earlier rule left ("/orders/<uuid>/lines").
	msgUnixPathRe = regexp.MustCompile(`(?:^|[\s=(\[,;:])(?:~|\.{1,2})?(?:/(?:[\w.@%+~-]+|<\w+>)){2,}/?`)
	// A file name ends in an extension that starts with a letter, so the
	// "5" of "/carts/1.5" is none, and has at most ten characters
	// ("application.properties").
	msgFileNameRe = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,9}$`)
	// A Windows path may hold spaces in its directories ("C:\Program
	// Files\…"): a segment with spaces counts only when a backslash ends it,
	// so the path stops at the first space after its last backslash. A
	// segment has at most msgWinPathWords words, so that the prose after a
	// path does not become part of it wherever a backslash follows later
	// ("… is locked by CORP\alice"). Brackets, commas and semicolons end a
	// path; a word in parentheses ("(x86)") is part of one.
	msgWinPathRe = regexp.MustCompile(
		`\b[A-Za-z]:\\(?:` + msgWinPathWord + `(?: ` + msgWinPathWord + `){0,` + msgWinPathWords + `}\\)*(?:` + msgWinPathWord + `)?`)
	msgHexLitRe = regexp.MustCompile(`\b0[xX][0-9A-Fa-f]+\b`)
	// Object identity suffixes ("Cart@1b2c3d") and hex runs from seven
	// characters, the short form of a commit id.
	msgHexAtRe  = regexp.MustCompile(`@[0-9A-Fa-f]+\b`)
	msgHexRunRe = regexp.MustCompile(`\b[0-9A-Fa-f]{7,}\b`)
	// An id behind a lower-case prefix ("user_12345", "cus_9s6XAbCdEf"); the
	// prefix says what it identifies and is kept. An upper-case name with
	// digits ("ERR_HTTP2_STREAM_ERROR", "E_42") is a constant, not an id.
	msgPrefixedIDRe = regexp.MustCompile(`\b((?:[a-z]+_)+)[A-Za-z0-9]*\d[A-Za-z0-9]*\b`)
	// One pass over the numbers, first alternative first:
	//
	//  1. a SQLSTATE, five digits or upper-case letters, and an Oracle-style
	//     code ("ORA-00942"). Kept: they name the failure.
	//  2. a number after the wording of a code: an HTTP status ("HTTP 404",
	//     "HTTP Error 404"), a status, an errno, or "code" behind what says
	//     whose it is (exit, error, response, return, result, SQL). Kept
	//     where it is at most msgCodeDigits digits and nothing else but a
	//     minus sign before them ("error code -32601"), since 404 and 500
	//     are different failures; "code" alone names a value ("promo code
	//     12345"), and "status: 12.5ms" is a duration.
	//
	//     The wording is matched without regard to case, which in a regular
	//     expression also takes "ſ" for s and the kelvin sign for k:
	//     replaceNumbers takes wording beyond ASCII for none.
	//  3. a number (msgNumberText) and, after the start, a space or one of
	//     msgNumberLeads, a minus sign before it ("Index -1").
	msgNumberRe = regexp.MustCompile(
		`(?i:\bSQLSTATE)[ :=#\[(]{1,3}(\d{1,5}|[0-9A-Z]{5})\b` +
			`|\b(?:ORA|PLS|TNS)-\d{5}\b` +
			`|(?i:\b(?:HTTP(?:/\d(?:\.\d)?)?(?: status| error)?|(?:(?:http|exit|response)[ _-]?)?status(?:[ _-]?code)?` +
			`|(?:error|err|exit|response|return|result|sql)[ _-]?code|errno)[ :=#\[(]{1,3})(-?` + msgNumberText + `)\b` +
			`|(?:(?:^|[\s=:(\[,;])-)?\b` + msgNumberText + `\b`)
	// A SQLSTATE in quotes ("SQLSTATE '23505'"), which is the code and no
	// value: the quotes go, so that the rule for quoted values leaves it.
	msgQuotedStateRe = regexp.MustCompile(`((?i:\bSQLSTATE)[ :=#\[(]{1,3})(?:'([0-9A-Z]{5})'|"([0-9A-Z]{5})")`)
	// A CSI sequence (a colour, a cursor move), or any other control
	// character but the white space collapseSpaces handles.
	msgControlRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|[\x00-\x08\x0b\x0e-\x1f\x7f]`)
)

// msgUnits are the units a number may carry without a space: of time, of
// size and of rate, and the "x" of a factor. Letters behind a number are
// part of it only where they are one of these, in any case: "250ms" is a
// number, and "2fa", "3ds", "4xx" and "404Err" are words, which stay. The
// micro sign has two spellings, U+00B5 and the Greek letter U+03BC.
var msgUnits = []string{
	"ns", "us", "\u00b5s", "\u03bcs", "ms", "s", "sec", "secs", "msec", "m", "min", "mins", "h", "hr", "hrs", "d",
	"b", "kb", "mb", "gb", "tb", "kib", "mib", "gib", "tib", "k", "g",
	"bps", "kbps", "mbps", "gbps", "rps", "qps", "hz", "khz", "mhz", "ghz", "x",
}

// msgNumberText is a number with an optional unit ("250ms", "12.5secs",
// "1.5µs"), or a run of them without a space ("1m0s", "2h45m30.5s").
var msgNumberText = `(?:` + msgNumeral + msgUnitText + `)*` + msgNumeral + msgUnitText + `?`

// msgNumeral is the number itself: digits, a fraction, an exponent
// ("1.5e-10").
const msgNumeral = `\d+(?:\.\d+)?(?:[eE][+-]?\d+)?`

// msgUnitText matches one of msgUnits. Each ASCII letter is written in both
// cases: matching without regard to case would also take the kelvin sign
// for k and "ſ" for s.
var msgUnitText = func() string {
	var b strings.Builder
	b.WriteString("(?:")
	for i, unit := range msgUnits {
		if i > 0 {
			b.WriteByte('|')
		}
		for _, r := range unit {
			if r < utf8.RuneSelf {
				b.WriteString("[" + strings.ToLower(string(r)) + strings.ToUpper(string(r)) + "]")
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteString(")")
	return b.String()
}()

const (
	// msgNumberLeads are the characters a minus sign may follow, as
	// msgNumberRe lists them.
	msgNumberLeads = " \t\n\f\r=:([,;"
	// msgCodeDigits is the longest status or error code that is kept.
	msgCodeDigits = 5
	// msgEscape starts a control sequence.
	msgEscape = "\x1b"
)

// messageRule is one replacement step of the parameteriser. applies is a
// cheap test on the bytes the rule cannot do without: most messages hold
// nothing for most rules, and a pass after the one that replaced everything
// holds nothing for any, so the regular expressions are only run where they
// can match.
type messageRule struct {
	name    string
	applies func(string) bool
	apply   func(string) string
}

// messageRules run in this order, and the order is part of the result: a
// quoted SQLSTATE loses its quotes before quoted values go (replaceQuoted),
// which go next because they may hold anything, URLs before the
// paths and numbers inside them, UUIDs before the hex runs and numbers they
// are made of, MAC addresses before the IPs and dates their octets read as
// (eight octets are an IPv6 address, three a clock time), IPs before dates (three groups of an IPv6 address read as a
// clock time) and before numbers, dates before the paths that hold one, and
// paths before the ids in their segments. A digit run is a number however
// long it is, so hex needs a letter. Changing the order changes fingerprints.
var messageRules = []messageRule{
	{"sqlstate", func(s string) bool { return strings.ContainsAny(s, "\"'") && len(s) > msgCodeDigits }, unquoteStates},
	{"quoted", func(s string) bool { return strings.ContainsAny(s, msgQuotes) }, replaceQuoted},
	{"url", func(s string) bool { return strings.Contains(s, "://") },
		func(s string) string { return msgURLRe.ReplaceAllString(s, msgURL) }},
	{"uuid", func(s string) bool { return strings.Count(s, "-") >= 4 },
		func(s string) string { return msgUUIDRe.ReplaceAllString(s, "${1}"+msgUUID) }},
	{"mac", func(s string) bool {
		return strings.Count(s, ":") >= msgMACSeparators || strings.Count(s, "-") >= msgMACSeparators
	},
		func(s string) string { return msgMACRe.ReplaceAllString(s, msgMAC) }},
	// Each expression of the IP rule runs only on text that holds what it
	// needs: most messages with colons or full stops hold no address.
	{"ip", func(s string) bool { return strings.Count(s, ".") >= 3 || strings.Count(s, ":") >= 2 }, func(s string) string {
		colons := strings.Count(s, ":") >= 2
		if colons && strings.Contains(s, "[") {
			s = msgIPv6BracketRe.ReplaceAllStringFunc(s, func(m string) string {
				if isIPv6(msgIPv6BracketRe.FindStringSubmatch(m)[1]) {
					return msgIP
				}
				return m
			})
		}
		// IPv6 before IPv4, so an IPv4-mapped address is taken whole.
		if colons {
			s = replaceBareIPv6(s)
		}
		if hasDigit(s) && strings.Count(s, ".") >= 3 {
			s = msgIPv4Re.ReplaceAllString(s, msgIP)
		}
		return s
	}},
	{"date", func(s string) bool { return hasDigit(s) && strings.ContainsAny(s, "-:") },
		func(s string) string { return msgDateRe.ReplaceAllString(s, msgDate) }},
	{"path", func(s string) bool { return strings.ContainsAny(s, `/\`) }, func(s string) string {
		s = msgUnixPathRe.ReplaceAllStringFunc(s, replaceFilePath)
		return msgWinPathRe.ReplaceAllString(s, msgPath)
	}},
	{"hex", hasDigit, func(s string) string {
		s = msgHexLitRe.ReplaceAllString(s, msgHex)
		s = msgHexAtRe.ReplaceAllStringFunc(s, func(m string) string {
			if hasDigit(m) {
				return "@" + msgHex
			}
			return m
		})
		// A run without a digit is a word ("defaced"), one without a letter
		// is a number.
		return msgHexRunRe.ReplaceAllStringFunc(s, func(m string) string {
			if hasDigit(m) && strings.ContainsAny(m, hexLetters) {
				return msgHex
			}
			return m
		})
	}},
	{"id", func(s string) bool { return hasDigit(s) && strings.Contains(s, "_") },
		func(s string) string { return msgPrefixedIDRe.ReplaceAllString(s, "${1}"+msgID) }},
	{"number", hasDigit, replaceNumbers},
}

// replaceFilePath decides what a path match becomes. A path that names a
// file — it starts at the home or the working directory, or its last segment
// has an extension — is user data and becomes <path>. Any other path is kept
// as a route ("GET /cart/items" and "GET /checkout/pay" are different
// failures), and the rules that follow replace the ids in its segments, so
// "/cart/42/items" reads "/cart/<n>/items". This reads a route that ends in
// an extension as a file, and keeps a file that has none.
func replaceFilePath(m string) string {
	lead := ""
	if c := m[0]; c != '/' && c != '~' && c != '.' {
		lead, m = m[:1], m[1:]
	}
	name := strings.TrimRight(m, "./")
	if m[0] == '/' && !msgFileNameRe.MatchString(name) {
		return lead + m
	}
	return lead + msgPath
}

const (
	// msgQuotes are the characters that open and close a quoted value, each
	// its own kind.
	msgQuotes = "\"'`"
	// msgApostrophe is the one of them that also stands inside words.
	msgApostrophe = '\''
	// msgEscapingQuotes are the kinds in which a backslash takes the
	// character after it along (\").
	msgEscapingQuotes = "\"'"
	// msgClosers are what, besides a letter, a digit or an underscore, an
	// apostrophe follows ("(ann)'s"): no value starts behind them.
	msgClosers = ")]}"
)

// replaceQuoted replaces every quoted value, in one scan from the left: the
// quote that comes first opens a value, which runs to the next quote of its
// kind, whatever its length and whatever quotes of another kind it holds
// ("unexpected '\"' near …"). Inside it a doubled quote is part of the value
// ('O”Brien'), and so is a backslash with the character after it, except
// in back quotes. A quote without a closing one is text, and so is every
// later quote of its kind.
//
// One scan settles values inside one another, which took a pass of all the
// rules for each level while each kind had an expression of its own.
//
// An apostrophe also stands inside words. It opens a value only where no
// letter, digit or closing bracket stands before it ("can't", "users'",
// "(ann)'s"), and closes one only where no letter or digit follows ("'don't
// do it'"), in any script.
func replaceQuoted(s string) string {
	var b strings.Builder
	var unclosed [len(msgQuotes)]bool
	last := 0
	for i := 0; i < len(s); i++ {
		kind := strings.IndexByte(msgQuotes, s[i])
		if kind < 0 || unclosed[kind] || s[i] == msgApostrophe && !opensQuoted(s[:i]) {
			continue
		}
		end := quotedEnd(s, i)
		if end < 0 {
			unclosed[kind] = true
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(msgQuoted)
		i, last = end, end+1
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// opensQuoted reports whether an apostrophe behind before can open a value.
func opensQuoted(before string) bool {
	r, _ := utf8.DecodeLastRuneInString(before)
	return before == "" || !isNameRune(r) && !strings.ContainsRune(msgClosers, r)
}

// quotedEnd is where the value that opens at s[open] closes, or -1. A value
// that does not close behind a doubled quote ends with it: the quotes
// around a value were doubled, not one inside it ("column ”sku” is
// unknown").
func quotedEnd(s string, open int) int {
	quote := s[open]
	escapes := strings.IndexByte(msgEscapingQuotes, quote) >= 0
	doubled := -1
	for i := open + 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && escapes:
			i++
		case c != quote:
		case i+1 < len(s) && s[i+1] == quote:
			i++
			doubled = i
		case quote == msgApostrophe && i+1 < len(s) && startsWithNameRune(s[i+1:]):
		default:
			return i
		}
	}
	return doubled
}

func startsWithNameRune(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return isNameRune(r)
}

// startsWordFraction reports whether rest, the text behind a number, is a
// fraction that a word follows directly (".5foo"). Such a number was read
// without its fraction, because what follows that is no unit: it is part of
// a word that starts with a digit, like "2fa", and stays. (A fraction that
// anything else follows is the next number, as in "1.2.3"; telling the two
// apart here spares such text a pass of all the rules.)
func startsWordFraction(rest string) bool {
	if !strings.HasPrefix(rest, ".") {
		return false
	}
	n := countDigits(rest[1:])
	return n > 0 && 1+n < len(rest) && startsWithNameRune(rest[1+n:])
}

// unquoteStates drops the quotes around a SQLSTATE behind its wording, where
// that is ASCII (see msgNumberRe).
func unquoteStates(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range msgQuotedStateRe.FindAllStringSubmatchIndex(s, -1) {
		if !isASCII(s[m[2]:m[3]]) {
			continue
		}
		state := max(m[4], m[6])
		b.WriteString(s[last : state-1])
		b.WriteString(s[state : state+msgCodeDigits])
		last = m[1]
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// replaceNumbers decides what each msgNumberRe match becomes: a number is
// replaced, and the character a minus sign followed is kept; a number after
// the wording of a code is replaced behind the wording unless it is a code
// and the wording is ASCII; any other match is a code and stays as it is.
func replaceNumbers(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range msgNumberRe.FindAllStringSubmatchIndex(s, -1) {
		start, end, state, code := m[0], m[1], m[2], m[4]
		keep := start
		switch c := s[start]; {
		case state >= 0:
			if isASCII(s[start:state]) || !hasDigit(s[state:end]) {
				continue
			}
			keep = state
		case code >= 0:
			value := strings.TrimPrefix(s[code:end], "-")
			if isASCII(s[start:code]) && len(value) <= msgCodeDigits && strings.Trim(value, digits) == "" {
				continue
			}
			keep = code
		case c == '-' || strings.IndexByte(digits, c) >= 0:
		case strings.IndexByte(msgNumberLeads, c) >= 0:
			keep = start + 1
		default:
			continue
		}
		if startsWordFraction(s[end:]) {
			continue
		}
		b.WriteString(s[last:keep])
		b.WriteString(msgNumber)
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// ParameterizeMessage replaces the variable parts of an exception message
// with placeholders (<q>, <url>, <uuid>, <mac>, <ip>, <date>, <path>, <hex>, <id>,
// <n>), collapses whitespace and bounds the result to ExceptionMessageMax
// bytes. It is idempotent: parameterising its own output changes nothing.
func ParameterizeMessage(msg string) string {
	s, _ := parameterize(msg)
	return s
}

// parameterize is ParameterizeMessage, and says how many passes it took.
func parameterize(msg string) (string, int) {
	// The cap is on the bytes that arrived, so nothing past it is looked
	// at; a rune it goes through is invalid and dropped with the rest.
	s := strings.ToValidUTF8(msg[:min(len(msg), exceptionMessageInput)], "")
	if strings.ContainsAny(s, msgControls) {
		s = msgControlRe.ReplaceAllStringFunc(s, func(m string) string {
			if strings.HasPrefix(m, msgEscape) && len(m) > len(msgEscape) {
				return "" // a control sequence is not text
			}
			return " "
		})
	}
	// A replacement or the cut can expose a new match (a quote pair around a
	// placeholder, a number at the new end), so run to a fixed point. Every
	// changing pass removes literal text, which bounds the passes; the cap
	// only guards that argument. The pass that confirms the fixed point
	// finds little left for the rules to apply to.
	passes := 0
	for passes < exceptionMessageInput {
		next := parameterizeOnce(s)
		passes++
		if next == s {
			break
		}
		s = next
	}
	return s, passes
}

// parameterizeOnce is one pass: whitespace, every rule in order, the cut.
func parameterizeOnce(s string) string {
	s = collapseSpaces(s)
	for _, rule := range messageRules {
		if rule.applies(s) {
			s = rule.apply(s)
		}
	}
	return strings.TrimRight(cutMessage(s), " ")
}

// collapseSpaces trims s and turns every run of whitespace into one space.
// Text already in that form, which the second pass always is, is returned as
// it is.
func collapseSpaces(s string) string {
	last := byte(' ')
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Anything that may be whitespace other than a single space inside
		// the text; a byte beyond ASCII may start one (U+00A0).
		if c >= utf8.RuneSelf || c < ' ' || c == ' ' && last == ' ' {
			return strings.Join(strings.Fields(s), " ")
		}
		last = c
	}
	return strings.TrimRight(s, " ")
}

// cutMessage bounds s to ExceptionMessageMax bytes without leaving half a
// placeholder at the end: a "<" near the cut goes with what follows it
// where a placeholder starts that way, and is text otherwise ("x < y").
func cutMessage(s string) string {
	if len(s) <= ExceptionMessageMax {
		return s
	}
	s = cutBytes(s, ExceptionMessageMax)
	tail := s[max(0, len(s)-msgPlaceholderMax+1):]
	if open := strings.LastIndexByte(tail, '<'); open >= 0 && hasPrefixIn(msgPlaceholders, tail[open:]) {
		s = s[:len(s)-len(tail)+open]
	}
	return s
}

// hasPrefixIn reports whether prefix starts one of the words without being
// the whole of it.
func hasPrefixIn(words []string, prefix string) bool {
	for _, word := range words {
		if len(prefix) < len(word) && strings.HasPrefix(word, prefix) {
			return true
		}
	}
	return false
}

// cutBytes truncates s to at most limit bytes on a rune boundary.
func cutBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// replaceBareIPv6 replaces unbracketed IPv6 addresses. A candidate must not
// touch a word character on either side ("Foo::bar" is not an address), and
// trailing sentence punctuation is kept.
func replaceBareIPv6(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range msgIPv6Re.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		for end > start && (s[end-1] == '.' || s[end-1] == ':') && !isIPv6(s[start:end]) {
			end--
		}
		if !isIPv6(s[start:end]) || (start > 0 && isWordByte(s[start-1])) ||
			(end < len(s) && isWordByte(s[end])) {
			continue
		}
		// Hex letters around a scope operator are a name ("db::add").
		if cand := s[start:end]; !hasDigit(cand) && strings.Count(cand, ":") < msgIPv6WordColons {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(msgIP)
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// isIPv6 reports whether s is an IPv6 address literal. A bare "::" is the
// unspecified address but reads as an operator in a message, so it is not
// one here.
func isIPv6(s string) bool {
	if len(s) > msgIPv6Max || strings.Trim(s, ":.") == "" {
		return false
	}
	// An address has a "::" or all of its groups; the test spares parsing
	// the many candidates that are neither ("10:11:12").
	if colons := strings.Count(s, ":"); !strings.Contains(s, "::") &&
		colons != msgIPv6Colons && (colons != msgIPv6Colons-1 || !strings.Contains(s, ".")) {
		return false
	}
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Is6()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func hasDigit(s string) bool {
	return strings.ContainsAny(s, digits)
}

const (
	hexLetters = "abcdefABCDEF"
	digits     = "0123456789"
	// msgControls are the control characters msgControlRe matches.
	msgControls = "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f"
)

// wordBytes are the bytes regexp's \w matches.
const wordBytes = "_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func isWordByte(c byte) bool {
	return strings.IndexByte(wordBytes, c) >= 0
}
