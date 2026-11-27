package exckey

import (
	"fmt"
	"strings"
	"testing"
)

// benchJavaEvent is a 70-frame exception of a servlet application: a few
// application frames on top of the framework's dispatch and filter chain.
func benchJavaEvent() Event {
	var b strings.Builder
	b.WriteString("java.lang.IllegalStateException: Cart 7 is locked by order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f\n")
	frame := func(name, file string, line int) { fmt.Fprintf(&b, "\tat %s(%s.java:%d)\n", name, file, line) }
	frame("com.example.shop.cart.CartService.addItem", "CartService", 42)
	b.WriteString("\tat com.example.shop.cart.CartService$$FastClassBySpringCGLIB$$1a2b3c4d.invoke(<generated>)\n")
	frame("org.springframework.cglib.proxy.MethodProxy.invoke", "MethodProxy", 218)
	b.WriteString("\tat com.example.shop.cart.CartService$$EnhancerBySpringCGLIB$$5e6f7a8b.addItem(<generated>)\n")
	frame("com.example.shop.cart.CartController.post", "CartController", 17)
	b.WriteString("\tat jdk.internal.reflect.GeneratedMethodAccessor112.invoke(Unknown Source)\n")
	frame("java.base/java.lang.reflect.Method.invoke", "Method", 568)
	for i := range 20 {
		frame(fmt.Sprintf("org.springframework.web.servlet.mvc.method.Handler%d.invoke", i), "Handler", 100+i)
	}
	for i := range 20 {
		frame("org.apache.catalina.core.ApplicationFilterChain.internalDoFilter", "ApplicationFilterChain", 166)
		frame(fmt.Sprintf("org.springframework.web.filter.Filter%d.doFilter", i), "Filter", 200+i)
	}
	frame("org.apache.tomcat.util.net.SocketProcessorBase.run", "SocketProcessorBase", 52)
	frame("java.base/java.util.concurrent.ThreadPoolExecutor.runWorker", "ThreadPoolExecutor", 1136)
	frame("java.base/java.lang.Thread.run", "Thread", 833)
	return Event{
		Type:       "java.lang.IllegalStateException",
		Message:    "Cart 7 is locked by order 3f2b8c1e-9d4a-4b7c-8e21-0a1b2c3d4e5f",
		StackTrace: b.String(),
	}
}

func BenchmarkFingerprintExceptions(b *testing.B) {
	excs := []Event{benchJavaEvent()}
	if n := strings.Count(excs[0].StackTrace, "\n\tat "); n != 70 {
		b.Fatalf("the event has %d frames, want 70", n)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := FingerprintExceptions(excs); !ok {
			b.Fatal("no fingerprint")
		}
	}
}

// A chain of 8,900 causes in which each shares one frame more with the
// exception around it: a megabyte of text, the most a trace is read of.
func BenchmarkParseStackFramesJavaChain(b *testing.B) {
	stack := javaChain(8900)
	if len(stack) < stackTextMax*9/10 || len(stack) > stackTextMax {
		b.Fatalf("the chain is %d bytes, want just under %d", len(stack), stackTextMax)
	}
	b.ReportAllocs()
	for b.Loop() {
		if len(ParseStackFrames(stack)) != 2 {
			b.Fatal("want the root cause's frame and the one all share")
		}
	}
}

// benchFrames is how many frames the traces of the per-notation benchmarks
// have, like benchJavaEvent.
const benchFrames = 70

// benchStack writes a trace of benchFrames frames: a header, then one line
// or two per frame.
func benchStack(header string, frame func(i int) string) string {
	var b strings.Builder
	b.WriteString(header)
	for i := range benchFrames {
		b.WriteString(frame(i))
	}
	return b.String()
}

// One trace per notation, each a few application frames over the
// framework's, and one in no notation at all: text that every notation has
// to look at before the event falls to its message.
func BenchmarkParseStackFrames(b *testing.B) {
	app := func(i int) bool { return i < 3 }
	stacks := []struct {
		name, stack string
		frames      int
	}{
		{"java", benchJavaEvent().StackTrace, benchFrames},
		{"dotnet", benchStack("System.InvalidOperationException: Cart 7 is locked\n", func(i int) string {
			if app(i) {
				return fmt.Sprintf("   at Example.Shop.Cart.CartService.Step%d(String sku, Int32 quantity) in /src/Shop/Cart/CartService.cs:line %d\n", i, 40+i)
			}
			return fmt.Sprintf("   at Microsoft.AspNetCore.Mvc.Infrastructure.Invoker%d.Next(State& next, Scope& scope, Object& state, Boolean& isCompleted)\n", i)
		}), benchFrames},
		{"javascript", benchStack("Error: Cart 7 is locked\n", func(i int) string {
			if app(i) {
				return fmt.Sprintf("    at CartService.step%d (/app/src/cart/service.js:%d:17)\n", i, 40+i)
			}
			return fmt.Sprintf("    at Layer.handle%d [as handle_request] (/app/node_modules/express/lib/router/layer.js:%d:5)\n", i, 90+i)
		}), benchFrames},
		{"python", benchStack("Traceback (most recent call last):\n", func(i int) string {
			if !app(benchFrames - 1 - i) {
				return fmt.Sprintf("  File \"/usr/lib/python3.12/site-packages/django/core/handlers/base.py\", line %d, in step_%d\n"+
					"    response = wrapped_callback(request, *callback_args, **callback_kwargs)\n", 100+i, i)
			}
			return fmt.Sprintf("  File \"/app/shop/cart.py\", line %d, in step_%d\n    self.repo.save(sku)\n", 40+i, i)
		}) + "ValueError: Cart 7 is locked\n", benchFrames},
		{"go", benchStack("goroutine 1 [running]:\n", func(i int) string {
			if app(i) {
				return fmt.Sprintf("shop.example.invalid/cart.(*Service).Step%d(0xc000012345, {0xc00001a0c0, 0x6})\n\t/app/cart/service.go:%d +0x1d\n", i, 40+i)
			}
			return fmt.Sprintf("net/http.(*ServeMux).step%d(0xc000012345, {0x7b2a40, 0xc0000a2000}, 0xc0000b4000)\n\t/usr/local/go/src/net/http/server.go:%d +0x1d\n", i, 2000+i)
		}), benchFrames},
		{"no notation", benchStack("The cart could not be saved.\n", func(i int) string {
			return fmt.Sprintf("step %d of the checkout failed at 10:11:%02d (cart 7, order 12): the order service did not answer\n", i, i%60)
		}), 0},
	}
	for _, s := range stacks {
		b.Run(s.name, func(b *testing.B) {
			if got := len(ParseStackFrames(s.stack)); got != s.frames {
				b.Fatalf("the trace has %d frames, want %d", got, s.frames)
			}
			b.ReportAllocs()
			for b.Loop() {
				ParseStackFrames(s.stack)
			}
		})
	}
}
