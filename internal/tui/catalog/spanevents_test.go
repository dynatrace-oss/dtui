package catalog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/exckey"
)

// exceptionSpan mirrors the live shapes: a OneAgent/Java exception event with
// the Grail stack_trace spelling and file/line, next to a feature_flag event.
func exceptionSpan() map[string]any {
	return map[string]any{
		"span.name":        "GET /v1/orders",
		"span.kind":        "server",
		"span.status_code": nil, // most exception-bearing spans are NOT failed
		"span.events": []any{
			map[string]any{
				"span_event.name":       "exception",
				"exception.type":        "javax.servlet.http.HttpServletResponse.setStatus",
				"exception.message":     "HTTP 404 setStatus called",
				"exception.stack_trace": "a.b.C.one (C.java:1)\na.b.C.two (C.java:2)",
				"exception.file.full":   "C.java",
				"exception.line_number": "243",
			},
			map[string]any{
				"span_event.name":  "feature_flag",
				"feature_flag.key": "recommendationCacheFailure",
			},
		},
	}
}

func TestSpanEventsParsesNamesAndFields(t *testing.T) {
	events := SpanEvents(exceptionSpan())
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Name != "exception" || events[1].Name != "feature_flag" {
		t.Errorf("names = %q, %q", events[0].Name, events[1].Name)
	}
	if SpanEvents(map[string]any{"span.name": "no events"}) != nil {
		t.Error("a span without span.events must yield nil")
	}
}

func TestSpanExceptionsCoalescesBothSpellings(t *testing.T) {
	excs := SpanExceptions(exceptionSpan())
	if len(excs) != 1 {
		t.Fatalf("exceptions = %d, want 1 (the feature_flag event is not one)", len(excs))
	}
	ex := excs[0]
	if ex.Type != "javax.servlet.http.HttpServletResponse.setStatus" ||
		ex.Message != "HTTP 404 setStatus called" {
		t.Errorf("type/message = %q / %q", ex.Type, ex.Message)
	}
	if !strings.Contains(ex.StackTrace, "C.java:1") {
		t.Errorf("stack trace = %q", ex.StackTrace)
	}
	if ex.Location != "C.java:243" {
		t.Errorf("location = %q, want C.java:243", ex.Location)
	}

	// The pure-OTel spelling (exception.stacktrace) coalesces too.
	otel := map[string]any{"span.events": []any{map[string]any{
		"span_event.name": "exception", "exception.type": "*fmt.wrapError",
		"exception.stacktrace": "main.run\n\tmain.go:42",
	}}}
	if got := SpanExceptions(otel); len(got) != 1 || !strings.Contains(got[0].StackTrace, "main.go:42") {
		t.Errorf("otel spelling exceptions = %+v", got)
	}

	if SpanExceptions(nil) != nil {
		t.Error("nil record must yield nil, not panic")
	}
}

// TestSpanExceptionsMemoizes: column Value funcs run per render frame — the
// parse must happen once per record.
func TestSpanExceptionsMemoizes(t *testing.T) {
	rec := exceptionSpan()
	first := SpanExceptions(rec)
	rec["span.events"] = []any{} // would parse to nothing…
	if again := SpanExceptions(rec); len(again) != len(first) {
		t.Error("second call must return the memoized parse")
	}
}

func TestExceptionTypeShort(t *testing.T) {
	cases := map[string]string{
		"javax.servlet.http.HttpServletResponse.setStatus": "HttpServletResponse.setStatus",
		"*fmt.wrapError":  "*fmt.wrapError",
		"CLOSE_Exception": "CLOSE_Exception",
		"":                "",
	}
	for in, want := range cases {
		if got := ExceptionTypeShort(in); got != want {
			t.Errorf("ExceptionTypeShort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSpanErrored: the OTel status code alone must mark a span errored — the
// request verdict exists only on entry spans (the waterfall's old gap).
func TestSpanErrored(t *testing.T) {
	if !SpanErrored(map[string]any{"span.status_code": "error"}) {
		t.Error("status_code error must count as errored")
	}
	if !SpanErrored(map[string]any{"request.is_failed": true}) {
		t.Error("request verdict must count as errored")
	}
	if SpanErrored(map[string]any{"span.status_code": "ok"}) {
		t.Error("ok span is not errored")
	}
}

// TestSpanErrorBrief: HTTP error status wins, then the gRPC status name, then
// the exception type, then the status message.
func TestSpanErrorBrief(t *testing.T) {
	rec := exceptionSpan()
	rec["http.response.status_code"] = float64(503)
	rec["rpc.grpc.status_code"] = float64(4)
	if got := SpanErrorBrief(rec); got != "HTTP 503" {
		t.Errorf("brief = %q, want HTTP 503", got)
	}
	delete(rec, "http.response.status_code")
	if got := SpanErrorBrief(rec); got != "DEADLINE_EXCEEDED" {
		t.Errorf("brief = %q, want DEADLINE_EXCEEDED", got)
	}
	delete(rec, "rpc.grpc.status_code")
	if got := SpanErrorBrief(rec); got != "HttpServletResponse.setStatus" {
		t.Errorf("brief = %q, want the short exception type", got)
	}
	msgOnly := map[string]any{"span.status_message": "upstream connect error"}
	if got := SpanErrorBrief(msgOnly); got != "upstream connect error" {
		t.Errorf("brief = %q, want the status message", got)
	}
	// A 200 on a failed span is not an error story — fall through.
	ok200 := map[string]any{"http.status_code": float64(200), "span.status_message": "canceled"}
	if got := SpanErrorBrief(ok200); got != "canceled" {
		t.Errorf("brief = %q, want canceled (200 is not an error)", got)
	}
}

// TestSpanPreviewSurfacesExceptions: exceptions render as error-classed facts
// with the message wrapped — regardless of the status verdict.
func TestSpanPreviewSurfacesExceptions(t *testing.T) {
	rec := exceptionSpan()
	rec["span.events"] = append(rec["span.events"].([]any), map[string]any{
		"span_event.name": "exception", "exception.type": "CLOSE_Exception", "exception.message": "-501",
	})
	facts := PreviewFacts(rec)
	f := factByLabel(facts, "exceptions ×2")
	if f.Value != "javax.servlet.http.HttpServletResponse.setStatus" || f.Class != "error" {
		t.Errorf("exceptions fact = %+v", f)
	}
	if m := factByLabel(facts, "thrown"); !m.Wrap || m.Value != "HTTP 404 setStatus called" {
		t.Errorf("thrown fact = %+v", m)
	}
}

// TestSpanPreviewGRPCStatus: the rpc arm names the gRPC verdict.
func TestSpanPreviewGRPCStatus(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "oteldemo.CartService/GetCart", "span.kind": "client",
		"rpc.service": "oteldemo.CartService", "rpc.method": "GetCart",
		"rpc.system": "grpc", "rpc.grpc.status_code": float64(14),
	})
	if f := factByLabel(facts, "grpc"); f.Value != "UNAVAILABLE" || f.Class != "error" {
		t.Errorf("grpc fact = %+v, want UNAVAILABLE/error", f)
	}
	okFacts := PreviewFacts(map[string]any{
		"span.name": "x", "span.kind": "client",
		"rpc.system": "grpc", "rpc.grpc.status_code": float64(0),
	})
	if f := factByLabel(okFacts, "grpc"); f.Value != "OK" || f.Class != "dim" {
		t.Errorf("grpc ok fact = %+v, want OK/dim", f)
	}
}

// TestSpanPreviewHTTPStatusWithoutMethod: a failed span's 503 must not vanish
// because the instrumentation skipped the method attribute.
func TestSpanPreviewHTTPStatusWithoutMethod(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "handler", "span.kind": "server",
		"http.response.status_code": float64(503),
	})
	if f := factByLabel(facts, "http"); f.Value != "503" || f.Class != "error" {
		t.Errorf("http fact = %+v, want 503/error", f)
	}
}

// The root flag is a boolean, or the word a string attribute spells it
// with. Anything else is not the flag.
func TestSpanExceptionRootFlag(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{true, true}, {"true", true}, {false, false}, {"false", false}, {nil, false}, {"", false},
		{[]any{true}, false}, {[]any{"true"}, false}, {map[string]any{"true": true}, false}, {1.0, false}, {"TRUE", false},
	} {
		excs := SpanExceptions(map[string]any{"span.events": []any{
			map[string]any{"span_event.name": "exception", "exception.type": "A", "exception.is_caused_by_root": tc.value}}})
		if len(excs) != 1 || excs[0].CauseRoot != tc.want {
			t.Errorf("exception.is_caused_by_root = %#v: %+v, want root %v", tc.value, excs, tc.want)
		}
	}
}

func TestSpanExceptionCarriesTheRootFlag(t *testing.T) {
	// What live spans carry: an id per event and the flag on the root.
	live := SpanExceptions(map[string]any{"span.events": []any{
		map[string]any{"span_event.name": "exception", "exception.type": "Wrapper", "exception.id": "00000000000000a7"},
		map[string]any{"span_event.name": "exception", "exception.type": "Root",
			"exception.id": "00000000000000b8", "exception.is_caused_by_root": true},
		map[string]any{"span_event.name": "exception", "exception.type": "Outer", "exception.is_caused_by_root": false},
	}})
	want := []SpanException{{Type: "Wrapper"}, {Type: "Root", CauseRoot: true}, {Type: "Outer"}}
	if !reflect.DeepEqual(live, want) {
		t.Errorf("exceptions = %+v\nwant %+v", live, want)
	}
	if fp, _ := FingerprintExceptions(live); fp.Type != "Root" {
		t.Errorf("fingerprinted %s, want the flagged root", fp.Type)
	}
	live[1].CauseRoot = false
	if fp, _ := FingerprintExceptions(live); fp.Type != "Outer" {
		t.Errorf("fingerprinted %s, want the last event where nothing marks a chain", fp.Type)
	}
}

// The key is computed from the type, the message, the stack trace and the
// root flag of the events; a location alone says nothing.
func TestFingerprintExceptionsHandsTheEventsOn(t *testing.T) {
	excs := SpanExceptions(exceptionSpan())
	fp, ok := FingerprintExceptions(excs)
	if !ok || !strings.HasPrefix(fp.Key, exckey.ExceptionKeyVersion+":") || fp.EventType != excs[len(excs)-1].Type {
		t.Errorf("fingerprint %+v, %v", fp, ok)
	}
	if fp, ok := FingerprintExceptions([]SpanException{{Location: "Cart.java:42"}}); ok {
		t.Errorf("a location alone got the key %s", fp.Key)
	}
	if _, ok := FingerprintExceptions(nil); ok {
		t.Error("no events got a key")
	}
}

// From a raw span record to the key: every field the key reads must reach
// it, under either spelling of the stack trace, and the key is the one
// exckey gives for the same fields.
func TestFingerprintExceptionsFromARawRecord(t *testing.T) {
	const (
		java  = "java.lang.IllegalStateException: Cart 7 is locked\n\tat com.example.shop.Cart.add(Cart.java:42)\n\tat com.example.shop.Api.post(Api.java:12)\n"
		other = "java.lang.IllegalStateException: Cart 7 is locked\n\tat com.example.shop.Stock.take(Stock.java:9)\n"
	)
	event := func(fields map[string]any) map[string]any {
		fields["span_event.name"] = "exception"
		return fields
	}
	record := func(events ...any) map[string]any { return map[string]any{"span.events": events} }
	cartFrames := []exckey.StackFrame{
		{Module: "com.example.shop.Cart", Function: "add", Syntax: exckey.StackJava},
		{Module: "com.example.shop.Api", Function: "post", Syntax: exckey.StackJava},
	}
	stockFrames := []exckey.StackFrame{{Module: "com.example.shop.Stock", Function: "take", Syntax: exckey.StackJava}}
	const typ = "java.lang.IllegalStateException"
	for _, tc := range []struct {
		name   string
		rec    map[string]any
		same   exckey.Event
		rung   exckey.FingerprintRung
		frames []exckey.StackFrame
		msg    string
	}{
		{"the Grail spelling", record(event(map[string]any{"exception.type": typ, "exception.stack_trace": java})),
			exckey.Event{Type: typ, StackTrace: java}, exckey.RungAppFrames, cartFrames, ""},
		{"the OpenTelemetry spelling", record(event(map[string]any{"exception.type": typ, "exception.stacktrace": java})),
			exckey.Event{Type: typ, StackTrace: java}, exckey.RungAppFrames, cartFrames, ""},
		{"both spellings: Grail's", record(event(map[string]any{"exception.type": typ, "exception.stack_trace": java, "exception.stacktrace": other})),
			exckey.Event{Type: typ, StackTrace: java}, exckey.RungAppFrames, cartFrames, ""},
		{"both spellings, the other way round", record(event(map[string]any{"exception.type": typ, "exception.stack_trace": other, "exception.stacktrace": java})),
			exckey.Event{Type: typ, StackTrace: other}, exckey.RungAppFrames, stockFrames, ""},
		{"Grail's spelling of white space alone", record(event(map[string]any{"exception.type": typ, "exception.stack_trace": " \n\t", "exception.stacktrace": java})),
			exckey.Event{Type: typ, StackTrace: java}, exckey.RungAppFrames, cartFrames, ""},
		{"Grail's spelling empty", record(event(map[string]any{"exception.type": typ, "exception.stack_trace": "", "exception.stacktrace": java})),
			exckey.Event{Type: typ, StackTrace: java}, exckey.RungAppFrames, cartFrames, ""},
		{"a message alone", record(event(map[string]any{"exception.message": "Cart 7 is locked"})),
			exckey.Event{Message: "Cart 7 is locked"}, exckey.RungMessage, nil, "Cart <n> is locked"},
		{"a type and a message", record(event(map[string]any{"exception.type": typ, "exception.message": "Cart 7 is locked"})),
			exckey.Event{Type: typ, Message: "Cart 7 is locked"}, exckey.RungMessage, nil, "Cart <n> is locked"},
		{"a flagged chain", record(
			event(map[string]any{"exception.type": "com.example.shop.CheckoutException", "exception.stack_trace": other}),
			event(map[string]any{"exception.type": typ, "exception.stack_trace": java, "exception.is_caused_by_root": true}),
			event(map[string]any{"exception.type": "java.lang.RuntimeException", "exception.message": "wrapped"})),
			exckey.Event{Type: typ, StackTrace: java}, exckey.RungAppFrames, cartFrames, ""},
	} {
		got, ok := FingerprintExceptions(SpanExceptions(tc.rec))
		want, wantOK := exckey.FingerprintExceptions([]exckey.Event{tc.same})
		if !ok || !wantOK || got.Key != want.Key {
			t.Errorf("%s: key %s %v, want %s", tc.name, got.Key, ok, want.Key)
		}
		if got.Rung != tc.rung || got.Type != tc.same.Type || !reflect.DeepEqual(got.Frames, tc.frames) || got.Message != tc.msg {
			t.Errorf("%s: rung %v type %q message %q frames %+v", tc.name, got.Rung, got.Type, got.Message, got.Frames)
		}
	}
	// Each field moves the key.
	key := func(fields map[string]any) string {
		fp, ok := FingerprintExceptions(SpanExceptions(record(event(fields))))
		if !ok {
			t.Fatalf("%v has no key", fields)
		}
		return fp.Key
	}
	if key(map[string]any{"exception.type": typ, "exception.message": "locked"}) == key(map[string]any{"exception.type": typ, "exception.message": "sold out"}) {
		t.Error("the message does not reach the key")
	}
	if key(map[string]any{"exception.type": typ, "exception.stack_trace": java}) == key(map[string]any{"exception.type": typ, "exception.stack_trace": other}) {
		t.Error("the stack trace does not reach the key")
	}
	if key(map[string]any{"exception.type": typ}) == key(map[string]any{"exception.type": "java.lang.Error"}) {
		t.Error("the type does not reach the key")
	}
}
