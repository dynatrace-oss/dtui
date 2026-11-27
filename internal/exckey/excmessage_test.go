package exckey

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParameterizeMessageReplacesEachKind(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain text is kept", "Cart is locked", "Cart is locked"},
		{"whitespace collapses", "  Cart \t is\n locked  ", "Cart is locked"},

		{"double quoted", `No product named "blue kettle" found`, "No product named <q> found"},
		{"single quoted", "column 'sku' is unknown", "column <q> is unknown"},
		{"back quoted", "table `orders` is missing", "table <q> is missing"},
		{"empty quotes", `value "" rejected`, "value <q> rejected"},
		{"apostrophe is not a quote", "can't reach the user's cart", "can't reach the user's cart"},
		{"two single quoted values", "'a' and 'b'", "<q> and <q>"},
		{"unbalanced quote stays", `expected " here`, `expected " here`},

		{"a double quote inside single quotes", `unexpected char '"' near "foo"`, "unexpected char <q> near <q>"},
		{"the same with another value", `unexpected char '"' near "bar"`, "unexpected char <q> near <q>"},
		{"a double quote inside back quotes", "unexpected char `\"` near \"foo\"", "unexpected char <q> near <q>"},
		{"a single quote inside double quotes", `unexpected "'" near 'foo'`, "unexpected <q> near <q>"},
		{"a back quote inside double quotes", "unexpected \"`\" near `foo`", "unexpected <q> near <q>"},
		{"quotes of one kind inside another", `got 'a "b" c' and "d 'e' f"`, "got <q> and <q>"},
		{"an apostrophe after a letter beyond ASCII", "caf\u00e9's order and users' limit", "caf\u00e9's order and users' limit"},
		{"the same in ASCII", "cafe's order and users' limit", "cafe's order and users' limit"},
		{"an apostrophe after a closing bracket", "user (ann)'s cart exceeded users' limit", "user (ann)'s cart exceeded users' limit"},
		{"an apostrophe after a digit beyond ASCII", "\u0663's and users' limit", "\u0663's and users' limit"},
		{"a quoted value before a letter beyond ASCII is none", "'a'\u00e9 'b' c", "<q> c"},
		{"a quoted value after an opening bracket", "name ('ann') taken", "name (<q>) taken"},
		{"a doubled single quote", "WHERE name = 'O''Brien'", "WHERE name = <q>"},
		{"no doubled quote", "WHERE name = 'Smith'", "WHERE name = <q>"},
		{"a doubled double quote", `column "a ""b"" c" bad`, "column <q> bad"},
		{"a doubled back quote", "table `a``b` bad", "table <q> bad"},
		{"doubled quotes around a value", "column ''sku'' is unknown", "column <q> is unknown"},
		{"doubled quotes around another value", "column ''name'' is unknown", "column <q> is unknown"},
		{"a doubled quote ends an unclosed value", `got "a"" b`, "got <q> b"},
		{"two empty values", "'' and ''", "<q> and <q>"},
		{"an apostrophe inside a quoted value", "said 'don't do it' twice", "said <q> twice"},
		{"an escaped quote before a space", `name 'a\' b' taken`, "name <q> taken"},
		{"an apostrophe after an underscore", "x_'s and users' limit", "x_'s and users' limit"},
		{"an unclosed single quote is text", "expected ' here", "expected ' here"},
		{"an unclosed quote before a closed one of another kind", `got " and 'a'`, `got " and <q>`},
		{"an unclosed quote after a closed one", `got "a" and "b`, `got <q> and "b`},
		{"a backslash ends an unclosed value", `got "a\`, `got "a\`},
		{"a backslash in back quotes is text", "got `a\\` b`", "got <q> b`"},
		{"url", "GET https://shop.example.invalid/cart/42?x=1 failed", "GET <url> failed"},
		{"url with another scheme", "dial jdbc:postgresql://db.example.invalid:5432/shop", "dial jdbc:<url>"},

		{"uuid", "order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f missing", "order <uuid> missing"},
		{"uuid upper case", "order 3F2B8C1E-9D4A-4B7C-8E21-0A1B2C3D4E5F missing", "order <uuid> missing"},

		{"date", "expired on 2026-03-01", "expired on <date>"},
		{"timestamp utc", "at 2026-03-01T10:11:12Z", "at <date>"},
		{"timestamp fraction and offset", "at 2026-03-01T10:11:12.345+02:00 it broke", "at <date> it broke"},
		{"timestamp with a space", "at 2026-03-01 10:11:12,345 it broke", "at <date> it broke"},
		{"clock time", "started 10:11:12.345", "started <date>"},

		{"ipv4", "connect to 10.0.0.7 refused", "connect to <ip> refused"},
		{"ipv4 with port", "connect to 10.0.0.7:8080 refused", "connect to <ip> refused"},
		{"ipv6", "connect to fd00::1f refused", "connect to <ip> refused"},
		{"ipv6 loopback ends a sentence", "connect to ::1.", "connect to <ip>."},
		{"ipv6 with port", "connect to [fd00::1f]:8443 refused", "connect to <ip> refused"},
		{"ipv4-mapped ipv6", "peer ::ffff:10.0.0.7 gone", "peer <ip> gone"},
		{"longest ipv6 form", "peer 0000:0000:0000:0000:0000:ffff:192.168.100.200 gone", "peer <ip> gone"},
		{"two colons are not enough", "a::g", "a::g"},
		{"one colon is not an address", "f:1", "f:<n>"},
		{"ipv6 with groups that read as a time", "peer 2001:db8:10:20:30::1 gone", "peer <ip> gone"},
		{"ipv6 ends the message", "peer fd00::1f", "peer <ip>"},
		{"ipv6 starts the message", "::1 refused", "<ip> refused"},
		{"ipv6 before a colon", "peer fd00::1f: gone", "peer <ip>: gone"},
		{"a bare scope operator stays", "a :: b", "a :: b"},
		{"a leading dot stays", ". then", ". then"},
		{"underscore before an address", "_fd00::1f", "_fd00::1f"},
		{"underscore after an address", "fd00::1f_x", "fd00::1f_x"},
		{"brackets without an address", "index [12] out of range", "index [<n>] out of range"},
		{"scope operator is not an address", "Cart::add failed", "Cart::add failed"},
		{"address glued to a word is not one", "xfd00::1f", "xfd00::1f"},

		{"unix path", "open /var/data/cart/7.json: no such file", "open <path>: no such file"},
		{"home path", "open ~/data/cart.json", "open <path>"},
		{"relative path", "open ./data/cart.json", "open <path>"},
		{"path after an equals sign", "file=/var/data/cart.json", "file=<path>"},
		{"one segment is not a path", "POST /cart failed", "POST /cart failed"},
		{"a route keeps its words", "GET /cart/items failed", "GET /cart/items failed"},
		{"a route loses its ids", "GET /cart/42/items/4bf92f3577b34da6 failed", "GET /cart/<n>/items/<hex> failed"},
		{"a route with a uuid", "no handler for /orders/3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f/lines",
			"no handler for /orders/<uuid>/lines"},
		{"a file under a dated directory", "open /logs/2026-03-01/shop.log", "open <path>"},
		{"a file ends a sentence", "cannot read /etc/shop/cart.yaml.", "cannot read <path>"},
		{"a long extension", "open /opt/tenant-a/application.properties: denied", "open <path>: denied"},
		{"an extension of ten letters", "open /opt/a/cart.abcdefghij", "open <path>"},
		{"eleven letters are no extension", "GET /api/cart.abcdefghijk", "GET /api/cart.abcdefghijk"},
		// Accepted: a route whose last segment ends like a file name reads
		// as a file.
		{"a route that ends like a file", "GET /api/v1.internal failed", "GET <path> failed"},
		{"a file behind a closing slash", "open /etc/shop/cart.yaml/ now", "open <path> now"},
		{"a code of five digits", "HTTP 12345 seen", "HTTP 12345 seen"},
		{"six digits are no code", "HTTP 123456 seen", "HTTP <n> seen"},
		{"ipv6 in full", "peer 2001:0db8:0000:0000:0000:ff00:0042:8329 gone", "peer <ip> gone"},
		{"seven groups are no address", "peer 2001:0db8:0000:0000:0000:ff00:0042 gone", "peer <n>:0db8:<n>:<n>:<n>:ff00:<n> gone"},
		{"a directory is kept", "cannot list /var/data/cart", "cannot list /var/data/cart"},
		{"a slash between words is not a path", "read/write error", "read/write error"},
		{"windows path", `open C:\data\cart\7.json failed`, "open <path> failed"},
		{"windows path with a space", `open C:\Program Files\Shop\cart.json failed`, "open <path> failed"},
		{"two windows paths", `copy C:\data\a.json to D:\backup files\a.json`, "copy <path> to <path>"},

		{"mac address", "mac 00:1A:2B:FF:4D:5E down", "mac <mac> down"},
		{"mac address of letters", "mac AA:BB:CC:DD:EE:FF down", "mac <mac> down"},
		{"mac address of digits", "mac 00:12:34:56:78:90 down", "mac <mac> down"},
		{"mac address that starts like a time", "mac 00:12:34:56:78:9A down", "mac <mac> down"},
		{"mac address with hyphens", "mac 00-1a-2b-ff-4d-5e down", "mac <mac> down"},
		{"mac address of eight octets", "eui 00:12:34:56:78:9A:BC:DE down", "eui <mac> down"},
		{"mac address of eight octets with hyphens", "eui 00-12-34-56-78-9a-bc-de down", "eui <mac> down"},
		{"mac address ends a sentence", "no route to 00:1A:2B:FF:4D:5E.", "no route to <mac>."},
		{"five octets are no mac address", "id 00:1A:2B:FF:4D down", "id <n>:1A:<n>:FF:<n> down"},
		{"mixed separators are no mac address", "id AA:BB-CC:DD-EE:FF down", "id AA:BB-CC:DD-EE:FF down"},
		{"three letters are no octet", "id AA:BB:CC:DD:EE:FFF down", "id AA:BB:CC:DD:EE:FFF down"},
		{"a short commit id", "commit 3f2b8c1 failed", "commit <hex> failed"},
		{"a short commit id that starts with a letter", "commit f2b8c13 failed", "commit <hex> failed"},
		{"six hex characters are a word", "commit f2b8c1 failed", "commit f2b8c1 failed"},
		{"seven hex letters are a word", "the defaced page", "the defaced page"},
		{"hex literal", "bad pointer 0x1F", "bad pointer <hex>"},
		{"object identity", "Cart@1b2c3d is closed", "Cart@<hex> is closed"},
		{"object identity of digits", "Cart@123456 is closed", "Cart@<hex> is closed"},
		{"mail address is not an identity", "bob@added.example.invalid", "bob@added.example.invalid"},
		{"hex run", "trace 4bf92f3577b34da6a3ce929d0e0e4736 lost", "trace <hex> lost"},
		{"a digit run is a number, however long", "id 1234567890 lost", "id <n> lost"},
		{"seven and eight digits read alike", "order 1234567 or 12345678", "order <n> or <n>"},
		{"hex word without a digit stays", "deadbeef", "deadbeef"},
		{"a short hex run is a word", "id 12ab lost", "id 12ab lost"},

		{"number", "retry 3 of 5", "retry <n> of <n>"},
		{"decimal", "took 1.5 seconds", "took <n> seconds"},
		{"number with unit", "timeout after 250ms", "timeout after <n>"},
		{"number with a long unit", "waited 5000msec then 12.5secs", "waited <n> then <n>"},
		{"a negative number", "Index -1 out of bounds for length 0", "Index <n> out of bounds for length <n>"},
		{"a negative value after a sign", "offset=-5 (-2, -3)", "offset=<n> (<n>, <n>)"},
		{"a negative number starts the message", "-1 is not an index", "<n> is not an index"},
		{"a hyphen between numbers is no sign", "pages 2-10", "pages <n>-<n>"},
		{"a hyphen after a word is no sign", "kettle-1 missing", "kettle-<n> missing"},

		{"prefixed id", "user_12345 not found", "user_<id> not found"},
		{"prefixed opaque id", "customer cus_9s6XAbCdEf is blocked", "customer cus_<id> is blocked"},
		{"two-part id prefix", "key api_key_7f3a rejected", "key api_key_<id> rejected"},
		{"a snake case name is no id", "max_retries exceeded", "max_retries exceeded"},
		{"an upper case constant is no id", "ERR_HTTP2_STREAM_ERROR and E_42", "ERR_HTTP2_STREAM_ERROR and E_42"},

		{"http status is kept", "HTTP 404 sendError called", "HTTP 404 sendError called"},
		{"http status with a version", "got HTTP/1.1 503 from upstream", "got HTTP/1.1 503 from upstream"},
		{"status code wording", "status code: 500, retry 3", "status code: 500, retry <n>"},
		{"status with an equals sign", "upstream status=502 after 30ms", "upstream status=502 after <n>"},
		{"camel case status code", "statusCode=404", "statusCode=404"},
		{"exit code", "process ended with exit code 137", "process ended with exit code 137"},
		{"error code", "error code 1045 for user_77", "error code 1045 for user_<id>"},
		{"errno", "connect failed: errno 111", "connect failed: errno 111"},
		{"sqlstate of digits", "SQLSTATE 23505: duplicate key", "SQLSTATE 23505: duplicate key"},
		{"sqlstate in brackets", "SQLSTATE[08006] connection failure", "SQLSTATE[08006] connection failure"},
		{"sqlstate with a letter", "SQLSTATE 2200G", "SQLSTATE 2200G"},
		{"vendor error code", "ORA-00942: table or view does not exist", "ORA-00942: table or view does not exist"},
		{"a long number after code wording is an id", "code 1234567", "code <n>"},
		{"a number after code alone is a value", "x-code 5 and x -code -5", "x-code <n> and x -code <n>"},
		{"a promo code is a value", "Invalid promo code 12345", "Invalid promo code <n>"},
		{"a zip code is a value", "zip code 90210 not served", "zip code <n> not served"},
		{"a verification code is a value", "verification code 4821 expired", "verification code <n> expired"},
		{"a duration after status wording", "status: 200ms elapsed", "status: <n> elapsed"},
		{"a decimal after status wording", "status: 12.5ms", "status: <n>"},
		{"a status ends a sentence", "upstream answered HTTP 404.", "upstream answered HTTP 404."},
		{"sqlstate in lower case letters is no code", "sqlstate error on row 7", "sqlstate error on row <n>"},
		{"a long s is no s", "a\u017ftatus 404", "a\u017ftatus <n>"},
		{"a long s inside the wording", "HTTP \u017ftatus 404", "HTTP \u017ftatus <n>"},
		{"http error wording", "HTTP Error 404: upstream failure", "HTTP Error 404: upstream failure"},
		{"http error wording, another status", "HTTP Error 500: upstream failure", "HTTP Error 500: upstream failure"},
		{"http error wording in lower case", "http error 503", "http error 503"},
		{"http error with a duration", "HTTP error 12.5ms", "HTTP error <n>"},
		{"a negative exit code", "exit code -1", "exit code -1"},
		{"a negative errno", "connect failed: errno -111", "connect failed: errno -111"},
		{"a negative error code", "error code -32601", "error code -32601"},
		{"another negative error code", "error code -32600", "error code -32600"},
		{"a negative code of six digits is a value", "error code -326011", "error code <n>"},
		{"a negative duration after status wording", "status: -12.5ms", "status: <n>"},
		{"a hyphen after code wording is no sign", "status-404", "status-<n>"},
		{"a long s before a sqlstate", "a\u017fQLSTATE 23505", "a\u017fQLSTATE <n>"},
		{"a long s before a sqlstate of letters", "a\u017fQLSTATE ABCDE", "a\u017fQLSTATE ABCDE"},
		{"a long s before a quoted sqlstate", "a\u017fQLSTATE '23505'", "a\u017fQLSTATE <q>"},
		{"a quoted sqlstate", "SQLSTATE '23505' dup", "SQLSTATE 23505 dup"},
		{"another quoted sqlstate", `SQLSTATE "40001" dup`, "SQLSTATE 40001 dup"},
		{"a quoted sqlstate with a letter", "sqlstate: '2200G'", "sqlstate: 2200G"},
		{"a quoted word after sqlstate is a value", "SQLSTATE 'unknown' dup", "SQLSTATE <q> dup"},
		{"a quoted code elsewhere is a value", "state '23505' dup", "state <q> dup"},
		{"a kelvin sign is no k", "to\u212aen 42", "to\u212aen <n>"},

		{"a compound duration", "timeout after 1m0s", "timeout after <n>"},
		{"a longer compound duration", "ran 2h45m30.5s then 1.5µs", "ran <n> then <n>"},
		{"digits and letters in turns are a word", "part 12gh34kl missing", "part 12gh34kl missing"},
		{"a word that starts with a digit", "2fa failed and 3ds failed", "2fa failed and 3ds failed"},
		{"a class of statuses", "upstream returned 4xx, then 5xx", "upstream returned 4xx, then 5xx"},
		{"a status glued to a word", "404Err and 500Error and 404NotFound", "404Err and 500Error and 404NotFound"},
		{"both micro signs", "took 100\u00b5s, then 100\u03bcs", "took <n>, then <n>"},
		{"units of time", "1ns 2us 3ms 4s 5sec 6secs 7msec 8m 9min 10mins 11h 12hr 13hrs 14d", strings.TrimSuffix(strings.Repeat("<n> ", 14), " ")},
		{"units of size", "1b 2B 3kb 4KB 5MB 6gb 7TB 8KiB 9MiB 10GiB 11k 12K 13M 14g", strings.TrimSuffix(strings.Repeat("<n> ", 14), " ")},
		{"units of rate", "1bps 2kbps 3Mbps 4rps 5qps 6Hz 7kHz 8MHz 9GHz", strings.TrimSuffix(strings.Repeat("<n> ", 9), " ")},
		{"a unit and more letters are a word", "5msx and 5sx and 3dx", "5msx and 5sx and 3dx"},
		{"a compound with a word in it", "1m0sx", "1m0sx"},
		{"scientific notation", "value 1.5e-10 out of range", "value <n> out of range"},
		{"scientific notation, another value", "value 1.7e-10 out of range", "value <n> out of range"},
		{"an exponent with a sign or without", "2.5E+03 and 1e5 and 3e8ms", "<n> and <n> and <n>"},
		{"an exponent needs its digits", "5e and 5e+", "5e and 5e+"},
		{"a factor", "2.5x slower than 3x", "<n> slower than <n>"},
		{"a decimal glued to a word is a word", "1.5foo and 1.7foo", "1.5foo and 1.7foo"},
		{"the same behind the wording of a code", "error code 123456.5foo", "error code 123456.5foo"},
		{"a version in a sentence", "v 1.2.3 is out", "v <n>.<n> is out"},
		{"a kelvin sign is no unit", "5\u212a", "<n>\u212a"},

		{"a long quoted value", `value "` + strings.Repeat("x", 300) + `" rejected`, "value <q> rejected"},
		{"escaped quotes inside a quoted value", `value "a \"red\" b" bad`, "value <q> bad"},
		{"an escaped quote inside single quotes", `name 'O\'Brien' taken`, "name <q> taken"},

		{"colour codes are no text", "\x1b[31mCart 7 is locked\x1b[0m", "Cart <n> is locked"},
		{"a colour code inside a word", "ERR\x1b[1mOR\x1b[0m: cart 7", "ERROR: cart <n>"},
		{"control characters separate words", "cart\x007\x1fis locked\x7f", "cart <n> is locked"},

		{"prose after a windows path", `C:\app\a.json is locked by CORP\alice`, `<path> is locked by CORP\alice`},
		{"a windows path in parentheses", `open (C:\data\cart.json) failed`, "open (<path>) failed"},
		{"a windows path in a list", `read [C:\data\a.json, C:\data\b.json]; gave up`, "read [<path>, <path>]; gave up"},
		{"a windows directory with parentheses", `open C:\Program Files (x86)\Shop\cart.json failed`, "open <path> failed"},
		{"a windows directory of three words", `open C:\Documents and Settings\cart.json failed`, "open <path> failed"},

		{"a scoped name of hex letters", "db::add failed", "db::add failed"},
		{"a scoped name of hex letters, upper case", "A::B failed", "A::B failed"},
		{"three colons make an address without a digit", "peer fe::ab:cd gone", "peer <ip> gone"},

		{"a uuid behind an id prefix", "order_123e4567-e89b-12d3-a456-426614174000 gone", "order_<uuid> gone"},
		{"a number elsewhere is still a number", "404 items in cart 500", "<n> items in cart <n>"},
		{"digits inside a word stay", "utf8 and http2 and x86_64", "utf8 and http2 and x86_64"},
		{"version", "v 1.2.3", "v <n>.<n>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParameterizeMessage(tc.in); got != tc.want {
				t.Errorf("ParameterizeMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Quoted values inside one another are replaced by the pass that meets the
// outermost, so nesting costs no passes of all the rules.
func TestParameterizeMessageSettlesNestedQuotesAtOnce(t *testing.T) {
	onion := "HTTP 404 ---- _ / :// " + strings.Repeat("'a:", 38) + "''" + strings.Repeat("a':", 38)
	long := strings.Repeat("'a:", 41) + `"` + strings.Repeat("x", 3840) + `"` + strings.Repeat("a':", 41)
	kinds := strings.Repeat("\"'`", 40) + "x" + strings.Repeat("`'\"", 40)
	same := strings.Repeat(`"`, 252)
	for name, msg := range map[string]string{"the onion of 252 bytes": onion, "a long one": long, "three kinds in turns": kinds, "one kind": same} {
		got, passes := parameterize(msg)
		if passes > 3 {
			t.Errorf("%s: %d passes, want at most three", name, passes)
		}
		if again := ParameterizeMessage(got); again != got || got != ParameterizeMessage(msg) {
			t.Errorf("%s: %q is not settled: %q", name, got, again)
		}
	}
	if len(onion) != 252 {
		t.Errorf("the onion is %d bytes", len(onion))
	}
	if got := ParameterizeMessage("\"a 'b `c` d' e\""); got != "<q>" {
		t.Errorf("three kinds inside one another: %q", got)
	}
	// A quote that cannot open a value is passed by once however many
	// follow: the scan does not start over at each.
	unclosed := `"` + strings.Repeat(`\"`, exceptionMessageInput/2-1)
	if got := ParameterizeMessage(unclosed); got != unclosed[:ExceptionMessageMax] {
		t.Errorf("unclosed quotes: %q", got)
	}
	// Measured against the same text with a letter for each quote, which
	// no rule has work with either.
	plain := strings.ReplaceAll(unclosed, `"`, "x")
	cost := func(msg string) time.Duration {
		best := time.Duration(0)
		for i := range 5 {
			start := time.Now()
			for range 100 {
				ParameterizeMessage(msg)
			}
			if took := time.Since(start); i == 0 || took < best {
				best = took
			}
		}
		return best
	}
	if base, took := cost(plain), cost(unclosed); took > 3*base {
		t.Errorf("unclosed quotes took %v, the same text without them %v", took, base)
	}
}

// The rule order is part of the fingerprint: reordering changes keys.
func TestParameterizeMessageRuleOrder(t *testing.T) {
	want := []string{"sqlstate", "quoted", "url", "uuid", "mac", "ip", "date", "path", "hex", "id", "number"}
	if len(messageRules) != len(want) {
		t.Fatalf("rules = %d, want %d", len(messageRules), len(want))
	}
	for i, rule := range messageRules {
		if rule.name != want[i] {
			t.Errorf("rule %d = %s, want %s", i, rule.name, want[i])
		}
	}
}

// Each case is text two rules could claim; the earlier rule must win.
func TestParameterizeMessageEarlierRuleWins(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"quoted over url", `fetch "https://shop.example.invalid/a/b" failed`, "fetch <q> failed"},
		{"quoted over path", "open '/var/data/cart.json'", "open <q>"},
		{"quoted over number", `got "42"`, "got <q>"},
		{"url over path", "GET http://shop.example.invalid/var/data", "GET <url>"},
		{"url over ip", "GET http://10.0.0.7:8080/cart", "GET <url>"},
		{"uuid over hex and number", "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f", "<uuid>"},
		{"date over number", "2026-03-01", "<date>"},
		{"ip leaves a clock time to date", "at 10:11:12", "at <date>"},
		{"ip over date", "2001:db8:10:11:12::1", "<ip>"},
		{"ip over number", "10.0.0.7", "<ip>"},
		{"path over hex", "/orders/4bf92f3577b34da6.json", "<path>"},
		{"path over number", "/orders/42.json", "<path>"},
		{"hex over number", "1234567a", "<hex>"},
		{"id over number", "user_42", "user_<id>"},
		{"a kept code over number", "status 404", "status 404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParameterizeMessage(tc.in); got != tc.want {
				t.Errorf("ParameterizeMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParameterizeMessageMergesVariants(t *testing.T) {
	pairs := [][2]string{
		{"order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f not found for user 17",
			"order 0a1b2c3d-4e5f-4b7c-8e21-3f2b8c1e9d4a not found for user 2301"},
		{"timeout after 250ms calling 10.0.0.7:8080", "timeout after 3s calling 10.0.0.19:9090"},
		{`unknown sku "kettle-1" in /var/data/cart/7.json`, `unknown sku "pan-22" in /var/data/cart/1190.json`},
		{"Cart@1b2c3d closed at 2026-03-01T10:11:12Z", "Cart@99887766 closed at 2026-04-02T01:02:03.5Z"},
	}
	for _, p := range pairs {
		if a, b := ParameterizeMessage(p[0]), ParameterizeMessage(p[1]); a != b {
			t.Errorf("variants differ:\n  %q\n  %q", a, b)
		}
	}
}

// Every rule has a pair of messages it must bring together and a pair it
// must keep apart: a rule that merges too little splits one bug into many
// issues, one that merges too much hides a bug behind another.
func TestParameterizeMessageRulesMergeAndSplit(t *testing.T) {
	cases := []struct {
		rule         string
		merge, split [2]string
	}{
		{"quoted", [2]string{`unknown sku "kettle"`, `unknown sku "pan"`},
			[2]string{`unknown sku "kettle"`, `unknown shelf "kettle"`}},
		{"url", [2]string{"GET https://shop.example.invalid/a failed", "GET http://pay.example.invalid/b?c=1 failed"},
			[2]string{"GET https://shop.example.invalid/a failed", "PUT https://shop.example.invalid/a failed"}},
		{"uuid", [2]string{"order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f lost", "order 0a1b2c3d-4e5f-4b7c-8e21-3f2b8c1e9d4a lost"},
			[2]string{"order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f lost", "cart 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f lost"}},
		{"ip", [2]string{"peer 10.0.0.7:8080 gone", "peer 2001:db8:10:20:30::1 gone"},
			[2]string{"peer 10.0.0.7 gone", "peer 10.0.0.7 refused"}},
		{"date", [2]string{"expired 2026-03-01T10:11:12Z", "expired 2026-11-26 09:00"},
			[2]string{"expired 2026-03-01", "created 2026-03-01"}},
		{"path", [2]string{"open /var/data/cart/7.json", `open C:\data files\cart\9.json`},
			[2]string{"GET /cart/items", "GET /checkout/pay"}},
		{"path ids", [2]string{"GET /cart/7/items", "GET /cart/1190/items"},
			[2]string{"GET /cart/7/items", "GET /cart/7/coupons"}},
		{"hex", [2]string{"trace 4bf92f3577b34da6 lost at 0x1F", "trace a3ce929d0e0e4736 lost at 0xc000012345"},
			[2]string{"Cart@1b2c3d closed", "Order@1b2c3d closed"}},
		{"id", [2]string{"user_12345 blocked", "user_9s6XAbCdEf blocked"},
			[2]string{"user_12345 blocked", "cus_12345 blocked"}},
		{"number", [2]string{"Index -1 out of bounds for length 1234567", "Index 7 out of bounds for length 12345678"},
			[2]string{"retry 3 of 5", "page 3 of 5"}},
		{"number units", [2]string{"timeout after 250ms", "timeout after 12.5secs"},
			[2]string{"timeout after 250ms", "timeout after 250 rows"}},
		{"http status", [2]string{"HTTP 404 for cart 7", "HTTP 404 for cart 1190"},
			[2]string{"HTTP 404 sendError called", "HTTP 500 sendError called"}},
		{"error codes", [2]string{"SQLSTATE 23505 on row 7", "SQLSTATE 23505 on row 9"},
			[2]string{"SQLSTATE 23505", "SQLSTATE 08006"}},
		{"a code that is a value", [2]string{"Invalid promo code 12345", "Invalid promo code 777"},
			[2]string{"Invalid promo code 12345", "Invalid zip code 12345"}},
		{"a zip code", [2]string{"zip code 90210 not served", "zip code 10115 not served"},
			[2]string{"zip code 90210 not served", "zip code 90210 not found"}},
		{"a verification code", [2]string{"verification code 4821 expired", "verification code 77 expired"},
			[2]string{"verification code 4821 expired", "error code 4821 expired"}},
		{"compound durations", [2]string{"timeout after 1m0s", "timeout after 59s"},
			[2]string{"timeout after 1m0s", "timeout before 1m0s"}},
		{"colour codes", [2]string{"\x1b[31mCart 7 is locked\x1b[0m", "Cart 9 is locked"},
			[2]string{"\x1b[31mCart 7 is locked\x1b[0m", "\x1b[31mOrder 7 is locked\x1b[0m"}},
		{"windows path and prose", [2]string{`C:\app\a.json is locked by CORP\alice`, `D:\data\b.json is locked by CORP\alice`},
			[2]string{`C:\app\a.json is locked by CORP\alice`, `C:\app\a.json is missing for CORP\bob`}},
		{"scoped names", [2]string{"peer fd00::1f gone", "peer fd00::2a gone"},
			[2]string{"db::add failed", "ab::cd failed"}},
		{"prefixed uuid", [2]string{"order_123e4567-e89b-12d3-a456-426614174000 gone", "order_0a1b2c3d-4e5f-4b7c-8e21-3f2b8c1e9d4a gone"},
			[2]string{"order_123e4567-e89b-12d3-a456-426614174000 gone", "cart_123e4567-e89b-12d3-a456-426614174000 gone"}},
	}
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			if a, b := ParameterizeMessage(tc.merge[0]), ParameterizeMessage(tc.merge[1]); a != b {
				t.Errorf("must merge:\n  %q\n  %q", a, b)
			}
			if a, b := ParameterizeMessage(tc.split[0]), ParameterizeMessage(tc.split[1]); a == b {
				t.Errorf("must split, both %q:\n  %q\n  %q", a, tc.split[0], tc.split[1])
			}
		})
	}
}

func TestCollapseSpaces(t *testing.T) {
	for in, want := range map[string]string{
		"": "", " ": "", "a": "a", "a b": "a b", " a b": "a b", "a b ": "a b", "a  b": "a b",
		"a\tb": "a b", "a\nb": "a b", "a\u00a0b": "a b", "a\u2003b": "a b", "é à": "é à", "a\x00b": "a\x00b",
	} {
		if got := collapseSpaces(in); got != want {
			t.Errorf("collapseSpaces(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParameterizeMessageIsIdempotent(t *testing.T) {
	inputs := []string{
		`'<q>'`,
		`''a''`,
		`"a"b"c"`,
		"<n> <n>",
		"it's '5' o'clock",
		"/a/5 '/b/c' 0x10 10:11:12 ::1 2026-03-01",
		strings.Repeat("7 ", 400),
		strings.Repeat(`"x" `, 400),
	}
	for _, in := range inputs {
		once := ParameterizeMessage(in)
		if twice := ParameterizeMessage(once); twice != once {
			t.Errorf("not idempotent for %q:\n  once  %q\n  twice %q", in, once, twice)
		}
	}
	// A value whose quotes are doubled.
	if got := ParameterizeMessage(`''a''`); got != msgQuoted {
		t.Errorf("nested quotes = %q, want %q", got, msgQuoted)
	}
}

// The cut never leaves the start of a placeholder behind, which takes the
// longest one to know.
func TestPlaceholderMaxIsTheLongest(t *testing.T) {
	longest := 0
	for _, p := range msgPlaceholders {
		longest = max(longest, len(p))
	}
	if msgPlaceholderMax != longest {
		t.Errorf("msgPlaceholderMax = %d, the longest placeholder has %d bytes", msgPlaceholderMax, longest)
	}
}

func TestParameterizeMessageIsBounded(t *testing.T) {
	long := strings.Repeat("cart ", 200)
	got := ParameterizeMessage(long)
	if len(got) > ExceptionMessageMax {
		t.Errorf("len = %d, want <= %d", len(got), ExceptionMessageMax)
	}
	if want := strings.TrimRight(long[:ExceptionMessageMax], " "); got != want {
		t.Errorf("a long message must keep its head: got %q", got)
	}

	// Exactly at the bound nothing is cut.
	exact := strings.Repeat("a", ExceptionMessageMax)
	if got := ParameterizeMessage(exact); got != exact {
		t.Errorf("a message of exactly %d bytes was changed (len %d)", ExceptionMessageMax, len(got))
	}
	if got := ParameterizeMessage(exact + "a"); got != exact {
		t.Errorf("one byte over the bound must be cut to it (len %d)", len(got))
	}

	// At the bound the tail is left alone, even one that looks half cut.
	half := strings.Repeat("a", ExceptionMessageMax-4) + "<uui"
	if got := ParameterizeMessage(half); got != half {
		t.Errorf("a message of exactly %d bytes lost its tail: %q", ExceptionMessageMax, got[len(got)-8:])
	}

	// The cut never leaves half a placeholder, whether one byte of it is
	// missing or all but one.
	head := strings.Repeat("a", ExceptionMessageMax-6) + " "
	got = ParameterizeMessage(head + "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f")
	if want := strings.TrimRight(head, " "); got != want {
		t.Errorf("half a placeholder survived the cut: %q", got[len(got)-12:])
	}
	head = strings.Repeat("a", ExceptionMessageMax-2) + " "
	got = ParameterizeMessage(head + "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f")
	if want := strings.TrimRight(head, " "); got != want {
		t.Errorf("half a placeholder survived the cut: %q", got[len(got)-12:])
	}
	// A placeholder that ends exactly at the bound is kept whole.
	head = strings.Repeat("a", ExceptionMessageMax-len(msgUUID)-1) + " "
	got = ParameterizeMessage(head + "3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f tail")
	if want := head + msgUUID; got != want {
		t.Errorf("a whole placeholder at the bound was cut: %q", got[len(got)-12:])
	}
	// Only the placeholder the cut went through goes: a whole one before it
	// in the tail stays.
	head = strings.Repeat("a", ExceptionMessageMax-5)
	got = ParameterizeMessage(head + `"kettle"http://shop.example.invalid/cart`)
	if want := head + msgQuoted; got != want {
		t.Errorf("the cut behind a whole placeholder left %q", got[len(got)-12:])
	}
	// A literal "<" at the cut is text unless a placeholder starts like
	// what follows it.
	head = strings.Repeat("a", ExceptionMessageMax-5)
	if got, want := ParameterizeMessage(head+" x < y zzzz"), head+" x <"; got != want {
		t.Errorf("a literal < at the cut took the text before it: %q", got[len(got)-12:])
	}
	// A literal "<" far from the end is not mistaken for one.
	head = "a < b " + strings.Repeat("a", ExceptionMessageMax)
	if got := ParameterizeMessage(head); got != head[:ExceptionMessageMax] {
		t.Errorf("a literal < changed the cut: len %d", len(got))
	}

	// Nor is one a whole placeholder's length before the cut.
	head = strings.Repeat("a", ExceptionMessageMax-len(msgUUID)) + "<aaaaa"
	if got := ParameterizeMessage(head + "aaaa"); got != head {
		t.Errorf("a literal < was cut as a placeholder: %q", got[len(got)-8:])
	}

	// A multi-byte rune on the bound is dropped whole, not split.
	runes := strings.Repeat("a", ExceptionMessageMax-1) + "é"
	got = ParameterizeMessage(runes)
	if !utf8.ValidString(got) || len(got) != ExceptionMessageMax-1 {
		t.Errorf("rune cut: valid=%v len=%d", utf8.ValidString(got), len(got))
	}

	// Input of exactly the read cap is read whole.
	if got := ParameterizeMessage(strings.Repeat(" ", exceptionMessageInput-1) + "x"); got != "x" {
		t.Errorf("the last byte inside the input cap was lost: %q", got)
	}

	// The cap counts the bytes of the input, whatever they are: invalid
	// ones before it do not let later text in.
	if got := ParameterizeMessage(strings.Repeat("\x80", exceptionMessageInput) + "late"); got != "" {
		t.Errorf("text past the input cap leaked behind invalid bytes: %q", got)
	}
	if got := ParameterizeMessage(strings.Repeat("\x80", exceptionMessageInput-4) + "late"); got != "late" {
		t.Errorf("text inside the input cap was lost: %q", got)
	}
	// A rune the cap goes through is dropped whole.
	if got := ParameterizeMessage(strings.Repeat("a", exceptionMessageInput-1) + "é"); !utf8.ValidString(got) {
		t.Errorf("the cap split a rune: %q", got[len(got)-4:])
	}
	// Input beyond the read cap is never looked at.
	if got := ParameterizeMessage(strings.Repeat(" ", exceptionMessageInput) + "late"); got != "" {
		t.Errorf("text past the input cap leaked: %q", got)
	}
	if got := ParameterizeMessage(strings.Repeat(" ", exceptionMessageInput-4) + "late"); got != "late" {
		t.Errorf("text inside the input cap was lost: %q", got)
	}
}

func TestParameterizeMessageDropsInvalidUTF8(t *testing.T) {
	if got := ParameterizeMessage("cart \xff\xfe locked"); got != "cart locked" {
		t.Errorf("got %q", got)
	}
	// An invalid byte is dropped, not turned into a break between words.
	if got := ParameterizeMessage("ca\xffrt lo\xc3cked"); got != "cart locked" {
		t.Errorf("got %q", got)
	}
}

// FuzzParameterizeMessage checks the properties that must hold for any
// input: no panic, a bounded valid result, and idempotence.
func FuzzParameterizeMessage(f *testing.F) {
	for _, seed := range []string{
		"", "'<q>'", `''a''`, "order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f", "::1.", "[::1]:80",
		"/a/b '/c/d' 0x1F 10.0.0.7:80 2026-03-01T10:11:12Z 250ms", "<", "<uui", "a@1b", `C:\a\b`,
		"GET /cart/42/items", "/a/b.c.", `C:\a b\c d\e.f g`, "user_1 cus_9s6X", "Index -1", "x=-5",
		"HTTP 404", "status code: 500", "SQLSTATE[08006]", "ORA-00942", "2001:db8:10:20:30::1", "5000msec",
		strings.Repeat("9 ", 300), strings.Repeat("<", 300), "\xff'\xfe'", "a\u017ftatus 404", "to\u212aen 42",
		"\x1b[31mCart 7\x1b[0m", `"a \"b\" c"`, "1m0s", `C:\a (x86)\b c d e\f`, "db::add", "order_123e4567-e89b-12d3-a456-426614174000",
		strings.Repeat("a", 251) + " x < y zzzz", strings.Repeat("\x80", 300) + "<n", "status: 12.5ms",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := ParameterizeMessage(in)
		if len(got) > ExceptionMessageMax {
			t.Fatalf("len = %d for %q", len(got), in)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 for %q: %q", in, got)
		}
		if again := ParameterizeMessage(got); again != got {
			t.Fatalf("not idempotent for %q:\n  once  %q\n  twice %q", in, got, again)
		}
		// A rule is only skipped where it would have changed nothing.
		for _, rule := range messageRules {
			if !rule.applies(in) && rule.apply(in) != in {
				t.Fatalf("rule %s is skipped for %q but changes it to %q", rule.name, in, rule.apply(in))
			}
		}
		if want := strings.Join(strings.Fields(in), " "); collapseSpaces(in) != want {
			t.Fatalf("collapseSpaces(%q) = %q, want %q", in, collapseSpaces(in), want)
		}
	})
}
