package exckey

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var updateExceptionKeys = flag.Bool("update", false,
	"add the keys of new corpus cases to testdata/exceptions/<version>-keys.golden")

const cartStack = `java.lang.IllegalStateException: Cart 7 is locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	at org.springframework.web.Dispatcher.dispatch(Dispatcher.java:9)
	at com.acme.shop.CartController.post(CartController.java:17)`

func mustFingerprint(t *testing.T, excs ...Event) ExceptionFingerprint {
	t.Helper()
	fp, ok := FingerprintExceptions(excs)
	if !ok {
		t.Fatal("no fingerprint")
	}
	return fp
}

func TestFingerprintExceptionsLadder(t *testing.T) {
	const typ = "java.lang.IllegalStateException"
	runtimeStack := "\tat java.base/java.util.ArrayList.get(ArrayList.java:427)\n\tat java.base/java.lang.Thread.run(Thread.java:833)"

	t.Run("in-app frames", func(t *testing.T) {
		fp := mustFingerprint(t, Event{Type: " " + typ + "\n", Message: "Cart 7 is locked", StackTrace: cartStack})
		want := frames(StackJava, "com.acme.shop.CartService", "addItem", "com.acme.shop.CartController", "post")
		if fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
			t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
		}
		if fp.Type != typ || fp.Message != "Cart <n> is locked" {
			t.Errorf("type %q message %q", fp.Type, fp.Message)
		}
	})
	t.Run("all frames when none is in-app", func(t *testing.T) {
		fp := mustFingerprint(t, Event{Type: typ, Message: "Cart 7 is locked", StackTrace: runtimeStack})
		want := frames(StackJava, "java.util.ArrayList", "get", "java.lang.Thread", "run")
		if fp.Rung != RungFrames || !reflect.DeepEqual(fp.Frames, want) {
			t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
		}
	})
	t.Run("message when no frame parses", func(t *testing.T) {
		fp := mustFingerprint(t, Event{Type: typ, Message: "Cart 7 is locked", StackTrace: "no frames here"})
		if fp.Rung != RungMessage || fp.Frames != nil || fp.Message != "Cart <n> is locked" {
			t.Errorf("rung %v frames %+v message %q", fp.Rung, fp.Frames, fp.Message)
		}
	})
	t.Run("type alone", func(t *testing.T) {
		fp := mustFingerprint(t, Event{Type: typ, Message: " \n "})
		if fp.Rung != RungType || fp.Frames != nil || fp.Message != "" {
			t.Errorf("rung %v frames %+v message %q", fp.Rung, fp.Frames, fp.Message)
		}
	})
	t.Run("each rung keys differently", func(t *testing.T) {
		keys := map[string]string{}
		for name, ex := range map[string]Event{
			"app frames": {Type: typ, Message: "Cart 7 is locked", StackTrace: cartStack},
			"frames":     {Type: typ, Message: "Cart 7 is locked", StackTrace: runtimeStack},
			"message":    {Type: typ, Message: "Cart 7 is locked"},
			"type":       {Type: typ},
		} {
			key := mustFingerprint(t, ex).Key
			if other, dup := keys[key]; dup {
				t.Errorf("%s and %s share key %s", name, other, key)
			}
			keys[key] = name
		}
	})
	t.Run("no events", func(t *testing.T) {
		if fp, ok := FingerprintExceptions(nil); ok || !reflect.DeepEqual(fp, ExceptionFingerprint{}) {
			t.Errorf("got %+v, %v", fp, ok)
		}
	})
}

func TestFingerprintKeyShape(t *testing.T) {
	fp := mustFingerprint(t, Event{Type: "java.lang.IllegalStateException", StackTrace: cartStack})
	if !exceptionKeyRe.MatchString(fp.Key) {
		t.Errorf("key = %q, want %s: and 12 hex characters", fp.Key, ExceptionKeyVersion)
	}
	// The hash input: rung number, type, then module and function per
	// frame, each field behind its length.
	sum := sha256.Sum256([]byte("1:1" + "31:java.lang.IllegalStateException" +
		"25:com.acme.shop.CartService" + "7:addItem" + "28:com.acme.shop.CartController" + "4:post"))
	if want := ExceptionKeyVersion + ":" + hex.EncodeToString(sum[:])[:12]; fp.Key != want {
		t.Errorf("key = %s, want %s", fp.Key, want)
	}
	// Evidence is positional and ordered.
	a := fingerprintKey(RungMessage, "a", []string{"b"})
	for name, other := range map[string]string{
		"type and message swapped": fingerprintKey(RungMessage, "b", []string{"a"}),
		"another rung":             fingerprintKey(RungType, "a", []string{"b"}),
		"evidence split in two":    fingerprintKey(RungMessage, "a", []string{"", "b"}),
		"more evidence":            fingerprintKey(RungMessage, "a", []string{"b", "c"}),
	} {
		if other == a {
			t.Errorf("%s: same key %s", name, a)
		}
	}
	if fingerprintKey(RungFrames, "a", []string{"b", "c"}) == fingerprintKey(RungFrames, "a", []string{"c", "b"}) {
		t.Error("frame order must matter")
	}
}

// No two different inputs may write the same canonical string: a separator
// that can also occur inside a field lets one field run into the next.
func TestFingerprintKeyFieldsCannotRunTogether(t *testing.T) {
	a := mustFingerprint(t, Event{Type: "T\x00a.b\x1fc", StackTrace: javaStack("d.e.f")})
	b := mustFingerprint(t, Event{Type: "T", StackTrace: javaStack("a.b.c", "d.e.f")})
	if a.Rung != RungAppFrames || b.Rung != RungAppFrames || len(a.Frames) != 1 || len(b.Frames) != 2 {
		t.Fatalf("frames %+v and %+v", a.Frames, b.Frames)
	}
	if a.Key == b.Key {
		t.Errorf("a type that spells out a frame shares key %s with the frame", a.Key)
	}
	keys := map[string][]string{}
	for _, fields := range [][]string{
		{"ab", "c"}, {"a", "bc"}, {"abc"}, {"abc", ""}, {"", "abc"}, {"a", "b", "c"},
		{"1:a"}, {"a", "1:b"}, {"a1:b"}, {"a\x00b"}, {"a", "\x00b"}, {"2:ab", "c"}, {"12:ab"},
	} {
		key := fingerprintKey(RungFrames, "T", fields)
		if other, dup := keys[key]; dup {
			t.Errorf("%q and %q share key %s", fields, other, key)
		}
		keys[key] = fields
	}
	if fingerprintKey(RungFrames, "T1", []string{"a"}) == fingerprintKey(RungFrames, "T", []string{"1a"}) {
		t.Error("the type runs into the evidence")
	}
}

func TestFingerprintRungLabels(t *testing.T) {
	want := map[FingerprintRung]string{
		RungAppFrames: "app frames", RungFrames: "frames", RungMessage: "message", RungType: "type", 0: "",
	}
	for rung, label := range want {
		if got := rung.String(); got != label {
			t.Errorf("rung %d = %q, want %q", int(rung), got, label)
		}
	}
	if RungAppFrames != 1 || RungFrames != 2 || RungMessage != 3 || RungType != 4 {
		t.Error("rung numbers are hashed into keys and must not move")
	}
}

// javaStack builds a trace of the given methods, one frame each.
func javaStack(methods ...string) string {
	var b strings.Builder
	for i, m := range methods {
		fmt.Fprintf(&b, "\tat %s(File.java:%d)\n", m, i+1)
	}
	return b.String()
}

func TestFingerprintUsesTopFiveFrames(t *testing.T) {
	key := func(methods ...string) string {
		t.Helper()
		return mustFingerprint(t, Event{Type: "T", StackTrace: javaStack(methods...)}).Key
	}
	base := []string{"com.acme.A.a", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.E.e", "com.acme.F.f"}
	fp := mustFingerprint(t, Event{Type: "T", StackTrace: javaStack(base...)})
	if len(fp.Frames) != FingerprintFrames || fp.Frames[4].Function != "e" {
		t.Fatalf("frames = %+v, want the top %d", fp.Frames, FingerprintFrames)
	}
	if key(base...) != key("com.acme.A.a", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.E.e", "com.acme.Z.z") {
		t.Error("the sixth frame must not change the key")
	}
	if key(base...) == key("com.acme.A.a", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.Z.z", "com.acme.F.f") {
		t.Error("the fifth frame must change the key")
	}
	if key(base...) == key("com.acme.Z.z", "com.acme.B.b", "com.acme.C.c", "com.acme.D.d", "com.acme.E.e") {
		t.Error("the top frame must change the key")
	}
	if key("com.acme.A.a") != key("com.acme.A.a", "java.lang.Thread.run") {
		t.Error("a runtime frame below must not change an in-app key")
	}
	// Five runtime frames on top do not push the in-app frame out, and the
	// topmost of them, the throw site, is part of the key.
	deep := []string{"java.util.A.a", "java.util.B.b", "java.util.C.c", "java.util.D.d", "java.util.E.e", "com.acme.A.a"}
	fp = mustFingerprint(t, Event{Type: "T", StackTrace: javaStack(deep...)})
	if want := frames(StackJava, "java.util.A", "a", "com.acme.A", "a"); fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
		t.Errorf("rung %v frames %+v, want the throw site and the in-app frame", fp.Rung, fp.Frames)
	}
	if key(deep...) != key("java.util.A.a", "com.acme.A.a") {
		t.Error("runtime frames between the throw site and the in-app frame must not change the key")
	}
	// The throw site comes on top of the five in-app frames, not instead of
	// one of them.
	fp = mustFingerprint(t, Event{Type: "T", StackTrace: javaStack(append([]string{"java.util.A.a"}, base...)...)})
	if len(fp.Frames) != FingerprintFrames+1 || fp.Frames[0].Module != "java.util.A" || fp.Frames[5].Function != "e" {
		t.Errorf("frames = %+v, want the throw site and the top %d in-app frames", fp.Frames, FingerprintFrames)
	}
	// The same holds on the all-frames rung: top five, in order.
	runtime := []string{"java.util.A.a", "java.util.B.b", "java.util.C.c", "java.util.D.d", "java.util.E.e", "java.util.F.f"}
	fp = mustFingerprint(t, Event{Type: "T", StackTrace: javaStack(runtime...)})
	if fp.Rung != RungFrames || len(fp.Frames) != FingerprintFrames || fp.Frames[4].Function != "e" {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

func TestFingerprintCollapsesRecursionAcrossFrameworkFrames(t *testing.T) {
	direct := javaStack("com.acme.Tree.walk", "com.acme.Tree.start")
	viaProxy := javaStack("com.acme.Tree.walk", "org.springframework.aop.Proxy.invoke",
		"com.acme.Tree.walk", "org.springframework.aop.Proxy.invoke", "com.acme.Tree.walk", "com.acme.Tree.start")
	a := mustFingerprint(t, Event{Type: "T", StackTrace: direct})
	b := mustFingerprint(t, Event{Type: "T", StackTrace: viaProxy})
	if a.Key != b.Key || len(b.Frames) != 2 {
		t.Errorf("recursion through a proxy split the group: %+v vs %+v", a.Frames, b.Frames)
	}
}

// A panic in a handler of a Go service: the application's module has no host
// in its path, the router is a dependency from the module cache.
func TestFingerprintGoApplicationFrames(t *testing.T) {
	read := func(name string) Event {
		t.Helper()
		stack, err := os.ReadFile(filepath.Join(exceptionCorpusDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return Event{Type: "runtime.Error", StackTrace: string(stack)}
	}
	add, addAgain, remove := mustFingerprint(t, read("go-handler-add.txt")),
		mustFingerprint(t, read("go-handler-add-b.txt")), mustFingerprint(t, read("go-handler-remove.txt"))
	want := frames(StackGo, "shop/internal/cart", "Service.AddItem", "shop/internal/api", "Handler.PostItem")
	if add.Rung != RungAppFrames || !reflect.DeepEqual(add.Frames, want) {
		t.Errorf("rung %v frames %+v, want the application's two", add.Rung, add.Frames)
	}
	if add.Key != addAgain.Key {
		t.Error("the same panic under another router version must keep its key")
	}
	if add.Key == remove.Key {
		t.Error("two panics in one binary must not share a key")
	}
}

// The type and the frames of a fingerprint describe the same exception. Where
// the stack text of one event holds a chain, that is the root cause: its
// type is read from the text, and the event's own type is kept for display.
func TestFingerprintTypeIsTheRootCauses(t *testing.T) {
	cases := []struct {
		name, eventType, stack string
		wantType               string
		wantTop                string // function of the first frame
	}{
		{"java: the last cause", "com.example.shop.CheckoutException", `com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: com.example.shop.CartException: cart failed
	at com.example.shop.Cart.add(Cart.java:12)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	at com.example.shop.CartService.addItem(CartService.java:42)`,
			"java.lang.IllegalStateException", "addItem"},
		{"java: a cause without a message", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: java.lang.NullPointerException
	at com.example.shop.CartService.addItem(CartService.java:42)`,
			"java.lang.NullPointerException", "addItem"},
		{"java: a last cause without frames is not the root", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: com.example.shop.CartException: cart failed
	at com.example.shop.Cart.add(Cart.java:12)
Caused by: java.lang.IllegalStateException: Cart 7 is locked`,
			"com.example.shop.CartException", "add"},
		{"java: no chain", "com.example.shop.CheckoutException", `java.lang.Error: the text names another type
	at com.example.shop.Checkout.run(Checkout.java:30)`,
			"com.example.shop.CheckoutException", "run"},
		{"java: a cause whose type cannot be read", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
Caused by: <unknown>
	at com.example.shop.CartService.addItem(CartService.java:42)`,
			"com.example.shop.CheckoutException", "addItem"},
		{"java: the cause of a suppressed exception is nobody's root", "com.example.shop.CheckoutException", `	at com.example.shop.Checkout.run(Checkout.java:30)
	Suppressed: java.io.IOException: close failed
		at com.example.shop.Conn.close(Conn.java:5)
	Caused by: java.net.SocketException: broken pipe
		at com.example.shop.Socket.write(Socket.java:9)`,
			"com.example.shop.CheckoutException", "run"},
		{"dotnet: the innermost exception", "Example.Shop.CheckoutException", `Example.Shop.CheckoutException: checkout failed
 ---> Example.Shop.CartException: cart failed
 ---> System.InvalidOperationException: Cart 7 is locked
   at Example.Shop.CartService.AddItem(String sku)
   --- End of inner exception stack trace ---
   at Example.Shop.Cart.Add(String sku)
   --- End of inner exception stack trace ---
   at Example.Shop.Checkout.Run()`,
			"System.InvalidOperationException", "AddItem"},
		{"dotnet: inner exceptions on the first line", "System.AggregateException",
			"System.AggregateException: One or more errors occurred. ---> (Inner Exception #0) System.TimeoutException: timed out\n" +
				"   at Example.Shop.Stock.Reserve(String sku)\n   --- End of inner exception stack trace ---\n   at Example.Shop.Checkout.Run()",
			"System.TimeoutException", "Reserve"},
		{"dotnet: no chain", "Example.Shop.CheckoutException", `System.Exception: the text names another type
   at Example.Shop.Checkout.Run()`,
			"Example.Shop.CheckoutException", "Run"},
		{"python: the first exception raised", "RuntimeError", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")
ValueError: locked

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`,
			"ValueError", "add_item"},
		{"python: a qualified type", "RuntimeError", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    self.session.commit()
sqlalchemy.exc.OperationalError: (psycopg2.OperationalError) server closed the connection

The above exception was the direct cause of the following exception:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`,
			"sqlalchemy.exc.OperationalError", "add_item"},
		{"python: the first member of a group", "ExceptionGroup", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     async with asyncio.TaskGroup() as tg:
  | ExceptionGroup: unhandled errors in a TaskGroup (2 sub-exceptions)
  +-+---------------- 1 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/cart.py", line 42, in add_item
    |     raise ValueError("locked")
    | ValueError: locked
    +---------------- 2 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/stock.py", line 9, in reserve
    |     raise KeyError(sku)
    | KeyError: 'kettle'
    +------------------------------------`,
			"ValueError", "add_item"},
		{"python: no chain", "RuntimeError", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")
ValueError: the text names another type`,
			"RuntimeError", "add_item"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := mustFingerprint(t, Event{Type: " " + tc.eventType + " ", Message: "checkout failed", StackTrace: tc.stack})
			if fp.Type != tc.wantType || fp.EventType != tc.eventType {
				t.Errorf("type %q of event type %q, want %q of %q", fp.Type, fp.EventType, tc.wantType, tc.eventType)
			}
			if len(fp.Frames) == 0 || fp.Frames[0].Function != tc.wantTop {
				t.Errorf("frames %+v, want %s on top", fp.Frames, tc.wantTop)
			}
			// The key is the one of the root cause recorded on its own.
			alone := mustFingerprint(t, Event{Type: tc.wantType, StackTrace: stackOf(fp.Frames)})
			if tc.wantType != tc.eventType && alone.Key != fp.Key {
				t.Errorf("key %s, want that of the root cause alone, %s (%+v)", fp.Key, alone.Key, alone.Frames)
			}
		})
	}
	// Below the frame rungs there is no root cause to read: the type is the
	// event's, like the message.
	fp := mustFingerprint(t, Event{Type: "A", Message: "m", StackTrace: "Caused by: B: no frames anywhere"})
	if fp.Rung != RungMessage || fp.Type != "A" || fp.EventType != "A" {
		t.Errorf("rung %v type %q event type %q", fp.Rung, fp.Type, fp.EventType)
	}
}

// stackOf writes frames back as a trace in their notation, one exception.
func stackOf(frames []StackFrame) string {
	var b strings.Builder
	for i := len(frames) - 1; i >= 0 && frames[0].Syntax == StackPython; i-- {
		fmt.Fprintf(&b, "  File \"/app/%s\", line 1, in %s\n    pass\n", frames[i].Module, frames[i].Function)
	}
	for _, f := range frames {
		switch f.Syntax {
		case StackJava:
			fmt.Fprintf(&b, "\tat %s.%s(File.java:1)\n", f.Module, f.Function)
		case StackDotNet:
			fmt.Fprintf(&b, "   at %s.%s()\n", f.Module, f.Function)
		}
	}
	return b.String()
}

// The throw site is in every key that has frames: two exceptions thrown from
// different places are different issues even where the application frames
// under them are the same.
func TestFingerprintIncludesTheThrowSite(t *testing.T) {
	under := []string{"com.example.shop.web.ErrorPages.render", "com.example.shop.web.AuthFilter.doFilter"}
	sendError := mustFingerprint(t, Event{Type: "T",
		StackTrace: javaStack(append([]string{"org.apache.catalina.connector.ResponseFacade.sendError"}, under...)...)})
	redirect := mustFingerprint(t, Event{Type: "T",
		StackTrace: javaStack(append([]string{"org.apache.catalina.connector.Response.sendRedirect"}, under...)...)})
	if sendError.Key == redirect.Key {
		t.Errorf("two throw sites share key %s: %+v", sendError.Key, sendError.Frames)
	}
	want := frames(StackJava, "org.apache.catalina.connector.ResponseFacade", "sendError", "com.example.shop.web.ErrorPages", "render")
	if sendError.Rung != RungAppFrames || !reflect.DeepEqual(sendError.Frames, want) {
		t.Errorf("rung %v frames %+v", sendError.Rung, sendError.Frames)
	}
	// An in-app throw site is not listed twice.
	own := mustFingerprint(t, Event{Type: "T", StackTrace: javaStack(under...)})
	if want := frames(StackJava, "com.example.shop.web.ErrorPages", "render"); !reflect.DeepEqual(own.Frames, want) {
		t.Errorf("frames %+v", own.Frames)
	}
	// A pass-through frame that throws is the throw site all the same.
	filter := mustFingerprint(t, Event{Type: "T",
		StackTrace: javaStack("com.example.shop.web.AuthFilter.doFilter", "com.example.shop.web.Api.get")})
	want = frames(StackJava, "com.example.shop.web.AuthFilter", "doFilter", "com.example.shop.web.Api", "get")
	if filter.Rung != RungAppFrames || !reflect.DeepEqual(filter.Frames, want) {
		t.Errorf("rung %v frames %+v", filter.Rung, filter.Frames)
	}
	// It does not count toward the top frames, though: five more follow it.
	filter = mustFingerprint(t, Event{Type: "T", StackTrace: javaStack("com.example.shop.web.AuthFilter.doFilter",
		"com.example.A.a", "com.example.B.b", "com.example.C.c", "com.example.D.d", "com.example.E.e", "com.example.F.f")})
	want = frames(StackJava, "com.example.shop.web.AuthFilter", "doFilter",
		"com.example.A", "a", "com.example.B", "b", "com.example.C", "c", "com.example.D", "d", "com.example.E", "e")
	if filter.Rung != RungAppFrames || !reflect.DeepEqual(filter.Frames, want) {
		t.Errorf("rung %v frames %+v", filter.Rung, filter.Frames)
	}
}

// Frames that only hand a call on (a filter or middleware chain, reflection,
// async plumbing) say nothing about the bug next to the application's other
// frames, so they do not count toward the top in-app frames. Where the key
// is made of all frames, they are frames like any other.
func TestFingerprintSkipsPassThroughFrames(t *testing.T) {
	cases := []struct {
		name, stack string
		rung        FingerprintRung
		want        []StackFrame
	}{
		{"java: application filters around a controller", javaStack(
			"com.example.shop.cart.CartService.addItem",
			"com.example.shop.cart.CartController.post",
			"com.example.shop.web.TracingFilter.doFilter",
			"com.example.shop.web.AuthFilter.doFilterInternal",
			"com.example.shop.web.RetryInterceptor.intercept",
			"com.example.shop.web.Aspect.proceed",
			"com.example.shop.Main.main"), RungAppFrames,
			frames(StackJava, "com.example.shop.cart.CartService", "addItem",
				"com.example.shop.cart.CartController", "post", "com.example.shop.Main", "main")},
		{"java: only filters are the application's", javaStack(
			"org.apache.catalina.connector.ResponseFacade.sendError",
			"org.apache.catalina.servlets.DefaultServlet.doGet",
			"org.apache.catalina.core.ApplicationFilterChain.doFilter",
			"com.example.shop.web.TracingFilter.doFilter",
			"org.apache.catalina.core.ApplicationFilterChain.doFilter",
			"com.example.shop.web.AuthFilter.doFilter",
			"java.lang.reflect.Method.invoke",
			"java.lang.Thread.run"), RungFrames,
			frames(StackJava, "org.apache.catalina.connector.ResponseFacade", "sendError",
				"org.apache.catalina.servlets.DefaultServlet", "doGet",
				"org.apache.catalina.core.ApplicationFilterChain", "doFilter",
				"com.example.shop.web.TracingFilter", "doFilter",
				"org.apache.catalina.core.ApplicationFilterChain", "doFilter")},
		{"dotnet: middleware", `   at Example.Shop.CartService.AddItem(String sku)
   at Example.Shop.TracingMiddleware.InvokeAsync(HttpContext context)
   at Example.Shop.AuthMiddleware.InvokeAsync(HttpContext context)
   at Example.Shop.Program.Main(String[] args)`, RungAppFrames,
			frames(StackDotNet, "Example.Shop.CartService", "AddItem", "Example.Shop.Program", "Main")},
		{"javascript: middleware and compiled async plumbing", `    at CartService.addItem (/app/src/cart/service.js:42:17)
    at /app/src/cart/service.js:30:71
    at Object.next (/app/src/cart/service.js:14:53)
    at fulfilled (/app/src/cart/service.js:5:58)
    at __awaiter (/app/src/cart/service.js:4:12)
    at next (/app/src/web/chain.js:9:5)
    at CartController.post (/app/src/cart/controller.js:17:5)`, RungAppFrames,
			frames(StackJS, "src/cart/service.js", "CartService.addItem", "src/cart/service.js", "<anonymous>",
				"src/cart/controller.js", "CartController.post")},
		{"python: middleware and decorators", `Traceback (most recent call last):
  File "/app/shop/web.py", line 9, in wrapped
    return self.app(environ, start_response)
  File "/app/shop/views.py", line 12, in inner
    return handler(request)
  File "/app/shop/auth.py", line 30, in wrapper
    return view(request)
  File "/app/shop/views.py", line 40, in post
    cart.add_item(sku)
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")`, RungAppFrames,
			frames(StackPython, "shop/cart.py", "add_item", "shop/views.py", "post")},
		{"go: handlers that wrap handlers", `panic: cart locked
shop/internal/cart.(*Service).AddItem(0xc000010000)
shop/internal/api.(*Handler).Post(0xc000020000)
shop/internal/web.(*Auth).ServeHTTP(0xc000020000)
shop/internal/web.Tracing.ServeHTTP(0xc000020000)
main.main()`, RungAppFrames,
			frames(StackGo, "shop/internal/cart", "Service.AddItem", "shop/internal/api", "Handler.Post", "main", "main")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := mustFingerprint(t, Event{Type: "T", StackTrace: tc.stack})
			if fp.Rung != tc.rung || !reflect.DeepEqual(fp.Frames, tc.want) {
				t.Errorf("rung %v frames %+v\nwant %v %+v", fp.Rung, fp.Frames, tc.rung, tc.want)
			}
		})
	}
}

// Where the application's only frame under a runtime throw site is a
// pass-through one, that frame is what tells two handlers, filters or
// middlewares apart: the key is made of all frames and must hold it.
func TestFingerprintKeepsPassThroughFramesOnTheAllFramesRung(t *testing.T) {
	goHandler := func(handler string) string {
		return "goroutine 34 [running]:\npanic({0x6a1e20?, 0x7d5c10?})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" +
			"encoding/json.(*Decoder).Decode(0xc000128000, {0x69c2a0, 0xc00011e018})\n\t/usr/local/go/src/encoding/json/stream.go:73 +0x179\n" +
			"shop/internal/web.(*" + handler + ").ServeHTTP(0xc000010000, {0x7f1a40, 0xc00019c000}, 0xc00018e000)\n" +
			"\t/src/shop/internal/web/handlers.go:31 +0x8c\n" +
			"net/http.serverHandler.ServeHTTP({0xc000122000?}, {0x7f1a40, 0xc00019c000}, 0xc00018e000)\n" +
			"\t/usr/local/go/src/net/http/server.go:3137 +0x8e\n"
	}
	javaFilter := func(filter string) string {
		return javaStack("org.apache.catalina.connector.ResponseFacade.sendError",
			"com.example.shop.web."+filter+".doFilter",
			"org.apache.catalina.core.ApplicationFilterChain.doFilter",
			"java.lang.Thread.run")
	}
	pythonMiddleware := func(file string) string {
		return "Traceback (most recent call last):\n" +
			"  File \"/usr/lib/python3.11/wsgiref/handlers.py\", line 137, in run\n    self.result = application(self.environ, self.start_response)\n" +
			"  File \"/app/shop/" + file + "\", line 9, in wrapper\n    return json.loads(body)\n" +
			"  File \"/usr/lib/python3.11/json/decoder.py\", line 355, in raw_decode\n    raise JSONDecodeError(\"Expecting value\", s, err.value) from None\n"
	}
	dotnetMiddleware := func(middleware string) string {
		return "   at System.Text.Json.JsonSerializer.Deserialize(String json)\n" +
			"   at Example.Shop." + middleware + ".InvokeAsync(HttpContext context)\n" +
			"   at Microsoft.AspNetCore.Server.Kestrel.Core.Internal.Http.HttpProtocol.ProcessRequests(IHttpApplication application)\n"
	}
	for name, pair := range map[string][2]string{
		"go":     {goHandler("CartHandler"), goHandler("CheckoutHandler")},
		"java":   {javaFilter("AuthFilter"), javaFilter("RateLimitFilter")},
		"python": {pythonMiddleware("charge.py"), pythonMiddleware("refund.py")},
		"dotnet": {dotnetMiddleware("AuthMiddleware"), dotnetMiddleware("QuotaMiddleware")},
	} {
		a := mustFingerprint(t, Event{Type: "T", StackTrace: pair[0]})
		b := mustFingerprint(t, Event{Type: "T", StackTrace: pair[1]})
		if a.Rung != RungFrames || b.Rung != RungFrames || len(a.Frames) < 2 || !isPassThrough(a.Frames[1]) {
			t.Errorf("%s: rungs %v and %v, frames %+v; want the pass-through frame on the all-frames rung", name, a.Rung, b.Rung, a.Frames)
		}
		if a.Key == b.Key {
			t.Errorf("%s: two pass-through frames share key %s: %+v and %+v", name, a.Key, a.Frames, b.Frames)
		}
	}
	// The limit is the same on both rungs: five frames, the throw site
	// among them where it counts.
	fp := mustFingerprint(t, Event{Type: "T", StackTrace: javaStack("java.util.A.a", "java.util.B.doFilter",
		"java.util.C.invoke", "java.util.D.d", "java.util.E.proceed", "java.util.F.f")})
	want := frames(StackJava, "java.util.A", "a", "java.util.B", "doFilter", "java.util.C", "invoke", "java.util.D", "d", "java.util.E", "proceed")
	if fp.Rung != RungFrames || !reflect.DeepEqual(fp.Frames, want) {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

// A name an application gives its own entry point is not plumbing by that
// name alone: a command's invoke, a state machine's step, a view's dispatch
// and a callable's __call__ are where the work is done as often as where it
// is handed on. They count toward the key like any frame.
func TestEntryPointNamesCountTowardTheKey(t *testing.T) {
	cases := []struct {
		name string
		pair [2]string
		want StackFrame
	}{
		{"java invoke", [2]string{
			javaStack("com.example.shop.Repo.save", "com.example.shop.AddItem.invoke", "com.example.shop.Main.main"),
			javaStack("com.example.shop.Repo.save", "com.example.shop.RemoveItem.invoke", "com.example.shop.Main.main")},
			StackFrame{Module: "com.example.shop.AddItem", Function: "invoke", Syntax: StackJava}},
		{"dotnet Invoke", [2]string{
			"   at Example.Shop.Repo.Save(Cart cart)\n   at Example.Shop.AddItem.Invoke(Request request)\n   at Example.Shop.Program.Main(String[] args)",
			"   at Example.Shop.Repo.Save(Cart cart)\n   at Example.Shop.RemoveItem.Invoke(Request request)\n   at Example.Shop.Program.Main(String[] args)"},
			StackFrame{Module: "Example.Shop.AddItem", Function: "Invoke", Syntax: StackDotNet}},
		{"javascript step", [2]string{
			"    at save (/app/src/repo.js:4:11)\n    at step (/app/src/checkout/machine.js:33:23)\n    at run (/app/src/main.js:7:3)",
			"    at save (/app/src/repo.js:4:11)\n    at step (/app/src/refund/machine.js:33:23)\n    at run (/app/src/main.js:7:3)"},
			StackFrame{Module: "src/checkout/machine.js", Function: "step", Syntax: StackJS}},
		{"python dispatch and __call__", [2]string{
			"Traceback (most recent call last):\n  File \"/app/shop/main.py\", line 7, in run\n  File \"/app/shop/checkout.py\", line 12, in __call__\n" +
				"  File \"/app/shop/repo.py\", line 4, in save\nValueError: locked",
			"Traceback (most recent call last):\n  File \"/app/shop/main.py\", line 7, in run\n  File \"/app/shop/refund.py\", line 12, in dispatch\n" +
				"  File \"/app/shop/repo.py\", line 4, in save\nValueError: locked"},
			StackFrame{Module: "shop/checkout.py", Function: "__call__", Syntax: StackPython}},
	}
	for _, tc := range cases {
		a := mustFingerprint(t, Event{Type: "T", StackTrace: tc.pair[0]})
		b := mustFingerprint(t, Event{Type: "T", StackTrace: tc.pair[1]})
		if a.Rung != RungAppFrames || len(a.Frames) != 3 || a.Frames[1] != tc.want {
			t.Errorf("%s: rung %v, frames %+v, want %+v second", tc.name, a.Rung, a.Frames, tc.want)
		}
		if a.Key == b.Key {
			t.Errorf("%s: two entry points share the key %s", tc.name, a.Key)
		}
	}
}

// A Go error has no stack of its own: a trace recorded with one
// (debug.Stack in a middleware) says where the error was seen, not where it
// arose, and every error that passes there has the same. Only a panic's
// trace starts at the fault. So without a panic entry the key is the type
// and the message.
func TestGoTraceWithoutPanicIsKeyedOnTheMessage(t *testing.T) {
	const recorded = `goroutine 34 [running]:
runtime/debug.Stack()
	/usr/local/go/src/runtime/debug/stack.go:26 +0x5e
shop/internal/api.(*Recorder).Record(0xc000012345, {0x7b2a40, 0xc0000a2000})
	/app/internal/api/recorder.go:31 +0x45
shop/internal/api.(*Handler).ServeHTTP(0xc000012345, {0x7b2a40, 0xc0000a2000}, 0xc0000b4000)
	/app/internal/api/handler.go:58 +0x1a5
net/http.serverHandler.ServeHTTP({0xc0000a2000?}, {0x7b2a40, 0xc0000a2000}, 0xc0000b4000)
	/usr/local/go/src/net/http/server.go:3137 +0x8e
`
	seen := func(msg string) ExceptionFingerprint {
		return mustFingerprint(t, Event{Type: "*errors.errorString", Message: msg, StackTrace: recorded})
	}
	locked, lockedAgain, missing := seen("cart 7 is locked"), seen("cart 9 is locked"), seen("sku kettle not found")
	if locked.Rung != RungMessage || locked.Frames != nil || locked.Message != "cart <n> is locked" {
		t.Errorf("rung %v, frames %+v, message %q", locked.Rung, locked.Frames, locked.Message)
	}
	if locked.Key != lockedAgain.Key {
		t.Errorf("one error with two values: %s, %s", locked.Key, lockedAgain.Key)
	}
	if locked.Key == missing.Key {
		t.Errorf("two errors seen at one place share the key %s", locked.Key)
	}
	if bare := mustFingerprint(t, Event{Type: "*errors.errorString", Message: "cart 7 is locked"}); bare.Key != locked.Key {
		t.Errorf("the same error without a trace: %s, with one %s", bare.Key, locked.Key)
	}
	if got := mustFingerprint(t, Event{Type: "*errors.errorString", StackTrace: recorded}); got.Rung != RungType {
		t.Errorf("no message: rung %v, want the type alone", got.Rung)
	}

	// A panic is keyed on its frames, however its entry is printed: the
	// runtime's frames in a recovered trace, the header of a crash.
	const fault = `shop/internal/cart.(*Service).AddItem(0xc000012345, {0xc00001a0c0, 0x6})
	/app/internal/cart/service.go:42 +0x1d
shop/internal/api.(*Handler).PostItem(0xc000012345, {0x7b2a40, 0xc0000a2000}, 0xc0000b4000)
	/app/internal/api/handler.go:17 +0x25
`
	want := frames(StackGo, "shop/internal/cart", "Service.AddItem", "shop/internal/api", "Handler.PostItem")
	for name, stack := range map[string]string{
		"recovered":     "goroutine 34 [running]:\npanic({0x6a1e20?, 0x7d5c10?})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" + fault,
		"a crash":       "panic: cart locked\n\ngoroutine 34 [running]:\n" + fault,
		"a fatal error": "fatal error: concurrent map writes\n\ngoroutine 34 [running]:\n" + fault,
		"a signal": "panic: runtime error: invalid memory address or nil pointer dereference\n" +
			"[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x48a2bd]\n\ngoroutine 34 [running]:\n" + fault,
	} {
		got := mustFingerprint(t, Event{Type: "panic", Message: "cart 7 is locked", StackTrace: stack})
		if got.Rung != RungAppFrames || !reflect.DeepEqual(got.Frames, want) {
			t.Errorf("%s: rung %v, frames %+v", name, got.Rung, got.Frames)
		}
	}

	// Other runtimes throw where the fault is: their frames stay the key.
	java := mustFingerprint(t, Event{Type: "java.lang.IllegalStateException", Message: "Cart 7 is locked", StackTrace: cartStack})
	if java.Rung != RungAppFrames {
		t.Errorf("java: rung %v", java.Rung)
	}
}

// Whether a trace is a panic's is read from the goroutine its frames come
// from. A reporter that dumps every goroutine (runtime.Stack(buf, true))
// prints the others too, and one of them may be in the deferred call of a
// panic of its own. The dumps are a Go 1.26 program's.
func TestGoPanicOfAnotherGoroutineIsNotTheErrors(t *testing.T) {
	const alone = `goroutine 1 [running]:
main.report({0x27ff77b1af28?, 0x1043c1a40?})
	shop/main.go:11 +0x40
main.main()
	shop/main.go:27 +0x3c
`
	const beside = `goroutine 1 [running]:
main.report({0x1044f79c8?, 0x0?})
	shop/main.go:11 +0x40
main.main()
	shop/main.go:32 +0xfc

goroutine 19 [chan receive]:
main.worker.func1()
	shop/main.go:19 +0x3c
panic({0x1044dfd00?, 0x1045011c0?})
	runtime/panic.go:860 +0x12c
main.worker(0x0?, 0x0?)
	shop/main.go:22 +0x5c
created by main.main in goroutine 1
	shop/main.go:30 +0xe0
`
	seen := func(stack string) ExceptionFingerprint {
		return mustFingerprint(t, Event{Type: "*errors.errorString", Message: "cart 7 is locked", StackTrace: stack})
	}
	a, b := seen(alone), seen(beside)
	if a.Rung != RungMessage || b.Rung != RungMessage || a.Key != b.Key {
		t.Errorf("alone %s on rung %v, beside a panicking goroutine %s on rung %v (%+v)", a.Key, a.Rung, b.Key, b.Rung, b.Frames)
	}
	// The first goroutine's own panic still counts, with or without others.
	const own = `goroutine 19 [running]:
main.worker.func1()
	shop/main.go:19 +0x3c
panic({0x1044dfd00?, 0x1045011c0?})
	runtime/panic.go:860 +0x12c
main.worker(0x0?, 0x0?)
	shop/main.go:22 +0x5c

` + alone
	if got := seen(own); got.Rung != RungAppFrames || !reflect.DeepEqual(got.Frames, frames(StackGo, "main", "worker")) {
		t.Errorf("its own panic: rung %v, frames %+v", got.Rung, got.Frames)
	}
	// A crash header stands above the first goroutine, not somewhere in
	// the lines of another.
	const later = alone + "\ngoroutine 19 [running]:\npanic: not this goroutine's\nmain.worker(0x0?, 0x0?)\n\tshop/main.go:22 +0x5c\n"
	if got := seen(later); got.Rung != RungMessage {
		t.Errorf("a panic line of another goroutine: rung %v, frames %+v", got.Rung, got.Frames)
	}
	headerless := strings.TrimPrefix(alone, "goroutine 1 [running]:\n") + "panic: logged by something else\n"
	if got := seen(headerless); got.Rung != RungMessage {
		t.Errorf("a panic line below the frames: rung %v, frames %+v", got.Rung, got.Frames)
	}
}

// The value of a panic may take several lines, and one of them may read
// like a call. They end at the first empty line, and none is a frame.
func TestGoPanicValueLinesAreNoFrames(t *testing.T) {
	want := frames(StackGo, "main", "real", "main", "main")
	for name, stack := range map[string]string{
		"a call in the value":            "panic: bad state:\n\tshop.fake()\n\ngoroutine 1 [running]:\nmain.real(0x1)\n\t/app/main.go:9 +0x1d\nmain.main()\n\t/app/main.go:5 +0x25\n",
		"a call with arguments":          "panic: bad state:\n\tshop.fake(0x1, 0x2)\n\ngoroutine 1 [running]:\nmain.real(0x1)\n\t/app/main.go:9 +0x1d\nmain.main()\n\t/app/main.go:5 +0x25\n",
		"a fatal error":                  "fatal error: bad state:\nshop.fake(0x1)\n\ngoroutine 1 [running]:\nmain.real(0x1)\nmain.main()\n",
		"no header below the value":      "panic: bad state:\n\tshop.fake(0x1)\n\nmain.real(0x1)\n\t/app/main.go:9 +0x1d\nmain.main()\n\t/app/main.go:5 +0x25\n",
		"a signal line and no header":    "panic: runtime error: index out of range\n[signal SIGSEGV: segmentation violation]\n\nmain.real(0x1)\nmain.main()\n",
		"no empty line above the header": "panic: bad state:\n\tshop.fake(0x1)\ngoroutine 1 [running]:\nmain.real(0x1)\nmain.main()\n",
		"no line of a value":             "panic: bad state\nmain.real(0x1)\nmain.main()\n",
		"lines above the crash header":   "2026/11/26 10:11:12 shutting down\npanic: bad state:\n\tshop.fake(0x1)\n\ngoroutine 1 [running]:\nmain.real(0x1)\nmain.main()\n",
	} {
		got := mustFingerprint(t, Event{Type: "panic", Message: "bad state", StackTrace: stack})
		if got.Rung != RungAppFrames || !reflect.DeepEqual(got.Frames, want) {
			t.Errorf("%s: rung %v, frames %+v, want %+v", name, got.Rung, got.Frames, want)
		}
	}
}

// An event that says nothing about an exception (no type, no message, no
// stack) is not one to key on: it would give every such span one key, and
// as the last event hide the real one before it.
func TestFingerprintSkipsEventsThatSayNothing(t *testing.T) {
	real := Event{Type: "java.lang.IllegalStateException", Message: "Cart 7 is locked",
		StackTrace: "java.lang.IllegalStateException: Cart 7 is locked\n\tat com.example.shop.Cart.add(Cart.java:42)\n"}
	want := mustFingerprint(t, real)
	for name, excs := range map[string][]Event{
		"an empty event after it":         {real, {}},
		"an event of white space after":   {real, {Type: " ", Message: "\n", StackTrace: "\t"}},
		"an empty event flagged as root":  {real, {Root: true}},
		"an empty event before and after": {{}, real, {}},
	} {
		if got, ok := FingerprintExceptions(excs); !ok || got.Key != want.Key || got.Type != want.Type {
			t.Errorf("%s: key %s (%s) %v, want %s", name, got.Key, got.Type, ok, want.Key)
		}
	}
	for name, excs := range map[string][]Event{
		"one empty event":   {{}},
		"only empty events": {{}, {Root: true}, {Type: "  "}},
	} {
		if got, ok := FingerprintExceptions(excs); ok {
			t.Errorf("%s: got the key %s on rung %s", name, got.Key, got.Rung)
		}
	}
	// One of the three is enough.
	for _, ex := range []Event{{Type: "A"}, {Message: "locked"}, {StackTrace: "\tat com.example.shop.Cart.add(Cart.java:42)\n"}} {
		if _, ok := FingerprintExceptions([]Event{ex, {}}); !ok {
			t.Errorf("%+v has no key", ex)
		}
	}
}

// What an event gives must be something a reader can use: a type, a message
// that is not empty once parameterised, or a frame. An event with none of
// them would get the key of the empty type, the same for every such event
// of every service, and as the last event of a span stand in for the real
// one before it.
func TestFingerprintSkipsEventsNoReaderCanUse(t *testing.T) {
	goTrace := "goroutine 1 [running]:\nmain.process(0xc000012345)\n\t/app/main.go:42 +0x1d\nmain.main()\n\t/app/main.go:12 +0x25\n"
	useless := map[string]Event{
		"stack text no reader can use":     {StackTrace: "something unreadable"},
		"a Go stack that is not a panic's": {StackTrace: goTrace},
		"a message of colour codes":        {Message: "\x1b[31m\x1b[0m"},
		"a message of a NUL":               {Message: "\x00"},
		"a message of invalid UTF-8":       {Message: "\xff\xfe"},
		"a message past its limit":         {Message: strings.Repeat(" ", exceptionMessageInput) + "real error"},
		"a message of 5000 spaces":         {Message: strings.Repeat(" ", 5000) + "real error"},
		"all three, none usable":           {Type: " ", Message: "\x1b[0m", StackTrace: "at least nothing"},
		"a message of spaces beyond ASCII": {Message: "\u00a0\u2003"},
	}
	real := Event{Type: "java.lang.NullPointerException", Message: "x is null"}
	want := mustFingerprint(t, real)
	for name, ex := range useless {
		if got, ok := FingerprintExceptions([]Event{ex}); ok {
			t.Errorf("%s: got the key %s on rung %s", name, got.Key, got.Rung)
		}
		for _, flagged := range []bool{false, true} {
			ex.Root = flagged
			if got, ok := FingerprintExceptions([]Event{real, ex}); !ok || got.Key != want.Key {
				t.Errorf("%s (flagged %v) after a typed event: key %s %v, want %s", name, flagged, got.Key, ok, want.Key)
			}
		}
	}
	// The same events with one thing a reader can use.
	for name, ex := range map[string]Event{
		"a type":                       {Type: "E", StackTrace: "something unreadable"},
		"a message beside the stack":   {Message: "locked", StackTrace: goTrace},
		"a message behind colour":      {Message: "\x1b[31mlocked\x1b[0m"},
		"a message inside its limit":   {Message: strings.Repeat(" ", exceptionMessageInput-1) + "real error"},
		"a frame":                      {StackTrace: "\tat com.example.shop.Cart.add(Cart.java:42)\n"},
		"a Go panic's stack":           {StackTrace: "panic: bad state\n\n" + goTrace},
		"a message that is all values": {Message: "42"},
	} {
		got, ok := FingerprintExceptions([]Event{real, ex})
		if !ok || got.Key == want.Key {
			t.Errorf("%s: key %s %v, want one of its own", name, got.Key, ok)
		}
	}
	// A Go stack that is not a panic's is keyed as if the event had none.
	a := mustFingerprint(t, Event{Type: "E", Message: "locked", StackTrace: goTrace})
	if b := mustFingerprint(t, Event{Type: "E", Message: "locked"}); a.Key != b.Key || a.Rung != RungMessage {
		t.Errorf("with a message: %s on %s, want %s", a.Key, a.Rung, b.Key)
	}
	a = mustFingerprint(t, Event{Type: "E", StackTrace: goTrace})
	if b := mustFingerprint(t, Event{Type: "E"}); a.Key != b.Key || a.Rung != RungType {
		t.Errorf("with a type alone: %s on %s, want %s", a.Key, a.Rung, b.Key)
	}
}

// A wrapper the compiler generated for a dependency's method is the method,
// also where other frames stand between the two: it is in a key once, and
// never as the application's.
func TestTopFramesMergeADependencyWithItsWrapper(t *testing.T) {
	stack := "goroutine 1 [running]:\npanic({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" +
		"shop.example.invalid/db.(*Conn).Do(0x1)\n\t/go/pkg/mod/shop.example.invalid/db@v1.2.0/conn.go:10 +0x1d\n" +
		"reflect.Value.call({0x1})\n\t/usr/local/go/src/reflect/value.go:581 +0xca\n" +
		"shop.example.invalid/db.(*Conn).Do(0x1)\n\t<autogenerated>:1 +0x1d\n" +
		"main.main()\n\t/app/main.go:12 +0x25\n"
	got := mustFingerprint(t, Event{Type: "panic", StackTrace: stack})
	want := []StackFrame{{Module: "shop.example.invalid/db", Function: "Conn.Do", Syntax: StackGo, Dependency: true},
		{Module: "main", Function: "main", Syntax: StackGo}}
	if got.Rung != RungAppFrames || !reflect.DeepEqual(got.Frames, want) {
		t.Errorf("rung %v, frames %+v, want %+v", got.Rung, got.Frames, want)
	}
	// A recursion through frames that do not count is one frame as well.
	all := frames(StackJava, "com.example.shop.Cart", "add", "java.util.ArrayList", "forEach", "com.example.shop.Cart", "add", "com.example.shop.Api", "post")
	if got := topFrames(all, frameInApp, isPassThrough); len(got) != 2 {
		t.Errorf("a recursion: %+v", got)
	}
	// Two notations that spell a name alike do not name the same code.
	two := []StackFrame{{Module: "shop", Function: "add", Syntax: StackJava}, {Module: "shop", Function: "add", Syntax: StackDotNet}}
	if got := topFrames(two, anyFrame, noFrame); len(got) != 2 {
		t.Errorf("one name in two notations: %+v", got)
	}
}

// The throw site counts as the application's also where it only hands the
// call on: with no other frame of the application the key is still on the
// app-frames rung, and holds that frame alone.
func TestPassThroughThrowSiteAloneIsTheApplications(t *testing.T) {
	got := mustFingerprint(t, Event{Type: "java.lang.IllegalStateException", StackTrace: "java.lang.IllegalStateException: x\n" +
		"\tat com.example.shop.AuthFilter.doFilter(AuthFilter.java:9)\n" +
		"\tat org.apache.catalina.core.ApplicationFilterChain.doFilter(ApplicationFilterChain.java:178)\n" +
		"\tat org.apache.catalina.core.StandardWrapperValve.invoke(StandardWrapperValve.java:167)\n"})
	if want := frames(StackJava, "com.example.shop.AuthFilter", "doFilter"); got.Rung != RungAppFrames || !reflect.DeepEqual(got.Frames, want) {
		t.Errorf("rung %v, frames %+v, want %+v on the app-frames rung", got.Rung, got.Frames, want)
	}
}

// The type the stack text gives belongs to the frames of its root cause.
// Where no frame could be read the key is the event's: its type, with its
// message.
func TestInnerTypeWithoutAFrameIsNotTheKeys(t *testing.T) {
	got := mustFingerprint(t, Event{Type: "System.Exception", Message: "outer", StackTrace: "System.Exception: outer\n" +
		" ---> MyApp.InnerException: locked\n   at MyApp.Cart.()\n"})
	if got.Type != "System.Exception" || got.Rung != RungMessage || got.Frames != nil {
		t.Errorf("type %q on rung %v with frames %+v, want the event's type on the message rung", got.Type, got.Rung, got.Frames)
	}
	if _, typ, _ := parseStack("System.Exception: outer\n ---> MyApp.InnerException: locked\n   at MyApp.Cart.()\n"); typ != "MyApp.InnerException" {
		t.Errorf("the stack text gives the type %q", typ)
	}
}

func TestRootException(t *testing.T) {
	cases := []struct {
		name string
		excs []Event
		want string
	}{
		{"a single event", []Event{{Type: "A"}}, "A"},
		{"the last event without a chain", []Event{{Type: "A"}, {Type: "B"}, {Type: "C"}}, "C"},
		{"the flagged root", []Event{{Type: "A"}, {Type: "B", Root: true}, {Type: "C"}}, "B"},
		{"several flagged roots: the last", []Event{
			{Type: "A", Root: true}, {Type: "B"}, {Type: "C", Root: true}, {Type: "D"}}, "C"},
		{"every event flagged: the last", []Event{
			{Type: "A", Root: true}, {Type: "B", Root: true}}, "B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := rootException(tc.excs); !ok || got.typ != tc.want {
				t.Errorf("root = %s, want %s", got.typ, tc.want)
			}
		})
	}
}

// --- corpus -------------------------------------------------------------------

const exceptionCorpusDir = "testdata/exceptions"

// corpusCase is one span's exception events and the group it must land in:
// cases that share a group must share a key, and no two groups may.
type corpusCase struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	Rung   string `json:"rung"`
	Events []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Stack   string `json:"stack"` // file in the corpus directory
		Root    bool   `json:"root"`
	} `json:"events"`
}

func loadExceptionCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(exceptionCorpusDir, "corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func (c corpusCase) fingerprint(t *testing.T) ExceptionFingerprint {
	t.Helper()
	var excs []Event
	for _, ev := range c.Events {
		ex := Event{Type: ev.Type, Message: ev.Message, Root: ev.Root}
		if ev.Stack != "" {
			stack, err := os.ReadFile(filepath.Join(exceptionCorpusDir, ev.Stack))
			if err != nil {
				t.Fatal(err)
			}
			ex.StackTrace = string(stack)
		}
		excs = append(excs, ex)
	}
	return mustFingerprint(t, excs...)
}

func TestExceptionCorpusGroups(t *testing.T) {
	cases := loadExceptionCorpus(t)
	if len(cases) == 0 {
		t.Fatal("empty corpus")
	}
	keyOfGroup := map[string]string{}
	groupOfKey := map[string]string{}
	names := map[string]bool{}
	for _, c := range cases {
		if names[c.Name] {
			t.Errorf("duplicate case name %q", c.Name)
		}
		names[c.Name] = true
		fp := c.fingerprint(t)
		if fp.Rung.String() != c.Rung {
			t.Errorf("%s: rung %q, want %q", c.Name, fp.Rung, c.Rung)
		}
		if key, seen := keyOfGroup[c.Group]; seen && key != fp.Key {
			t.Errorf("%s: key %s splits group %s (%s)", c.Name, fp.Key, c.Group, key)
		}
		if group, seen := groupOfKey[fp.Key]; seen && group != c.Group {
			t.Errorf("%s: key %s merges groups %s and %s", c.Name, fp.Key, c.Group, group)
		}
		keyOfGroup[c.Group] = fp.Key
		groupOfKey[fp.Key] = c.Group
	}
}

// TestExceptionKeysAreStable pins the key of every corpus case. A key that
// moves means exceptions already grouped under it would regroup, so once a
// release has shipped keys of this version the fix is never to rewrite the
// golden line: raise ExceptionKeyVersion, which starts a new golden file,
// and say so in the change (exceptionKeyRepin). -update only adds lines for
// new cases.
func TestExceptionKeysAreStable(t *testing.T) {
	golden := filepath.Join(exceptionCorpusDir, ExceptionKeyVersion+"-keys.golden")
	pinned := readExceptionGolden(t, golden)

	var lines []string
	for _, c := range loadExceptionCorpus(t) {
		key := c.fingerprint(t).Key
		want, ok := pinned[c.Name]
		switch {
		case ok && want != key:
			t.Errorf("%s: key %s, pinned %s — the %s algorithm changed; %s",
				c.Name, key, want, ExceptionKeyVersion, exceptionKeyRepin("keys"))
		case !ok && !*updateExceptionKeys:
			t.Errorf("%s: no pinned key; run with -update to add %s", c.Name, key)
		}
		delete(pinned, c.Name)
		lines = append(lines, key+"\t"+c.Name)
	}
	for name := range pinned {
		t.Errorf("%s is pinned but no longer in the corpus", name)
	}
	if *updateExceptionKeys && !t.Failed() {
		sort.Slice(lines, func(i, j int) bool {
			return strings.SplitN(lines[i], "\t", 2)[1] < strings.SplitN(lines[j], "\t", 2)[1]
		})
		if err := os.WriteFile(golden, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var exceptionKeyRe = regexp.MustCompile(`^` + regexp.QuoteMeta(ExceptionKeyVersion) + `:[0-9a-f]{12}$`)

// FuzzFingerprintExceptions checks what must hold for any event: no panic,
// a key of the pinned shape, the same key on every run, a rung that matches
// what the key was built from, and no more frames than a key may hold.
func FuzzFingerprintExceptions(f *testing.F) {
	f.Add("java.lang.IllegalStateException", "Cart 7 is locked", "\tat com.example.shop.CartService.addItem(CartService.java:42)", false)
	f.Add("T", "", "main.cleanup()\npanic({0x1, 0x2})\nruntime.sigpanic()", true)
	f.Add("", "", "", false)
	f.Add("T", "x", "Caused by: a.B\n... 9 more\nat a.b(C.java:1)\nSuppressed: x", false)
	f.Add("E", "m", "Traceback (most recent call last):\n  File \"/app/a.py\", line 1, in __call__", true)
	f.Fuzz(func(t *testing.T, typ, msg, stack string, root bool) {
		excs := []Event{
			{Type: "Wrapper", Message: msg},
			{Type: typ, Message: msg, StackTrace: stack, Root: root},
			{Type: typ, StackTrace: stack},
		}
		fp, ok := FingerprintExceptions(excs)
		if !ok {
			t.Fatal("no fingerprint for three events")
		}
		if again, _ := FingerprintExceptions(excs); !reflect.DeepEqual(fp, again) {
			t.Fatalf("not deterministic: %+v then %+v", fp, again)
		}
		if !exceptionKeyRe.MatchString(fp.Key) {
			t.Fatalf("key %q", fp.Key)
		}
		switch {
		case fp.Rung >= RungMessage && fp.Frames != nil, fp.Rung < RungMessage && len(fp.Frames) == 0,
			fp.Rung == RungMessage && fp.Message == "", fp.Rung < RungAppFrames, fp.Rung > RungType:
			t.Fatalf("rung %v with frames %+v and message %q", fp.Rung, fp.Frames, fp.Message)
		}
		if len(fp.Frames) > FingerprintFrames+1 {
			t.Fatalf("%d frames in a key", len(fp.Frames))
		}
	})
}
