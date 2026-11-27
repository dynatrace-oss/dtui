package exckey

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// frames builds the expected result of a parse: module, function pairs.
func frames(syntax StackSyntax, pairs ...string) []StackFrame {
	var out []StackFrame
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, StackFrame{Module: pairs[i], Function: pairs[i+1], Syntax: syntax})
	}
	return out
}

func assertFrames(t *testing.T, stack string, want []StackFrame) {
	t.Helper()
	got := ParseStackFrames(stack)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseStackFramesJava(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"plain frames lose their line numbers", `java.lang.IllegalStateException: Cart 7 is locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	at com.acme.shop.CartController.post(CartController.java:17) ~[shop.jar:1.4.2]
	at java.base/java.lang.Thread.run(Thread.java:833)
	at app//com.acme.shop.Main.main(Main.java:9)
	at com.acme.shop.Native.call(Native Method)`,
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.CartController", "post",
				"java.lang.Thread", "run",
				"com.acme.shop.Main", "main",
				"com.acme.shop.Native", "call")},
		{"frames without at", "com.acme.shop.CartService.addItem(CartService.java:42)\n" +
			"com.acme.shop.CartService.<init> (CartService.java:9)\n" +
			"com.acme.shop.Native.call(Native Method)\n" +
			"com.acme.shop.Gen.run(Unknown Source)\n" +
			"com.acme.shop.Gen.go(<generated>)\n" +
			"com.acme.shop.Kt.run(Cart.kt)",
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.CartService", "<init>",
				"com.acme.shop.Native", "call",
				"com.acme.shop.Gen", "run",
				"com.acme.shop.Gen", "go",
				"com.acme.shop.Kt", "run")},
		{"a OneAgent call stack: no at, a space before the location", "com.example.shop.cart.CartService.addItem (CartService.java:42)\n" +
			"com.example.shop.cart.CartController.post (CartController.java:17)\n" +
			"jdk.internal.reflect.DirectMethodHandleAccessor.invoke (DirectMethodHandleAccessor.java:103)\n" +
			"java.lang.reflect.Method.invoke (Method.java:580)\n" +
			"org.apache.catalina.core.ApplicationFilterChain.doFilter (ApplicationFilterChain.java:129)\n" +
			"java.lang.Thread.run (Thread.java:1583)",
			frames(StackJava,
				"com.example.shop.cart.CartService", "addItem",
				"com.example.shop.cart.CartController", "post",
				"jdk.internal.reflect.DirectMethodHandleAccessor", "invoke",
				"java.lang.reflect.Method", "invoke",
				"org.apache.catalina.core.ApplicationFilterChain", "doFilter",
				"java.lang.Thread", "run")},
		{"prose with parentheses is not a frame", "com.acme.shop.CartService.addItem(sku was empty)\nfailed (see above)", nil},
		{"generated classes collapse", `	at com.acme.shop.CartService$$EnhancerBySpringCGLIB$$1a2b3c4d.addItem(<generated>)
	at com.acme.shop.CartService$$FastClassBySpringCGLIB$$9f8e7d6c.invoke(<generated>)
	at sun.reflect.GeneratedMethodAccessor123.invoke(Unknown Source)
	at jdk.internal.reflect.GeneratedConstructorAccessor7.newInstance(Unknown Source)
	at com.sun.proxy.$Proxy88.addItem(Unknown Source)
	at jdk.proxy3.$Proxy41.addItem(Unknown Source)`,
			frames(StackJava,
				"com.acme.shop.CartService<auto>", "addItem",
				"com.acme.shop.CartService<auto>", "invoke",
				"sun.reflect.<auto>", "invoke",
				"jdk.internal.reflect.<auto>", "newInstance",
				"com.sun.proxy.<auto>", "addItem",
				"jdk.proxy.<auto>", "addItem")},
		{"proxies named at run time collapse", `	at com.example.shop.Cart$HibernateProxy$kL2x9A.getId(Unknown Source)
	at com.example.shop.Cart$HibernateProxy$Zq81mB.getId(Unknown Source)
	at com.example.shop.CartService$ByteBuddy$Ab12Cd.addItem(Unknown Source)
	at com.example.shop.CartService$MockitoMock$1190.addItem(Unknown Source)
	at com.example.shop.CartService$$SpringCGLIB$$0.addItem(<generated>)
	at com.example.shop.CartService$$SpringCGLIB$$FastClass$$1.invoke(<generated>)
	at com.example.shop.CartService$$EnhancerByGuice$$7f3a.addItem(<generated>)
	at com.example.shop.Cart_$$_jvst3c0_1f.getId(Cart_$$_jvst3c0_1f.java)`,
			frames(StackJava,
				"com.example.shop.Cart<auto>", "getId",
				"com.example.shop.CartService<auto>", "addItem",
				"com.example.shop.CartService<auto>", "invoke",
				"com.example.shop.CartService<auto>", "addItem",
				"com.example.shop.Cart<auto>", "getId")},
		{"scala closures unwrap to the method", `	at com.example.shop.Cart$$anonfun$total$1.apply(Cart.scala:30)
	at com.example.shop.Cart$$anonfun$total$1$$anonfun$apply$2.apply$mcV$sp(Cart.scala:31)
	at com.example.shop.Cart.$anonfun$total$3(Cart.scala:30)
	at com.example.shop.Cart.$anonfun$total$3$adapted(Cart.scala:30)
	at com.example.shop.Cart$.$anonfun$apply$1(Cart.scala:9)
	at com.example.shop.Cart$$anon$1.run(Cart.scala:40)
	at com.example.shop.Cart$$inlined$map$1.invoke(Cart.kt:12)`,
			frames(StackJava,
				"com.example.shop.Cart", "total",
				"com.example.shop.Cart$", "apply",
				"com.example.shop.Cart$$anon$1", "run",
				"com.example.shop.Cart$$inlined$map$1", "invoke")},
		{"lambda frames", `	at com.acme.shop.CartService.lambda$addItem$3(CartService.java:51)
	at com.acme.shop.CartService$$Lambda$214/0x0000000800d1a840.accept(Unknown Source)
	at com.acme.shop.CartService$$Lambda/0x0000000800d1a840.accept(Unknown Source)
	at com.acme.shop.CartService.addItem(CartService.java:50)`,
			frames(StackJava, "com.acme.shop.CartService", "addItem")},
		{"the last cause is the root, completed from its parent", `com.acme.shop.CheckoutException: checkout failed
	at com.acme.shop.Checkout.run(Checkout.java:30)
	at com.acme.shop.Api.post(Api.java:12)
	at com.acme.shop.Server.handle(Server.java:80)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	at com.acme.shop.Checkout.run(Checkout.java:28)
	... 2 more`,
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.Checkout", "run",
				"com.acme.shop.Api", "post",
				"com.acme.shop.Server", "handle")},
		{"a chain of two causes", `com.acme.shop.ApiException: failed
	at com.acme.shop.Api.post(Api.java:12)
	at com.acme.shop.Server.handle(Server.java:80)
Caused by: com.acme.shop.CheckoutException: checkout failed
	at com.acme.shop.Checkout.run(Checkout.java:30)
	... 2 common frames omitted
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	... 2 more`,
			frames(StackJava,
				"com.acme.shop.CartService", "addItem",
				"com.acme.shop.Api", "post",
				"com.acme.shop.Server", "handle")},
		{"more than the parent holds takes all of it", `	at com.acme.shop.Api.post(Api.java:12)
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	... 9 more`,
			frames(StackJava, "com.acme.shop.CartService", "addItem", "com.acme.shop.Api", "post")},
		{"a cause without the marker stands alone", `	at com.acme.shop.Api.post(Api.java:12)
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)`,
			frames(StackJava, "com.acme.shop.CartService", "addItem")},
		{"suppressed exceptions are left out", `	at com.acme.shop.Api.post(Api.java:12)
	Suppressed: java.io.IOException: close failed
		at com.acme.shop.Conn.close(Conn.java:5)
		... 1 more
Caused by: java.lang.IllegalStateException: locked
	at com.acme.shop.CartService.addItem(CartService.java:42)
	... 1 more`,
			frames(StackJava, "com.acme.shop.CartService", "addItem", "com.acme.shop.Api", "post")},
		{"a cause inside a suppressed block is not the root", `com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.run(Checkout.java:30)
	at com.example.shop.Api.post(Api.java:12)
	Suppressed: java.io.IOException: close failed
		at com.example.shop.Conn.close(Conn.java:5)
		... 2 more
	Caused by: java.net.SocketException: broken pipe
		at com.example.shop.Socket.write(Socket.java:9)
		... 3 more
		Suppressed: java.io.IOException: flush failed
			at com.example.shop.Socket.flush(Socket.java:14)
	Suppressed: java.io.IOException: unlock failed
		at com.example.shop.Lock.release(Lock.java:21)`,
			frames(StackJava, "com.example.shop.Checkout", "run", "com.example.shop.Api", "post")},
		{"a cause after a suppressed block with a cause of its own", `    com.example.shop.CheckoutException: checkout failed
    	at com.example.shop.Checkout.run(Checkout.java:30)
    	at com.example.shop.Api.post(Api.java:12)
    	Suppressed: java.io.IOException: close failed
    		at com.example.shop.Conn.close(Conn.java:5)
    	Caused by: java.net.SocketException: broken pipe
    		at com.example.shop.Socket.write(Socket.java:9)
    Caused by: java.lang.IllegalStateException: locked
    	at com.example.shop.CartService.addItem(CartService.java:42)
    	... 1 more`,
			frames(StackJava, "com.example.shop.CartService", "addItem", "com.example.shop.Api", "post")},
		{"a last cause without frames falls back to the nearest with some", `com.example.shop.ApiException: failed
	at com.example.shop.Api.post(Api.java:12)
	at com.example.shop.Server.handle(Server.java:80)
Caused by: com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.run(Checkout.java:30)
	... 2 more
Caused by: java.lang.IllegalStateException: Cart 7 is locked
Caused by: java.sql.SQLException: lock timeout`,
			frames(StackJava,
				"com.example.shop.Checkout", "run",
				"com.example.shop.Api", "post",
				"com.example.shop.Server", "handle")},
		{"a cause without frames under a single exception", `com.example.shop.ApiException: failed
	at com.example.shop.Api.post(Api.java:12)
Caused by: java.lang.IllegalStateException: Cart 7 is locked`,
			frames(StackJava, "com.example.shop.Api", "post")},
		{"a cause that shares every frame is still the root", `com.example.shop.ApiException: failed
	at com.example.shop.Api.post(Api.java:12)
	at com.example.shop.Server.handle(Server.java:80)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	... 1 more`,
			frames(StackJava, "com.example.shop.Server", "handle")},
		{"recursion collapses", `	at com.acme.shop.Tree.walk(Tree.java:10)
	at com.acme.shop.Tree.walk(Tree.java:14)
	at com.acme.shop.Tree.walk(Tree.java:14)
	at com.acme.shop.Tree.start(Tree.java:3)
	at com.acme.shop.Tree.walk(Tree.java:14)`,
			frames(StackJava,
				"com.acme.shop.Tree", "walk", "com.acme.shop.Tree", "start", "com.acme.shop.Tree", "walk")},
		{"windows line ends", "\tat com.acme.shop.Api.post(Api.java:12)\r\n\tat com.acme.shop.Server.handle(Server.java:80)\r\n",
			frames(StackJava, "com.acme.shop.Api", "post", "com.acme.shop.Server", "handle")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesDotNet(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"arguments and source locations are dropped", `System.InvalidOperationException: Cart 7 is locked
   at Acme.Shop.CartService.AddItem(String sku, Int32 quantity) in C:\src\shop\CartService.cs:line 42
   at Acme.Shop.CartController.Post(CartRequest request)
   at Acme.Shop.Repo` + "`" + `1.Get[T](Int32 id)
   at Acme.Shop.Cart..ctor(String owner)
   at lambda_method(Closure, Object)`,
			frames(StackDotNet,
				"Acme.Shop.CartService", "AddItem",
				"Acme.Shop.CartController", "Post",
				"Acme.Shop.Repo`1", "Get",
				"Acme.Shop.Cart", ".ctor",
				"", "lambda_method")},
		{"async state machines unwrap to the method", `   at Acme.Shop.CartService.<AddItemAsync>d__12.MoveNext()
--- End of stack trace from previous location ---
   at System.Runtime.ExceptionServices.ExceptionDispatchInfo.Throw()
   at Acme.Shop.CartController.<PostAsync>d__4.MoveNext() in /src/shop/CartController.cs:line 20`,
			frames(StackDotNet,
				"Acme.Shop.CartService", "AddItemAsync",
				"System.Runtime.ExceptionServices.ExceptionDispatchInfo", "Throw",
				"Acme.Shop.CartController", "PostAsync")},
		{"closures unwrap to the method", `   at Acme.Shop.CartService.<>c__DisplayClass5_0.<AddItem>b__0(Item item)
   at Acme.Shop.CartService.<>c.<Total>b__7_1(Item item)
   at Acme.Shop.CartService.<>c.<Total>b__9(Item item)`,
			frames(StackDotNet,
				"Acme.Shop.CartService", "AddItem", "Acme.Shop.CartService", "Total")},
		{"local functions unwrap to their name under their method's", `   at Example.Shop.CartService.<Run>g__Validate|5_0(Int32 quantity)
   at Example.Shop.CartService.<Run>g__Validate|7_1(Int32 quantity)
   at Example.Shop.CartService.<<Run>g__Reserve|5_2>d.MoveNext()
   at Example.Shop.CartService.<>c__DisplayClass5_0.<Run>g__Check|3()
   at Program.<<Main>$>g__Handle|0_0(String[] args)
   at Program.<Main>$(String[] args)`,
			frames(StackDotNet,
				"Example.Shop.CartService.Run", "Validate",
				"Example.Shop.CartService.Run", "Reserve",
				"Example.Shop.CartService.Run", "Check",
				"Program.Main", "Handle",
				"Program", "Main")},
		{"compiled delegates lose their number", `   at lambda_method42(Closure, Object)
   at lambda_method7(Closure, Object)
   at System.Linq.Expressions.Compiled.lambda_method9(Closure)
   at Example.Shop.Cart.lambda_method42x(Int32 n)`,
			frames(StackDotNet,
				"", "lambda_method",
				"System.Linq.Expressions.Compiled", "lambda_method",
				"Example.Shop.Cart", "lambda_method42x")},
		{"a name that starts with a dot", "   at .Run()", frames(StackDotNet, "", "Run")},
		{"mono writes a space before the parameters", `System.InvalidOperationException: Cart 7 is locked
  at Example.Shop.CartService.AddItem (System.String sku, System.Int32 quantity) [0x0001a] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0
  at Example.Shop.CartController.Post (Example.Shop.CartRequest request) [0x00007] in /src/shop/CartController.cs:17
  at Example.Shop.Program.Main () [0x00000] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0`,
			frames(StackDotNet,
				"Example.Shop.CartService", "AddItem",
				"Example.Shop.CartController", "Post",
				"Example.Shop.Program", "Main")},
		{"the innermost exception is the root", `Acme.Shop.CheckoutException: failed
 ---> System.InvalidOperationException: Cart 7 is locked
   at Acme.Shop.CartService.AddItem(String sku)
   --- End of inner exception stack trace ---
   at Acme.Shop.Checkout.Run()`,
			frames(StackDotNet, "Acme.Shop.CartService", "AddItem")},
		{"the inner marker before any frame ends nothing", `   --- End of inner exception stack trace ---
   at Acme.Shop.Checkout.Run()`,
			frames(StackDotNet, "Acme.Shop.Checkout", "Run")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesGo(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"goroutine header, arguments and offsets are dropped", `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x10a2b3c]

goroutine 42 [running]:
panic({0x10a0b20?, 0xc000012345?})
	/usr/local/go/src/runtime/panic.go:770 +0x132
github.com/acme/shop/cart.(*Service).AddItem(0xc000010000, {0x10b1c2d, 0x5})
	/src/shop/cart/service.go:42 +0x1d
github.com/acme/shop/cart.Total(...)
	/src/shop/cart/total.go:9
net/http.HandlerFunc.ServeHTTP(0x10c2d3e?, {0x1234560, 0xc0000a0000}, 0xc0000b0000)
	/usr/local/go/src/net/http/server.go:2166 +0x29
main.main.func1()
	/src/shop/main.go:12 +0x25
created by main.main in goroutine 1
	/src/shop/main.go:11 +0x4a`,
			frames(StackGo,
				"github.com/acme/shop/cart", "Service.AddItem",
				"github.com/acme/shop/cart", "Total",
				"net/http", "HandlerFunc.ServeHTTP",
				"main", "main")},
		{"the throw site is below the panic and what ran while panicking", `goroutine 51 [running]:
net/http.(*conn).serve.func1()
	/usr/local/go/src/net/http/server.go:1898 +0xbe
panic({0x9a3c80?, 0xe3b1d0?})
	/usr/local/go/src/runtime/panic.go:770 +0x132
runtime.panicmem(...)
	/usr/local/go/src/runtime/panic.go:261
runtime.sigpanic()
	/usr/local/go/src/runtime/signal_unix.go:881 +0x378
shop/internal/cart.(*Service).AddItem(0xc000010000)
	/src/shop/internal/cart/service.go:42 +0x1d
main.main()
	/src/shop/main.go:12 +0x25`,
			frames(StackGo, "shop/internal/cart", "Service.AddItem", "main", "main")},
		{"an explicit panic", "runtime.gopanic({0x1, 0x2})\nmain.run(0x1)\nmain.main()",
			frames(StackGo, "main", "run", "main", "main")},
		{"a later panic is the one that counts", "main.cleanup()\npanic({0x1, 0x2})\nmain.run(0x1)\npanic({0x3, 0x4})\nmain.main()",
			frames(StackGo, "main", "run", "", "panic", "main", "main")},
		{"a trace that ends in the panic keeps its frames", "main.cleanup()\npanic({0x1, 0x2})\nruntime.sigpanic()",
			frames(StackGo, "main", "cleanup", "", "panic", "runtime", "sigpanic")},
		{"a function named like the runtime's is the application's", "shop/panic.panic(0x1)\nmain.panicmem(0x1)",
			frames(StackGo, "shop/panic", "panic", "main", "panicmem")},
		{"only the first goroutine counts", `goroutine 7 [running]:
main.worker(0x1)
	/src/shop/main.go:30 +0x1d

goroutine 1 [chan receive]:
main.main()
	/src/shop/main.go:12 +0x25`,
			frames(StackGo, "main", "worker")},
		{"generic instantiations are dropped", `github.com/acme/shop/list.Map[...](0x1, 0x2)
github.com/acme/shop/list.(*Set[go.shape.int,go.shape.string]).Add(0x1)
created by github.com/acme/shop/list.Start`,
			frames(StackGo,
				"github.com/acme/shop/list", "Map",
				"github.com/acme/shop/list", "Set.Add",
				"github.com/acme/shop/list", "Start")},
		{"a vendored package is addressed without its vendor directory", `github.com/example/shop/vendor/github.com/gin-gonic/gin.(*Context).Next(0xc0001a2000)
shop/vendor/golang.org/x/net/http2.(*serverConn).runHandler(0xc000010000)
vendor/golang.org/x/net/http2/hpack.(*Decoder).Write(0xc000010000)`,
			[]StackFrame{
				{Module: "github.com/gin-gonic/gin", Function: "Context.Next", Syntax: StackGo, Dependency: true},
				{Module: "golang.org/x/net/http2", Function: "serverConn.runHandler", Syntax: StackGo, Dependency: true},
				{Module: "golang.org/x/net/http2/hpack", Function: "Decoder.Write", Syntax: StackGo, Dependency: true}}},
		{"the file line tells a dependency from the application", `github.com/gin-gonic/gin.(*Context).Next(0xc0001a2000)
	/go/pkg/mod/github.com/gin-gonic/gin@v1.9.1/context.go:174 +0x2b
github.com/example/shop/cart.(*Service).AddItem(0xc000010000)
	/src/shop/cart/service.go:42 +0x1d
github.com/example/money.Add(0x1, 0x2)
	github.com/example/money@v0.3.0/money.go:9 +0x1d
github.com/example/tax.Rate(0x1)
	/src/shop/vendor/github.com/example/tax/rate.go:12 +0x3c
github.com/example/shop/api.Post(0xc000010000)
created by github.com/example/shop/api.Serve in goroutine 1
	/src/shop/api/serve.go:30 +0x4a`,
			[]StackFrame{
				{Module: "github.com/gin-gonic/gin", Function: "Context.Next", Syntax: StackGo, Dependency: true},
				{Module: "github.com/example/shop/cart", Function: "Service.AddItem", Syntax: StackGo},
				{Module: "github.com/example/money", Function: "Add", Syntax: StackGo, Dependency: true},
				{Module: "github.com/example/tax", Function: "Rate", Syntax: StackGo, Dependency: true},
				{Module: "github.com/example/shop/api", Function: "Post", Syntax: StackGo},
				{Module: "github.com/example/shop/api", Function: "Serve", Syntax: StackGo}}},
		{"a name that starts with a dot", ".run(0x1)\nshop/.run()",
			frames(StackGo, "", "run", "shop/", "run")},
		{"a one-letter name", "f(0x1)", frames(StackGo, "", "f")},
		{"lines that are not frames", "exit status 2\nmain.run(context deadline exceeded)\n(0x1)\ncreated by \nfoo bar()\nmain.run(0x1) tail", nil},
		{"a call in prose is not a frame", "decode(abc)\nretry(3)\nfailed()\ncart.decode(abc, 7)\ncreated by admin", nil},
		{"a frame needs a package or arguments", "run()\nmain.run()\nrun(0x1, {0xc000010000?, 0x5}, ...)",
			frames(StackGo, "main", "run", "", "run")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesNode(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"locations lose line and column", `TypeError: Cannot read properties of undefined (reading 'sku')
    at CartService.addItem (/app/src/cart/service.js:42:17)
    at async CartController.post (/app/src/cart/controller.js:17:5)
    at new Cart (file:///app/src/cart/cart.mjs:3:9)
    at Object.<anonymous> (/app/src/index.js:5:1)
    at CartService.add [as addItem] (/app/src/cart/service.js:40:3)
    at Layer.handle (/app/node_modules/express/lib/router/layer.js:95:5)
    at /app/src/cart/routes.js:9:3
    at async /app/src/cart/routes.js:11:3
    at async Promise.all (index 3)
    at process.processTicksAndRejections (node:internal/process/task_queues:95:5)
    at Array.map (<anonymous>)
    at <anonymous>`,
			frames(StackJS,
				"src/cart/service.js", "CartService.addItem",
				"src/cart/controller.js", "CartController.post",
				"src/cart/cart.mjs", "new Cart",
				"src/index.js", "<anonymous>",
				"src/cart/service.js", "CartService.add",
				"node_modules/express/lib/router/layer.js", "Layer.handle",
				"src/cart/routes.js", "<anonymous>",
				"index", "Promise.all",
				"node:internal/process/task_queues", "process.processTicksAndRejections",
				"<anonymous>", "Array.map",
				"<anonymous>", "<anonymous>")},
		{"nested dependencies address the innermost", "    at parse (/app/node_modules/a/node_modules/b/index.js:1:2)",
			frames(StackJS, "node_modules/b/index.js", "parse")},
		{"an unclosed location is kept", "    at parse (/app/src/parse.js:1:2",
			frames(StackJS, "src/parse.js", "parse")},
		{"node's own code before the node: scheme", `    at emitOne (events.js:116:13)
    at process._tickCallback (internal/process/next_tick.js:68:7)
    at Socket.emit (node:events:517:28)`,
			frames(StackJS,
				"node:events", "emitOne",
				"node:internal/process/next_tick", "process._tickCallback",
				"node:events", "Socket.emit")},
		{"prose after at is not a frame", "at least 3 items (max 5)\nat startup\nat parse ()\nat sign (@) expected\n" +
			"at checkout (final step)\nat Checkout (see above)\nat 10:11:12 the cart (id 7) was locked\nat 10:11:12", nil},
		{"function@location, as Firefox and Safari write it", `addItem@https://shop.example.invalid/static/js/cart.js:42:17
async*post@https://shop.example.invalid/static/js/api.js:17:5
@https://shop.example.invalid/static/js/main.js:5:1
CartService.prototype.total@https://shop.example.invalid/static/js/cart.js:60:9`,
			frames(StackJS,
				"static/js/cart.js", "addItem",
				"static/js/api.js", "post",
				"static/js/main.js", "<anonymous>",
				"static/js/cart.js", "CartService.prototype.total")},
		{"a mail address is not a frame", "bob@shop.example.invalid\nask bob@shop.example.invalid:25:1", nil},
		{"at without a frame", "    at \n    at async \n    at some words here", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

func TestParseStackFramesPython(t *testing.T) {
	cases := []struct {
		name  string
		stack string
		want  []StackFrame
	}{
		{"the throw site comes first", `Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
  File "/app/shop/cart.py", line 42, in add_item
    self.session.commit()
  File "/usr/lib/python3.11/site-packages/sqlalchemy/orm/session.py", line 1900, in commit
    trans.commit(_to_root=True)
  File "/usr/lib/python3.11/json/decoder.py", line 355, in raw_decode
    raise JSONDecodeError("Expecting value", s, err.value) from None
ValueError: cart 7 is locked`,
			frames(StackPython,
				"lib/python/json/decoder.py", "raw_decode",
				"site-packages/sqlalchemy/orm/session.py", "commit",
				"shop/cart.py", "add_item",
				"shop/api.py", "post")},
		{"module-level code and a frame without source", `Traceback (most recent call last):
  File "/app/shop/main.py", line 3
  File "<frozen importlib._bootstrap>", line 241, in _call_with_frames_removed
  File "/usr/lib/python3/dist-packages/yaml/loader.py", line 9, in load`,
			frames(StackPython,
				"dist-packages/yaml/loader.py", "load",
				"<frozen importlib._bootstrap>", "_call_with_frames_removed",
				"shop/main.py", "<module>")},
		{"a relative package directory keeps its path", `  File "site-packages/python3/yaml/loader.py", line 9, in load`,
			frames(StackPython, "site-packages/python3/yaml/loader.py", "load")},
		{"the first traceback is the root", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")
ValueError: locked

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`,
			frames(StackPython, "shop/cart.py", "add_item")},
		{"an exception group yields its first sub-exception", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     async with asyncio.TaskGroup() as tg:
  |   File "/usr/lib/python3.11/asyncio/taskgroups.py", line 145, in __aexit__
  |     raise me from None
  | ExceptionGroup: unhandled errors in a TaskGroup (2 sub-exceptions)
  +-+---------------- 1 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/tasks.py", line 30, in run
    |     cart.add_item(sku)
    |   File "/app/shop/cart.py", line 42, in add_item
    |     raise ValueError("locked")
    | ValueError: locked
    +---------------- 2 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/stock.py", line 9, in reserve
    |     raise KeyError(sku)
    | KeyError: 'kettle'
    +------------------------------------`,
			frames(StackPython, "shop/cart.py", "add_item", "shop/tasks.py", "run")},
		{"a group without sub-exceptions keeps its own frames", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     raise ExceptionGroup("none", [])
  +------------------------------------`,
			frames(StackPython, "shop/api.py", "post")},
		{"a line in another notation is not a frame", `  File "/app/shop/cart.py", line 42, in add_item
    session.commit()
main.run()
    at com.acme.shop.CartService.addItem(CartService.java:42)`,
			frames(StackPython, "shop/cart.py", "add_item")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertFrames(t, tc.stack, tc.want) })
	}
}

// stackNotations is indexed by StackSyntax: each notation must read its own
// sample line and stamp the frame with the index it sits at.
func TestStackNotationsMatchTheirSyntax(t *testing.T) {
	samples := map[StackSyntax]string{
		StackJava:   "at com.acme.shop.CartService.addItem(CartService.java:42)",
		StackDotNet: "at Acme.Shop.CartService.AddItem(String sku)",
		StackJS:     "at CartService.addItem (/app/src/cart/service.js:42:17)",
		StackPython: `File "/app/shop/cart.py", line 42, in add_item`,
		StackGo:     "github.com/acme/shop/cart.(*Service).AddItem(0xc000010000)",
	}
	if len(samples) != len(stackNotations)-1 {
		t.Fatalf("%d notations, %d samples", len(stackNotations)-1, len(samples))
	}
	for syntax, line := range samples {
		f, ok := stackNotations[syntax].frame(line)
		if !ok || f.Syntax != syntax || !stackNotations[syntax].owns(line) {
			t.Errorf("notation %d read %q as %+v, %v", syntax, line, f, ok)
		}
		for other := range stackNotations {
			if other != int(syntax) && other >= int(StackJava) && stackNotations[other].owns(line) {
				t.Errorf("notation %d owns %q, a line of notation %d", other, line, syntax)
			}
		}
		// The trace as a whole is read in the same notation.
		if got := ParseStackFrames(line); len(got) != 1 || got[0] != f {
			t.Errorf("ParseStackFrames(%q) = %+v, want %+v", line, got, f)
		}
	}
	if frameInApp(StackFrame{Module: "com.acme.shop.Cart", Syntax: StackSyntax(len(stackNotations))}) ||
		frameInApp(StackFrame{Module: "com.acme.shop.Cart", Syntax: -1}) {
		t.Error("a frame of an unknown notation must not be in-app")
	}
}

// A frame's module is the same wherever the code is deployed: each group
// lists one file as different hosts, releases and operating systems name it.
func TestSourceModulesAreDeployIndependent(t *testing.T) {
	py := func(path string) string { return `File "` + path + `", line 42, in add_item` }
	js := func(loc string) string { return "at addItem (" + loc + ":42:17)" }
	merge := map[string][]string{
		"shop/cart.py": {
			py("/app/shop/cart.py"), py("/srv/releases/20261126101112/shop/cart.py"), py("/var/task/shop/cart.py"),
			py("/home/deploy/shop/cart.py"), py(`C:\app\shop\cart.py`), py("/usr/src/app/shop/cart.py"),
			py("/var/www/checkout/releases/20261126101112/shop/cart.py"), py("./shop/cart.py"),
			py("/data/builds/3f2b8c1e9d/shop/cart.py"), py("/opt/v2.14.0-rc.1/shop/cart.py"),
			py("/srv/current/shop/cart.py"), py("/opt/shop/cart.py"),
		},
		"lib/python/json/decoder.py": {
			py("/usr/lib/python3.11/json/decoder.py"), py("/usr/local/lib/python3.12/json/decoder.py"),
			py("/opt/conda/lib64/python3.9/json/decoder.py"), py(`C:\Python311\Lib\json\decoder.py`),
		},
		"site-packages/sqlalchemy/orm/session.py": {
			py("/usr/lib/python3.11/site-packages/sqlalchemy/orm/session.py"),
			py("/app/.venv/lib/python3.12/site-packages/sqlalchemy/orm/session.py"),
			py(`C:\app\venv\Lib\site-packages\sqlalchemy\orm\session.py`),
		},
		"src/cart/service.js": {
			js("/app/src/cart/service.js"), js("/var/task/src/cart/service.js"), js("file:///usr/src/app/src/cart/service.js"),
			js("/srv/www/shop/releases/a1b2c3d4e5/src/cart/service.js"), js(`C:\app\src\cart\service.js`),
			js("/workspace/src/cart/service.js"), js("/home/site/wwwroot/src/cart/service.js"),
		},
		"node_modules/express/lib/router/layer.js": {
			js("/app/node_modules/express/lib/router/layer.js"), js("/var/task/node_modules/express/lib/router/layer.js"),
			js(`C:\app\node_modules\express\lib\router\layer.js`),
			js("/app/node_modules/.pnpm/express@4.19.2/node_modules/express/lib/router/layer.js"),
		},
		"static/js/main.js": {
			js("https://shop.example.invalid/static/js/main.8f3a2b1c.js"),
			js("https://staging.shop.example.invalid:8443/static/js/main-0a1b2c3d4e5f.js?v=7"),
			js("http://localhost:3000/static/js/main.js"),
		},
		"/data/apps/shop/cart.py": {py("/data/apps/shop/cart.py")}, // an unknown root stays
		// A directory of hex letters without a digit is a word, not a hash.
		"defaced/shop/cart.py": {py("/srv/defaced/shop/cart.py")},
		"index.js":             {js("/app/index.js"), js("/var/task/index.js")},
	}
	for want, lines := range merge {
		for _, line := range lines {
			got := ParseStackFrames(line)
			if len(got) != 1 || got[0].Module != want {
				t.Errorf("%s: module %+v, want %q", line, got, want)
			}
		}
	}
	split := [][2]string{
		{py("/app/shop/cart.py"), py("/app/shop/checkout.py")},
		{py("/app/shop/cart.py"), py("/app/billing/cart.py")},
		{py("/app/shop/json/decoder.py"), py("/usr/lib/python3.11/json/decoder.py")},
		{py("/app/shop/session.py"), py("/app/.venv/lib/python3.12/site-packages/shop/session.py")},
		{js("/app/src/cart/service.js"), js("/app/src/order/service.js")},
		{js("/app/src/cart/service.js"), js("/app/node_modules/cart/service.js")},
		{js("/app/events.js"), js("events.js")},
		{js("https://shop.example.invalid/static/js/main.8f3a2b1c.js"), js("https://shop.example.invalid/static/js/vendor.8f3a2b1c.js")},
	}
	for _, pair := range split {
		a, b := ParseStackFrames(pair[0]), ParseStackFrames(pair[1])
		if len(a) != 1 || len(b) != 1 || a[0].Module == b[0].Module {
			t.Errorf("%s and %s must differ: %+v, %+v", pair[0], pair[1], a, b)
		}
	}
	for i, root := range deployRoots {
		for _, earlier := range deployRoots[:i] {
			if strings.HasPrefix(root, earlier) {
				t.Errorf("deploy root %s is never reached: %s before it matches first", root, earlier)
			}
		}
	}
}

func TestParseStackFramesLineBreaks(t *testing.T) {
	want := frames(StackJava, "com.example.shop.Api", "post", "com.example.shop.Server", "handle")
	for name, stack := range map[string]string{
		"line feeds":       "\tat com.example.shop.Api.post(Api.java:12)\n\tat com.example.shop.Server.handle(Server.java:80)\n",
		"carriage returns": "\tat com.example.shop.Api.post(Api.java:12)\r\tat com.example.shop.Server.handle(Server.java:80)\r",
		"both":             "\tat com.example.shop.Api.post(Api.java:12)\r\n\tat com.example.shop.Server.handle(Server.java:80)\r\n",
		"escaped":          `java.lang.IllegalStateException: locked\n\tat com.example.shop.Api.post(Api.java:12)\n\tat com.example.shop.Server.handle(Server.java:80)`,
		"escaped, spaces":  `java.lang.IllegalStateException: locked\n    at com.example.shop.Api.post(Api.java:12)\n    at com.example.shop.Server.handle(Server.java:80)`,
		"escaped pair":     `java.lang.IllegalStateException: locked\r\n\tat com.example.shop.Api.post(Api.java:12)\r\n\tat com.example.shop.Server.handle(Server.java:80)`,
	} {
		t.Run(name, func(t *testing.T) { assertFrames(t, stack, want) })
	}
	assertFrames(t, `goroutine 1 [running]:\nmain.run(0x1)\n\t/src/shop/main.go:12 +0x25\nmain.main()\n\t/src/shop/main.go:5 +0x1d`,
		frames(StackGo, "main", "run", "main", "main"))
	// A backslash and an n are text where nothing shows they are a break,
	// and wherever the text has real line breaks: here part of a path.
	assertFrames(t, `at addItem (C:\app\node_modules\cart\index.js:42:17)`,
		frames(StackJS, "node_modules/cart/index.js", "addItem"))
	assertFrames(t, "   at Example.Shop.Cart.Add(String sku) in C:\\src\\new\\Cart.cs:line 4\n   at Example.Shop.Api.Post()",
		frames(StackDotNet, "Example.Shop.Cart", "Add", "Example.Shop.Api", "Post"))
}

// goStdlib must be the top-level names of the standard library, sorted: the
// in-app rule looks a module's first element up in it.
func TestGoStdlibNamesAreSorted(t *testing.T) {
	if !sort.StringsAreSorted(goStdlib) {
		t.Error("goStdlib must stay sorted")
	}
	for _, name := range []string{"archive", "net", "runtime", "weak"} {
		if !isGoStdlib(name + "/x") {
			t.Errorf("%s is not taken for the standard library", name)
		}
	}
	for _, module := range []string{"main", "shop/net", "nets", "github.com/example/shop", ""} {
		if isGoStdlib(module) {
			t.Errorf("%q is taken for the standard library", module)
		}
	}
}

func TestParseStackFramesUnparseable(t *testing.T) {
	for _, in := range []string{
		"", "\n\n", "not a stack trace", "Error: something failed (code 5)",
		"at", "at ", "0x1)", "File \"x\"", "... 3 more", "Caused by: ", "goroutine 1 [running]:",
		"app/models/cart.rb:42:in `add_item'", "#0 /app/Cart.php(42): Cart->addItem()",
		"at least 3 items (max 5)", "at startup", "decode(abc)", "the cart (id 7) was locked at 10:11:12",
	} {
		if got := ParseStackFrames(in); got != nil {
			t.Errorf("ParseStackFrames(%q) = %+v, want nil", in, got)
		}
	}
}

func TestBuiltinInApp(t *testing.T) {
	cases := []struct {
		frame StackFrame
		inApp bool
	}{
		{StackFrame{Module: "com.acme.shop.CartService", Syntax: StackJava}, true},
		{StackFrame{Module: "Acme.Shop.CartService", Syntax: StackDotNet}, true},
		{StackFrame{Module: "system.cart.Service", Syntax: StackDotNet}, true},
		{StackFrame{Module: "javafx", Syntax: StackJava}, true},
		{StackFrame{Module: "java.lang.Thread", Syntax: StackJava}, false},
		{StackFrame{Module: "org.springframework.web.Dispatcher", Syntax: StackJava}, false},
		{StackFrame{Module: "com.dynatrace.agent.Hook", Syntax: StackJava}, false},
		{StackFrame{Module: "System.Threading.Tasks.Task", Syntax: StackDotNet}, false},
		{StackFrame{Module: "Microsoft.AspNetCore.Routing.Endpoint", Syntax: StackDotNet}, false},
		{StackFrame{Module: "com.acme.shop.CartService<auto>", Syntax: StackJava}, false},
		{StackFrame{Module: "com.example.shop.Cart$$anon$1", Syntax: StackJava}, true},
		{StackFrame{Module: "", Function: "lambda_method", Syntax: StackDotNet}, false},

		{StackFrame{Module: "main", Syntax: StackGo}, true},
		{StackFrame{Module: "github.com/acme/shop/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "shop.example.invalid/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "shop/internal/cart", Syntax: StackGo}, true},
		{StackFrame{Module: "cart", Syntax: StackGo}, true},
		{StackFrame{Module: "mainframe", Syntax: StackGo}, true},
		{StackFrame{Module: "github.com/gin-gonic/gin", Syntax: StackGo, Dependency: true}, false},
		{StackFrame{Module: "shop/internal/cart", Syntax: StackGo, Dependency: true}, false},
		{StackFrame{Module: "internal/poll", Syntax: StackGo}, false},
		{StackFrame{Module: "sync/atomic", Syntax: StackGo}, false},
		{StackFrame{Module: "runtime", Syntax: StackGo}, false},
		{StackFrame{Module: "net/http", Syntax: StackGo}, false},
		{StackFrame{Module: "encoding/json.v2", Syntax: StackGo}, false},
		{StackFrame{Module: "golang.org/x/sync/errgroup", Syntax: StackGo}, false},
		{StackFrame{Module: "google.golang.org/grpc", Syntax: StackGo}, false},
		{StackFrame{Module: "", Function: "panic", Syntax: StackGo}, false},

		{StackFrame{Module: "src/cart/service.js", Syntax: StackJS}, true},
		{StackFrame{Module: "index.js", Syntax: StackJS}, true},
		{StackFrame{Module: "static/js/cart.js", Syntax: StackJS}, true},
		{StackFrame{Module: "lib/node_modules.js", Syntax: StackJS}, true},
		{StackFrame{Module: "node_modules/express/lib/router/layer.js", Syntax: StackJS}, false},
		{StackFrame{Module: "node:internal/process/task_queues", Syntax: StackJS}, false},
		{StackFrame{Module: "node:events", Syntax: StackJS}, false},
		{StackFrame{Module: "<anonymous>", Syntax: StackJS}, false},
		{StackFrame{Module: "native", Syntax: StackJS}, false},
		{StackFrame{Module: "index", Syntax: StackJS}, false},

		{StackFrame{Module: "shop/cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "shop/lib/python/cart.py", Syntax: StackPython}, true},
		{StackFrame{Module: "site-packages/sqlalchemy/orm/session.py", Syntax: StackPython}, false},
		{StackFrame{Module: "dist-packages/yaml/loader.py", Syntax: StackPython}, false},
		{StackFrame{Module: "lib/python/json/decoder.py", Syntax: StackPython}, false},
		{StackFrame{Module: "<frozen importlib._bootstrap>", Syntax: StackPython}, false},
	}
	for _, tc := range cases {
		if got := frameInApp(tc.frame); got != tc.inApp {
			t.Errorf("InApp(%+v) = %v, want %v", tc.frame, got, tc.inApp)
		}
	}
}

// A trace is read in the notation most of its lines are written in, not in
// the first one that recognises a line of it: a .NET parameter list can read
// like a Java source location, and a Java frame without one like a .NET
// frame.
func TestParseStackPicksTheNotationOfMostLines(t *testing.T) {
	javaChain := func(first string) string {
		return "com.example.shop.CheckoutException: failed\n\tat " + first +
			"\n\tat org.springframework.web.Dispatcher.dispatch(Dispatcher.java:9)\n" +
			"Caused by: java.lang.IllegalStateException: Cart 7 is locked\n" +
			"\tat com.example.shop.Cart.add(Cart.java:12)\n\tat com.example.shop.Checkout.run(Checkout.java:30)\n"
	}
	javaWant := frames(StackJava, "com.example.shop.Cart", "add", "com.example.shop.Checkout", "run")
	cases := []struct {
		name, stack string
		want        []StackFrame
		typ         string
	}{
		{"dotnet: parameters that read like a Java location", `Example.Shop.CheckoutException: failed
 ---> System.InvalidOperationException: Cart 7 is locked
   at Example.Shop.Util.Check(System.String)
   at Example.Shop.Cart.Save(String sku)
   --- End of inner exception stack trace ---
   at Example.Shop.Checkout.Run()`,
			frames(StackDotNet, "Example.Shop.Util", "Check", "Example.Shop.Cart", "Save"), "System.InvalidOperationException"},
		{"java: an obfuscated source file", javaChain("com.example.shop.Checkout.run(SourceFile:12)"), javaWant, "java.lang.IllegalStateException"},
		{"java: an unknown source with a line", javaChain("com.example.shop.Checkout.run(Unknown Source:0)"), javaWant, "java.lang.IllegalStateException"},
		{"java: a negative line", javaChain("com.example.shop.Checkout.run(Checkout.java:-1)"), javaWant, "java.lang.IllegalStateException"},
		{"java: compiled code", javaChain("com.example.shop.Checkout.run(Compiled Code)"), javaWant, "java.lang.IllegalStateException"},
		{"java: a template file", javaChain("views.html.cart$.apply(cart.scala.html:42)"), javaWant, "java.lang.IllegalStateException"},
		{"java: a file name beyond ASCII", javaChain("com.example.shop.Größe.berechne(Größe.java:7)"), javaWant, "java.lang.IllegalStateException"},
		{"java: a lambda's synthetic class on top",
			javaChain("com.example.shop.Checkout$$Lambda$14/0x0000000800c03000.accept(Unknown Source)"), javaWant, "java.lang.IllegalStateException"},
		{"python: prose that reads like a .NET frame", `at step(1)
Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
  File "/app/shop/cart.py", line 42, in add_item
    raise ValueError("locked")`,
			frames(StackPython, "shop/cart.py", "add_item", "shop/api.py", "post"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ, _ := parseStack(tc.stack)
			if !reflect.DeepEqual(got, tc.want) || typ != tc.typ {
				t.Errorf("frames %+v type %q\nwant %+v %q", got, typ, tc.want, tc.typ)
			}
		})
	}
	// A line of a message that reads like a call does not take the trace
	// under it.
	trace := "\tat com.example.shop.Cart.add(Cart.java:12)\n\tat com.example.shop.Checkout.run(Checkout.java:30)\n"
	for _, prose := range []string{
		"cart.validate()", "at redis.internal:6379", "config.load(app.yaml) failed", "at step(1)", "at ./src/lib.rs:12:5",
	} {
		assertFrames(t, prose+"\n"+trace, javaWant)
	}
}

// "at location" alone is how V8 writes a frame without a function, and how
// Rust, Swift and others write the file line under a frame of their own. It
// The lines that decide the notation are the first stackVoteLines a
// notation owns: a long trace is not read twice to find out what it is.
func TestParseStackVotesOnTheFirstLines(t *testing.T) {
	java := func(n int) string {
		return strings.Repeat("\tat com.example.shop.Cart.add(Cart.java:42)\n\tat com.example.shop.Api.post(Api.java:12)\n", n/2)
	}
	python := strings.Repeat("  File \"/app/shop/cart.py\", line 42, in add_item\n  File \"/app/shop/api.py\", line 17, in post\n", 50)
	for lines, want := range map[int]StackSyntax{stackVoteLines/2 + 2: StackJava, stackVoteLines/2 - 2: StackPython} {
		got := ParseStackFrames(java(lines) + python)
		if len(got) == 0 || got[0].Syntax != want {
			t.Errorf("%d Java lines of the first %d: notation %v, want %v", lines, stackVoteLines, got[0].Syntax, want)
		}
	}
}

// makes a trace JavaScript only next to something only JavaScript writes.
func TestBareLocationsAloneAreNotJavaScript(t *testing.T) {
	rust := func(fn string) string {
		return "   0: shop::" + fn + "\n             at ./src/lib.rs:12:5\n   1: shop::main\n             at ./src/main.rs:4:5\n"
	}
	read := mustFingerprint(t, Event{Type: "shop::Error", Message: "cannot read the cart", StackTrace: rust("read")})
	write := mustFingerprint(t, Event{Type: "shop::Error", Message: "cannot write the cart", StackTrace: rust("write")})
	if read.Rung != RungMessage || write.Rung != RungMessage || read.Key == write.Key {
		t.Errorf("rungs %v and %v, keys %s and %s; want two keys on the message rung", read.Rung, write.Rung, read.Key, write.Key)
	}
	for _, stack := range []string{
		"at ./src/lib.rs:12:5", "at /app/Sources/Shop/Cart.swift:42", "at redis.internal:6379\nat 10.0.0.7:5432",
	} {
		if got := ParseStackFrames(stack); got != nil {
			t.Errorf("ParseStackFrames(%q) = %+v, want nil", stack, got)
		}
	}
	// A file JavaScript runs, Node's own scheme, or a named frame next to it.
	assertFrames(t, "at /app/src/cart/service.js:30:71", frames(StackJS, "src/cart/service.js", FrameAnonymous))
	assertFrames(t, "at /app/src/cart/service.ts:30:71\nat /app/src/main.mjs:5:1",
		frames(StackJS, "src/cart/service.ts", FrameAnonymous, "src/main.mjs", FrameAnonymous))
	assertFrames(t, "at node:internal/main/run_main_module:28:49", frames(StackJS, "node:internal/main/run_main_module", FrameAnonymous))
	assertFrames(t, "at addItem (/app/bundle:1:2000)\nat /app/bundle:1:3000",
		frames(StackJS, "bundle", "addItem", "bundle", FrameAnonymous))
}

// Mono's parameter list is close to prose, and next to V8's "at fn
// (location)". The offset and the file after the list show which it is.
func TestMonoFramesWithAnyParameters(t *testing.T) {
	assertFrames(t, "  at Example.Shop.CartService.Count (System.Collections.Generic.Dictionary`2[System.String,System.Int32] map) "+
		"[0x0001a] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0\n"+
		"  at Example.Shop.Api.Get (shop.Request request) [0x00007] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0\n"+
		"  at Example.Shop.Program.Main () [0x00000] in <3f2b8c1e9d4a4b7c8e210a1b2c3d4e5f>:0",
		frames(StackDotNet, "Example.Shop.CartService", "Count", "Example.Shop.Api", "Get", "Example.Shop.Program", "Main"))
	if got := ParseStackFrames("at Example.Shop.CartService.Count (shop.Request request) [0x0001a] in <3f2b8c1e>:0"); len(got) != 1 ||
		got[0].Syntax != StackDotNet {
		t.Errorf("a single Mono frame was read as %+v", got)
	}
}

// Names are not ASCII everywhere, and Kotlin mangles a method that takes a
// value class with a hyphen.
func TestFrameNamesBeyondASCII(t *testing.T) {
	assertFrames(t, "\tat com.example.Größe.berechne(Größe.java:7)\n\tat com.example.shop.Money.plus-LRDsOJo(Money.kt:1)\n"+
		"\tat com.example.shop.Api.get(Api.java:3)",
		frames(StackJava, "com.example.Größe", "berechne", "com.example.shop.Money", "plus-LRDsOJo", "com.example.shop.Api", "get"))
	assertFrames(t, "   at Beispiel.Größe.Berechne(String wert)\n   at Beispiel.Api.Get()",
		frames(StackDotNet, "Beispiel.Größe", "Berechne", "Beispiel.Api", "Get"))
	assertFrames(t, "    at Größe.berechne (/app/src/größe.js:4:2)\ngröße@https://shop.example.invalid/static/app.js:1:2",
		frames(StackJS, "src/größe.js", "Größe.berechne", "static/app.js", "größe"))
	assertFrames(t, "shop/größe.(*Maß).Berechne(0xc000010000)\nmain.main()",
		frames(StackGo, "shop/größe", "Maß.Berechne", "main", "main"))
}

// Text that no trace is made of is not read: a line longer than any frame,
// and everything past the size no real trace reaches.
func TestParseStackBoundsItsInput(t *testing.T) {
	frame := "\tat com.example.shop.Cart.add(Cart.java:12)\n"
	long := "\tat com.example.shop." + strings.Repeat("a", stackLineMax) + ".run(Cart.java:1)\n"
	assertFrames(t, long+frame, frames(StackJava, "com.example.shop.Cart", "add"))
	atLimit := "at com.example.shop." + strings.Repeat("a", stackLineMax-len("at com.example.shop..run(C.java:1)")) + ".run(C.java:1)"
	if got := ParseStackFrames(atLimit); len(atLimit) != stackLineMax || len(got) != 1 {
		t.Errorf("a line of exactly %d bytes (%d) gave %d frames, want one", stackLineMax, len(atLimit), len(got))
	}
	if got := ParseStackFrames(strings.Repeat("at ", stackTextMax)); got != nil {
		t.Errorf("one line of %d bytes gave %d frames", 3*stackTextMax, len(got))
	}
	// The text is read up to the last whole line inside the limit.
	head := frame + strings.Repeat("x", stackLineMax-1) + "\n"
	filler := strings.Repeat(strings.Repeat("x", stackLineMax-1)+"\n", (stackTextMax-len(head))/stackLineMax)
	late := "\tat com.example.shop.Late.run(Late.java:1)\n"
	pad := strings.Repeat("y", stackTextMax-len(head)-len(filler)-len(late)-1) + "\n"
	if got := ParseStackFrames(head + filler + pad + late); len(got) != 2 {
		t.Errorf("a frame that ends at byte %d was not read: %+v", stackTextMax, got)
	}
	if got := ParseStackFrames(head + filler + "y" + pad + late); len(got) != 1 {
		t.Errorf("a frame that ends past byte %d was read: %+v", stackTextMax, got)
	}
}

func TestParseStackFramesIsNilWithoutFrames(t *testing.T) {
	// Every frame of the text belongs to a suppressed exception.
	stack := "\tSuppressed: c.D: y\n\t\tat c.D.d(D.java:2)"
	if got := ParseStackFrames(stack); got != nil {
		t.Errorf("ParseStackFrames(%q) = %#v, want nil", stack, got)
	}
}

// A frame built without a notation belongs to none: it is neither in-app
// nor pass-through by some notation's rules.
func TestFramesWithoutASyntaxFollowNoNotation(t *testing.T) {
	if frameInApp(StackFrame{}) || frameInApp(StackFrame{Module: "com.example.shop.Cart", Function: "add"}) {
		t.Error("a frame without a syntax counts as application code")
	}
	if isPassThrough(StackFrame{Module: "shop/web.py", Function: "doFilter"}) {
		t.Error("a frame without a syntax is pass-through by Java's list")
	}
	if StackJava == 0 || stackNotations[0].frame != nil {
		t.Error("the zero StackSyntax must name no notation")
	}
}

// A database driver, a cloud SDK and the platform of a phone are on the
// stack of unrelated bugs like a framework is. Counted as the
// application's they would fill the frames of a key.
func TestDriversAndSDKsAreNotTheApplication(t *testing.T) {
	vendor := map[StackSyntax][]string{
		StackJava: {
			"org.postgresql.jdbc.PgStatement", "com.mysql.cj.jdbc.ClientPreparedStatement", "org.mariadb.jdbc.Statement",
			"oracle.jdbc.driver.OraclePreparedStatement", "com.microsoft.sqlserver.jdbc.SQLServerStatement", "org.h2.jdbc.JdbcStatement",
			"com.mongodb.client.internal.MongoCollectionImpl", "org.bson.BsonDocument", "redis.clients.jedis.Jedis",
			"io.lettuce.core.RedisClient", "com.datastax.oss.driver.api.core.CqlSession", "org.neo4j.driver.internal.InternalSession",
			"software.amazon.awssdk.services.s3.DefaultS3Client", "com.amazonaws.services.s3.AmazonS3Client",
			"com.azure.storage.blob.BlobClient", "com.microsoft.azure.servicebus.QueueClient",
			"com.google.cloud.storage.StorageImpl", "com.google.api.client.http.HttpRequest", "com.google.protobuf.GeneratedMessageV3",
			"android.os.Handler", "androidx.fragment.app.Fragment", "dalvik.system.VMStack", "com.android.internal.os.ZygoteInit",
			"com.google.android.gms.tasks.Task", "com.google.firebase.firestore.FirebaseFirestore", "libcore.io.Linux",
		},
		StackDotNet: {
			"Npgsql.NpgsqlCommand", "MySql.Data.MySqlClient.MySqlCommand", "MySqlConnector.MySqlCommand",
			"Oracle.ManagedDataAccess.Client.OracleCommand", "MongoDB.Driver.MongoCollectionImpl", "StackExchange.Redis.ConnectionMultiplexer",
			"Dapper.SqlMapper", "Amazon.S3.AmazonS3Client", "Azure.Storage.Blobs.BlobClient", "Google.Cloud.Storage.V1.StorageClientImpl",
			"Android.App.Activity", "AndroidX.Fragment.App.Fragment", "Java.Lang.Thread", "Xamarin.Forms.Application",
		},
		StackGo: {
			"github.com/lib/pq", "github.com/jackc/pgx/v5", "github.com/go-sql-driver/mysql", "github.com/mattn/go-sqlite3",
			"go.mongodb.org/mongo-driver/mongo", "github.com/redis/go-redis/v9", "github.com/go-redis/redis/v8",
			"github.com/aws/aws-sdk-go-v2/service/s3", "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob",
			"cloud.google.com/go/storage", "github.com/googleapis/gax-go/v2",
		},
	}
	for syntax, modules := range vendor {
		for _, module := range modules {
			if f := (StackFrame{Module: module, Function: "f", Syntax: syntax}); frameInApp(f) {
				t.Errorf("%s counts as application code", module)
			}
		}
	}
	// A name that only starts like one of them is the application's.
	for syntax, modules := range map[StackSyntax][]string{
		StackJava:   {"com.mysqlshop.Cart", "androids.shop.Cart", "oracle.shop.Cart", "com.google.shop.Cart", "com.microsoft.shop.Cart"},
		StackDotNet: {"Amazonia.Shop.Cart", "Googleplex.Cart", "Javascript.Shop.Cart", "Oracle.Shop.Cart"},
		StackGo:     {"github.com/library/shop", "github.com/awsome/shop/cart", "cloud.google.company.example/shop"},
	} {
		for _, module := range modules {
			if f := (StackFrame{Module: module, Function: "f", Syntax: syntax}); !frameInApp(f) {
				t.Errorf("%s does not count as application code", module)
			}
		}
	}
	// The driver's frames do not push the application's out of the key.
	fp := mustFingerprint(t, Event{Type: "org.postgresql.util.PSQLException", StackTrace: javaStack(
		"org.postgresql.core.v3.QueryExecutorImpl.receiveErrorResponse", "org.postgresql.core.v3.QueryExecutorImpl.processResults",
		"org.postgresql.core.v3.QueryExecutorImpl.execute", "org.postgresql.jdbc.PgStatement.executeInternal",
		"org.postgresql.jdbc.PgStatement.execute", "org.postgresql.jdbc.PgPreparedStatement.executeWithFlags",
		"com.example.shop.CartRepo.save", "com.example.shop.CartService.addItem")})
	want := frames(StackJava, "org.postgresql.core.v3.QueryExecutorImpl", "receiveErrorResponse",
		"com.example.shop.CartRepo", "save", "com.example.shop.CartService", "addItem")
	if fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

// Every entry of a vendor table takes its frames out of the application's,
// the last as much as the first, and a library frame between two of the
// application's does not count toward a key.
func TestVendorTablesApplyWhole(t *testing.T) {
	tables := []struct {
		syntax   StackSyntax
		prefixes []string
	}{
		{StackJava, javaVendorPrefixes}, {StackDotNet, dotnetVendorPrefixes}, {StackGo, goVendorPrefixes},
		{StackPython, pythonPackageDirs},
	}
	for _, table := range tables {
		for _, prefix := range table.prefixes {
			module := prefix + "x"
			if table.syntax == StackGo && !strings.HasSuffix(prefix, "/") {
				module = prefix + "/x" // a module, which matches whole
			}
			if f := (StackFrame{Module: module, Function: "f", Syntax: table.syntax}); frameInApp(f) {
				t.Errorf("%+v counts as application code", f)
			}
		}
	}
	for _, name := range goStdlib {
		if f := (StackFrame{Module: name + "/x", Function: "f", Syntax: StackGo}); frameInApp(f) {
			t.Errorf("%+v counts as application code", f)
		}
	}
	fp := mustFingerprint(t, Event{Type: "T", StackTrace: `   at Newtonsoft.Json.JsonConvert.DeserializeObject(String value)
   at Example.Shop.Cart.Load(String id)
   at Newtonsoft.Json.Linq.JToken.ToObject(Type objectType)
   at Example.Shop.Api.Get(String id)`})
	want := frames(StackDotNet, "Newtonsoft.Json.JsonConvert", "DeserializeObject", "Example.Shop.Cart", "Load", "Example.Shop.Api", "Get")
	if fp.Rung != RungAppFrames || !reflect.DeepEqual(fp.Frames, want) {
		t.Errorf("rung %v frames %+v", fp.Rung, fp.Frames)
	}
}

// Every listed pass-through function is one, bare and as a method.
func TestPassThroughListsApplyWhole(t *testing.T) {
	for syntax, notation := range stackNotations {
		for _, name := range notation.passThrough {
			for _, function := range []string{name, "Type." + name} {
				if f := (StackFrame{Module: "m", Function: function, Syntax: StackSyntax(syntax)}); !isPassThrough(f) {
					t.Errorf("%+v is not pass-through", f)
				}
			}
		}
	}
}

// FuzzParseStackFrames checks what must hold for any input: no panic, the
// same frames on every parse, no empty function, and no adjacent repeats.
func FuzzParseStackFrames(f *testing.F) {
	for _, seed := range []string{
		"", "at ", "at (", "at x (", "at a.b(", "at a..b()", "at .()", "a.b(C.java:1)", "created by ",
		"created by a in goroutine 1", "x.(*T).m(0x1)", "().()", "... 99999999999999999999 more",
		"Caused by: x\n... 3 more", "File \"a\", line 1, in f\nx()\nFile \"b\", line 2",
		"goroutine 1 [running]:\nmain.main()\ngoroutine 2 [x]:\nmain.other()",
		"at a.<b>d__1.MoveNext()", "at a.<>c.<b>b__1()", "at a$$Lambda$1/0x1.run()", "at a$$b.c(<generated>)",
		"at /", "at async ", "at f [as g] (x:1:2)", "--- End of inner exception stack trace ---",
		"Traceback (most recent call last):", "Suppressed: x\nat a.b(C.java:1)",
		"at A.B (System.String s) [0x1] in <a>:0", "f@https://a/b.js:1:2", "@a:1:2", "a.b (C.java:1)",
		"| File \"a\", line 1, in f\n+-+---- 1 ----\n| File \"b\", line 2, in g\n+---- 2 ----", `at a.b(C.java:1)\nat c.d(E.java:2)`,
		"at a.b(C.java:1)\rat c.d(E.java:2)", "run(0x1, {0x2?}, ...)", "created by a.b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := ParseStackFrames(in)
		if again := ParseStackFrames(in); !reflect.DeepEqual(got, again) {
			t.Fatalf("not deterministic for %q", in)
		}
		// What a key takes of a trace is what the trace gives where it
		// is at fault, and nothing where it is not.
		lines := stackLines(in)
		_, typ, atFault := parseStack(in)
		if _, _, goFault := goRootCause(lines); goAtFault(lines) != goFault {
			t.Fatalf("goAtFault disagrees with the frames for %q", in)
		}
		faultFrames, faultType := faultStack(in)
		if atFault && (!reflect.DeepEqual(faultFrames, got) || faultType != typ) || !atFault && (faultFrames != nil || faultType != "") {
			t.Fatalf("faultStack gives %+v (%q) for %q, at fault %v", faultFrames, faultType, in, atFault)
		}
		for i, fr := range got {
			if fr.Function == "" {
				t.Fatalf("empty function in frame %d for %q", i, in)
			}
			if i > 0 && got[i-1] == fr {
				t.Fatalf("adjacent repeat at frame %d for %q", i, in)
			}
			if strings.ContainsAny(fr.Module+fr.Function, "\n") {
				t.Fatalf("line break in frame %d for %q", i, in)
			}
			frameInApp(fr)
		}
	})
}

func TestJavaFrameName(t *testing.T) {
	cases := []struct {
		line, name string
		ok         bool
	}{
		{"at a.b(C.java:1)", "a.b", true},
		{"a.b(C.java:1)", "a.b", true},
		{"a.b (C.java)", "a.b", true},
		{"at a.b(Native Method)", "a.b", true},
		{"at a.b(Unknown Source) ~[x.jar:1]", "a.b", true},
		{"at a..b(C.java:1)", "a..b", true},
		{"at java.base/a.B.<init>(B.java:1)", "java.base/a.B.<init>", true},
		{"at a@1.b(B.java:1)", "a@1.b", true},
		{"ata.b(C.java:1)", "ata.b", true},
		{"at a.b(C-1.java:1)", "a.b", true},
		{"at a.b(SourceFile:12)", "a.b", true},
		{"at a.b(Unknown Source:0)", "a.b", true},
		{"at a.b(C.java:-1)", "a.b", true},
		{"at a.b(Compiled Code)", "a.b", true},
		{"at a.b(c.scala.html:42)", "a.b", true},
		{"at a.b(C.java:1) [x.jar:1]", "a.b", true},
		// Names beyond ASCII, and the hyphen of a Kotlin mangled name.
		{"at a.é(É.java:1)", "a.é", true},
		{"at app//a.B$1.c-d(B.java:1)", "app//a.B$1.c-d", true},
		// No frame: nothing in the parentheses, no class, no method, more
		// than one space before the location, a second "at".
		{"", "", false},
		{"(", "", false},
		{"a.b(", "", false},
		{"a.b  (C.java:1)", "", false},
		{"at at a.b(C.java:1)", "", false},
		{"at  a.b(C.java:1)", "", false},
		{"at .b(C.java:1)", "", false},
		{"at a.(C.java:1)", "", false},
		{"at b(C.java:1)", "", false},
		// A method name holds no separator of the class path.
		{"at a.b/c(B.java:1)", "", false},
		{"at a.b@1(B.java:1)", "", false},
		{"at a.b c(B.java:1)", "", false},
		// The parentheses hold a source location, not parameters, and the
		// line ends behind them or goes on with a logger's archive.
		{"at a.b(<generated>)x", "", false},
		{"at a.b(C.kt:12)(D.java:1)", "", false},
		{"a.b(c.yaml) failed", "", false},
		{"at a.b(/app/c.js:1:2)", "", false},
		{"at a.b(C.java:1:2)", "", false},
		{"at a.b()", "", false},
		{"at a.b(C.)", "", false},
		{"at a.b(.C)", "", false},
		{"at a.b(C.java:)", "", false},
		{"at a.b(C.java:-)", "", false},
		{"at a.b(C.java:1x)", "", false},
		{"at a.b(:1)", "", false},
		{"at a.b(c, d.e)", "", false},
		{"at a.b(Some Source:1)", "", false},
		{"at a.b(Native Method:1)", "", false},
		{"at a.b(C.java:1) failed", "", false},
		{"at a.b(String s)", "", false},
		{"at a.b(B.java:x)", "", false},
		{"at a.b((B.java:1)", "", false},
		{"at a.b(B.java:1", "", false},
	}
	for _, tc := range cases {
		if name, ok := javaFrameName(tc.line); name != tc.name || ok != tc.ok {
			t.Errorf("javaFrameName(%q) = %q, %v, want %q, %v", tc.line, name, ok, tc.name, tc.ok)
		}
	}
}

// "... N more" counts the frames a cause shares with the exception around
// it as the JVM printed them, a lambda's synthetic frame included. A trace
// must read the same compressed and written out.
func TestJavaMoreCountsEveryPrintedFrame(t *testing.T) {
	const head = `com.example.shop.CheckoutException: checkout failed
	at com.example.shop.Checkout.wrap(Checkout.java:30)
	at com.example.shop.Checkout$$Lambda$14/0x0000000800c03000.run(Unknown Source)
	at com.example.shop.Main.run(Main.java:5)
Caused by: java.lang.IllegalStateException: Cart 7 is locked
	at com.example.shop.Cart.fail(Cart.java:12)
`
	want := frames(StackJava, "com.example.shop.Cart", "fail", "com.example.shop.Main", "run")
	for name, tail := range map[string]string{
		"compressed":  "\t... 2 more\n",
		"written out": "\tat com.example.shop.Checkout$$Lambda$14/0x0000000800c03000.run(Unknown Source)\n\tat com.example.shop.Main.run(Main.java:5)\n",
	} {
		got, typ, _ := parseStack(head + tail)
		if !reflect.DeepEqual(got, want) || typ != "java.lang.IllegalStateException" {
			t.Errorf("%s: frames %+v type %q, want %+v", name, got, typ, want)
		}
	}
	a := mustFingerprint(t, Event{Type: "T", StackTrace: head + "\t... 2 more\n"})
	b := mustFingerprint(t, Event{Type: "T", StackTrace: head + "\t... 3 more\n"})
	if a.Key == b.Key {
		t.Error("the number of shared frames must matter")
	}
}

func TestJavaChainBoundaries(t *testing.T) {
	cases := []struct {
		name, stack string
		want        []StackFrame
		typ         string
	}{
		// A cause printed without its frames stands between two that have
		// them: what the last one shares, it shares with the first.
		{"a cause without frames in the middle", `shop.A: outer
	at shop.A.a(A.java:1)
	at shop.B.b(B.java:2)
Caused by: shop.Mid: middle
Caused by: shop.Root: root
	at shop.C.c(C.java:3)
	... 1 more`, frames(StackJava, "shop.C", "c", "shop.B", "b"), "shop.Root"},
		{"shared frames through two causes", `shop.A: outer
	at shop.A.a(A.java:1)
	at shop.B.b(B.java:2)
	at shop.Main.main(Main.java:9)
Caused by: shop.Mid: middle
	at shop.M.m(M.java:4)
	... 2 more
Caused by: shop.Root: root
	at shop.C.c(C.java:3)
	... 2 more`, frames(StackJava, "shop.C", "c", "shop.B", "b", "shop.Main", "main"), "shop.Root"},
		{"more shared frames than the cause around it has", `shop.A: outer
	at shop.A.a(A.java:1)
Caused by: shop.Root: root
	at shop.C.c(C.java:3)
	... 7 more`, frames(StackJava, "shop.C", "c", "shop.A", "a"), "shop.Root"},
		{"a cause of shared frames only", `shop.A: outer
	at shop.A.a(A.java:1)
	at shop.B.b(B.java:2)
Caused by: shop.Root: root
	... 1 more`, frames(StackJava, "shop.B", "b"), "shop.Root"},
		{"a last cause of synthetic frames only is not the root", `shop.A: outer
	at shop.A.a(A.java:1)
Caused by: shop.Root: root
	at shop.A$$Lambda$3/0x0000000800c03000.run(Unknown Source)`, frames(StackJava, "shop.A", "a"), ""},
		{"synthetic frames between real ones go last", `shop.A: outer
	at shop.A.a(A.java:1)
	at shop.A$$Lambda$3/0x0000000800c03000.run(Unknown Source)
	at shop.B.b(B.java:2)
Caused by: shop.Root: root
	at shop.C.c(C.java:3)
	at shop.C$$Lambda$5/0x0000000800c04000.call(Unknown Source)
	... 3 more`, frames(StackJava, "shop.C", "c", "shop.A", "a", "shop.B", "b"), "shop.Root"},
		// The words after "Caused by:" are a type only where a colon or the
		// line's end follows them. Below a frame such a line still starts
		// a cause, whose type cannot be read; above the frames of its
		// section it is a line of the message.
		{"prose after Caused by is no type", `shop.A: outer
	at shop.A.a(A.java:1)
Caused by: connection reset by peer
	at shop.C.c(C.java:3)`, frames(StackJava, "shop.C", "c"), ""},
		{"prose after Caused by below the shared frames", `shop.A: outer
	at shop.A.a(A.java:1)
Caused by: shop.Inner: write failed
	... 1 more
Caused by: connection reset by peer
	at shop.C.c(C.java:3)`, frames(StackJava, "shop.C", "c"), ""},
		{"prose after Caused by in a message opens no section", `shop.A: outer
	at shop.A.a(A.java:1)
Caused by: shop.Inner: write failed
Caused by: connection reset by peer
	at shop.C.c(C.java:3)
	... 1 more`, frames(StackJava, "shop.C", "c", "shop.A", "a"), "shop.Inner"},
		{"a type beyond ASCII", `shop.A: outer
	at shop.A.a(A.java:1)
Caused by: shop.Größenfehler: zu groß
	at shop.C.c(C.java:3)`, frames(StackJava, "shop.C", "c"), "shop.Größenfehler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ, _ := parseStack(tc.stack)
			if !reflect.DeepEqual(got, tc.want) || typ != tc.typ {
				t.Errorf("frames %+v type %q\nwant %+v %q", got, typ, tc.want, tc.typ)
			}
		})
	}
}

// A JVM class compiled without line tables prints the file alone, and an
// obfuscated one the word "SourceFile". Both are Java's: read as .NET, whose
// innermost exception comes first, the wrapper would be the root.
func TestJavaTraceWithoutLineNumbers(t *testing.T) {
	trace := func(location func(file string, line int) string) string {
		return "Exception in thread \"main\" java.lang.IllegalStateException: checkout failed\n" +
			"\tat Checkout.pay(" + location("Checkout.java", 12) + ")\n\tat Checkout.main(" + location("Checkout.java", 5) + ")\n" +
			"Caused by: java.lang.IllegalArgumentException: no stock\n" +
			"\tat Checkout.reserve(" + location("Checkout.java", 20) + ")\n\tat Checkout.pay(" + location("Checkout.java", 10) + ")\n\t... 1 more\n"
	}
	want := frames(StackJava, "Checkout", "reserve", "Checkout", "pay", "Checkout", "main")
	lines := mustFingerprint(t, Event{Type: "java.lang.IllegalStateException", StackTrace: trace(func(file string, line int) string {
		return fmt.Sprintf("%s:%d", file, line)
	})})
	if lines.Type != "java.lang.IllegalArgumentException" || !reflect.DeepEqual(lines.Frames, want) {
		t.Fatalf("with lines: type %q frames %+v", lines.Type, lines.Frames)
	}
	for name, location := range map[string]func(string, int) string{
		"the file alone":      func(file string, _ int) string { return file },
		"a Kotlin file alone": func(string, int) string { return "Checkout.kt" },
		"an obfuscated file":  func(string, int) string { return "SourceFile" },
	} {
		got := mustFingerprint(t, Event{Type: "java.lang.IllegalStateException", StackTrace: trace(location)})
		if got.Key != lines.Key || got.Type != lines.Type {
			t.Errorf("%s: key %s type %q frames %+v, want %s", name, got.Key, got.Type, got.Frames, lines.Key)
		}
	}
	// However many of its first frames have no line: the notation is voted
	// on the first lines alone.
	var mixed strings.Builder
	mixed.WriteString("java.lang.IllegalStateException: checkout failed\n")
	for i := range 9 {
		fmt.Fprintf(&mixed, "\tat shop.Step%d.run(Step%d.java)\n", i, i)
	}
	for i := range 26 {
		fmt.Fprintf(&mixed, "\tat shop.Deep%d.run(Deep%d.java:%d)\n", i, i, i+1)
	}
	if got := ParseStackFrames(mixed.String()); len(got) != 35 || got[0].Syntax != StackJava || got[0].Module != "shop.Step0" {
		t.Errorf("nine frames without lines above 26 with: %d frames, the first %+v", len(got), got[:min(1, len(got))])
	}
	// A .NET parameter list still reads as one, whatever its types end in.
	for _, params := range []string{"System.String", "Shop.Java", "Shop.Source.java, Int32", "SourceFile file"} {
		stack := "Example.Shop.CheckoutException: failed\n ---> System.InvalidOperationException: Cart 7 is locked\n" +
			"   at Example.Shop.Util.Check(" + params + ")\n   at Example.Shop.Cart.Save(" + params + ")\n" +
			"   --- End of inner exception stack trace ---\n   at Example.Shop.Checkout.Run(" + params + ")"
		got, typ, _ := parseStack(stack)
		if want := frames(StackDotNet, "Example.Shop.Util", "Check", "Example.Shop.Cart", "Save"); !reflect.DeepEqual(got, want) ||
			typ != "System.InvalidOperationException" {
			t.Errorf("(%s): frames %+v type %q", params, got, typ)
		}
	}
	for line, want := range map[string]bool{
		"at a.B.c(B.java)": true, "at a.B.c(B.kt)": true, "at a.B.c(build.gradle.kts)": true, "at a.B.c(B.scala)": true,
		"at a.B.c(B.groovy)": true, "at a.B.c(b.clj)": true, "at a.B.c(SourceFile)": true,
		"at a.B.c(B.javax)": false, "at a.B.c(System.String)": false, "at a.B.c(java)": false, "at a.B.c(SourceFiles)": false,
		"at a.B.c(B.Java)": false,
	} {
		if got := javaOwns(line); got != want {
			t.Errorf("javaOwns(%q) = %v, want %v", line, got, want)
		}
	}
}

// "... N more" counts the frames the JVM printed. A frame the reader cannot
// read (a module version with "+") is one of them: it holds its place in
// the count and is left out of the result, so a chain reads the same
// compressed and written out, for every length of the shared tail.
func TestJavaMoreCountsFramesItCannotRead(t *testing.T) {
	enclosing := []string{
		"\tat com.example.shop.Wrap.wrap(Wrap.java:9)\n",
		"\tat dep@1.0+7/vendor.Lib.call(Lib.java:3)\n",
		"\tat com.example.shop.Api.post(Api.java:4)\n",
		"\tat dep@1.0+7/vendor.Lib.run(Lib.java:8)\n",
	}
	const head = "com.example.shop.WrapException: wrapped\n"
	const cause = "Caused by: java.lang.IllegalStateException: Cart 7 is locked\n\tat com.example.shop.Cart.fail(Cart.java:12)\n"
	for shared := 0; shared <= len(enclosing); shared++ {
		tail := strings.Join(enclosing[len(enclosing)-shared:], "")
		written := mustFingerprint(t, Event{Type: "T", StackTrace: head + strings.Join(enclosing, "") + cause + tail})
		compressed := mustFingerprint(t, Event{Type: "T",
			StackTrace: head + strings.Join(enclosing, "") + cause + fmt.Sprintf("\t... %d more\n", shared)})
		if written.Key != compressed.Key || !reflect.DeepEqual(written.Frames, compressed.Frames) {
			t.Errorf("%d shared: written out %s %+v, compressed %s %+v", shared, written.Key, written.Frames, compressed.Key, compressed.Frames)
		}
		for _, f := range compressed.Frames {
			if strings.Contains(f.Module, "vendor") || f.Module == javaSyntheticClass {
				t.Errorf("%d shared: a frame that cannot be read is in the key: %+v", shared, compressed.Frames)
			}
		}
	}
	// Only a line the JVM starts a frame with holds a place, and only below
	// a frame: a line of a message that starts with "at" is no frame, so a
	// cause that has nothing else is still one without frames, and an
	// empty line counts for nothing.
	for name, stack := range map[string]string{
		"prose in a cause without frames": head + "\tat com.example.shop.Wrap.wrap(Wrap.java:9)\n\tat com.example.shop.Api.post(Api.java:4)\n" +
			"Caused by: com.example.shop.StepException: step failed\nat least one cart (of 3)\n" +
			"Caused by: java.lang.IllegalStateException: locked\n\tat com.example.shop.Cart.fail(Cart.java:12)\n\t... 1 more\n",
		"an empty line below the frames": head + "\tat com.example.shop.Wrap.wrap(Wrap.java:9)\n\tat com.example.shop.Api.post(Api.java:4)\n\n" +
			"Caused by: java.lang.IllegalStateException: locked\n\tat com.example.shop.Cart.fail(Cart.java:12)\n\t... 1 more\n",
	} {
		got, _, _ := parseStack(stack)
		if want := frames(StackJava, "com.example.shop.Cart", "fail", "com.example.shop.Api", "post"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

// A line of an exception's message that starts with "Caused by:" and names
// no type leaves the type and the key alone.
func TestJavaProseCauseLineChangesNothing(t *testing.T) {
	trace := func(extra string) string {
		return "java.lang.RuntimeException: request failed\n\tat com.example.shop.Api.post(Api.java:4)\n" +
			"Caused by: java.lang.IllegalArgumentException: write failed\n" + extra +
			"\tat com.example.shop.Cart.save(Cart.java:12)\n\t... 1 more\n"
	}
	plain := mustFingerprint(t, Event{Type: "java.lang.RuntimeException", StackTrace: trace("")})
	prose := mustFingerprint(t, Event{Type: "java.lang.RuntimeException", StackTrace: trace("Caused by: connection reset by peer\n")})
	if plain.Type != "java.lang.IllegalArgumentException" || prose.Type != plain.Type || prose.Key != plain.Key {
		t.Errorf("plain %s (%s), with a line of prose %s (%s)", plain.Key, plain.Type, prose.Key, prose.Type)
	}
}

// A suppressed exception is left out with everything in it. Its message
// may take a second line, which the JVM prints at the left margin: that
// ends nothing, only a line of the exception around the block does. The
// traces are OpenJDK 21's, of a resource whose close throws.
func TestJavaSuppressedBlockWithATwoLineMessage(t *testing.T) {
	const second = "second line\n"
	plain := func(extra string) string {
		return "java.lang.IllegalArgumentException: no stock\n\tat Sup.run(Sup.java:9)\n\tat Sup.main(Sup.java:18)\n" +
			"\tSuppressed: java.lang.IllegalStateException: close failed\n" + extra +
			"\t\tat Sup$Conn.close(Sup.java:5)\n\t\tat Sup.run(Sup.java:8)\n\t\t... 1 more\n"
	}
	chain := func(extra string) string {
		return "java.lang.RuntimeException: checkout failed\n\tat Sup.outer(Sup.java:13)\n\tat Sup.main(Sup.java:20)\n" +
			"Caused by: java.lang.IllegalArgumentException: no stock\n\tat Sup.run(Sup.java:9)\n\t... 2 more\n" +
			"\tSuppressed: java.lang.IllegalStateException: close failed\n" + extra +
			"\t\tat Sup$Conn.close(Sup.java:5)\n\t\tat Sup.run(Sup.java:8)\n\t\t... 2 more\n"
	}
	for name, tc := range map[string]struct {
		stack string
		want  []StackFrame
	}{
		"one line":           {plain(""), frames(StackJava, "Sup", "run", "Sup", "main")},
		"two lines":          {plain(second), frames(StackJava, "Sup", "run", "Sup", "main")},
		"in a chain":         {chain(""), frames(StackJava, "Sup", "run", "Sup", "outer", "Sup", "main")},
		"two lines in chain": {chain(second), frames(StackJava, "Sup", "run", "Sup", "outer", "Sup", "main")},
		// What follows the block at the indentation of the exception
		// around it is that exception's again.
		"a cause after the block": {plain(second) + "Caused by: java.io.IOException: disk\n\tat Sup.read(Sup.java:30)\n\t... 2 more\n",
			frames(StackJava, "Sup", "read", "Sup", "run", "Sup", "main")},
		"a bare frame after it":  {plain(second) + "Sup.late (Sup.java:40)\n", frames(StackJava, "Sup", "run", "Sup", "main", "Sup", "late")},
		"a frame count after it": {chain(second) + "... 1 more\n", frames(StackJava, "Sup", "run", "Sup", "main")},
	} {
		if got := ParseStackFrames(tc.stack); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}

// Lines that only look like the JVM's: a count of something else in a
// message, a cause that names a number.
func TestJavaLinesOfAMessageThatReadLikeStructure(t *testing.T) {
	const enclosing = "com.example.shop.WrapException: wrapped\n\tat com.example.shop.Wrap.wrap(Wrap.java:9)\n\tat com.example.shop.Api.post(Api.java:4)\n"
	got, typ, _ := parseStack(enclosing + "Caused by: java.lang.IllegalStateException: rejected\n... 2 more errors in payload\n" +
		"\tat com.example.shop.Cart.fail(Cart.java:12)\n")
	if want := frames(StackJava, "com.example.shop.Cart", "fail"); !reflect.DeepEqual(got, want) || typ != "java.lang.IllegalStateException" {
		t.Errorf("a count in a message: %+v (%s), want %+v", got, typ, want)
	}
	for _, more := range []string{"... 1 more", "... 1 common frames omitted"} {
		got, _, _ = parseStack(enclosing + "Caused by: java.lang.IllegalStateException: rejected\n\tat com.example.shop.Cart.fail(Cart.java:12)\n\t" + more + "\n")
		if want := frames(StackJava, "com.example.shop.Cart", "fail", "com.example.shop.Api", "post"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", more, got, want)
		}
	}
	// No type starts with a digit: the line is one of the message.
	a := mustFingerprint(t, Event{Type: "T", StackTrace: enclosing + "Caused by: java.io.IOException: upstream\n" +
		"Caused by: 503: Service Unavailable\n\tat com.example.shop.Http.get(Http.java:7)\n\t... 1 more\n"})
	if a.Type != "java.io.IOException" {
		t.Errorf("a status after Caused by: type %q", a.Type)
	}
	for _, line := range []string{"Caused by: 503: Service Unavailable", "Caused by: 503", "Caused by: 4xx.Error: x"} {
		if javaCauseRe.MatchString(line) {
			t.Errorf("%q names a type", line)
		}
	}
	for _, line := range []string{"Caused by: _T: x", "Caused by: $Proxy12", "Caused by: a.B2: x"} {
		if !javaCauseRe.MatchString(line) {
			t.Errorf("%q names no type", line)
		}
	}
}

// Generated names of Scala and of Android's D8, written by hand from the
// compilers' naming: a closure of a method named in any script, and the
// class D8 generates for a lambda, which is plumbing like the JVM's.
func TestJavaGeneratedNamesOfOtherCompilers(t *testing.T) {
	for line, want := range map[string][2]string{
		"at shop.Cart$$anonfun$size$1.apply(Cart.scala:12)":                {"shop.Cart", "size"},
		"at shop.Cart$$anonfun$größe$1.apply(Cart.scala:12)":               {"shop.Cart", "größe"},
		"at shop.Cart$$anonfun$größe$2.apply(Cart.scala:14)":               {"shop.Cart", "größe"},
		"at shop.Cart.$anonfun$größe$1(Cart.scala:12)":                     {"shop.Cart", "größe"},
		"at shop.Cart.$anonfun$größe$3$adapted(Cart.scala:12)":             {"shop.Cart", "größe"},
		"at shop.Cart$$ExternalSyntheticLambda0.run(D8$$SyntheticClass:0)": {javaSyntheticClass, "run"},
		"at shop.Cart$$ExternalSyntheticLambda3.run(Unknown Source:2)":     {javaSyntheticClass, "run"},
	} {
		f, ok := javaFrame(line)
		if !ok || f.Module != want[0] || f.Function != want[1] {
			t.Errorf("%s: %q %q %v, want %q %q", line, f.Module, f.Function, ok, want[0], want[1])
		}
	}
	trace := func(n string) string {
		return "java.lang.IllegalStateException: locked\n\tat shop.Cart.lambda$add$0(Cart.java:12)\n" +
			"\tat shop.Cart$$ExternalSyntheticLambda" + n + ".run(Unknown Source:2)\n\tat android.os.Handler.handleCallback(Handler.java:942)\n"
	}
	a := mustFingerprint(t, Event{Type: "T", StackTrace: trace("0")})
	b := mustFingerprint(t, Event{Type: "T", StackTrace: trace("3")})
	if want := frames(StackJava, "shop.Cart", "add"); a.Key != b.Key || !reflect.DeepEqual(a.Frames, want) {
		t.Errorf("two D8 lambda ordinals: %s %+v and %s", a.Key, a.Frames, b.Key)
	}
}

// javaChain is a trace of n causes, each sharing one frame more with the
// exception around it than the one before.
func javaChain(n int) string {
	var b strings.Builder
	b.WriteString("com.example.shop.CheckoutException: checkout failed\n\tat com.example.shop.Main.main(Main.java:5)\n")
	for i := range n {
		fmt.Fprintf(&b, "Caused by: com.example.shop.StepException: step failed\n\tat com.example.shop.Step.run(Step.java:%d)\n\t... %d more\n", i, i+1)
	}
	return b.String()
}

// Reading a chain costs in proportion to its length: twice the causes must
// not take four times the memory.
func TestJavaChainIsReadInLinearSpace(t *testing.T) {
	// Measured over the runs of a benchmark: one cold call would count
	// whatever else the process allocates meanwhile.
	allocated := func(stack string) int64 {
		return testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if got := ParseStackFrames(stack); len(got) != 2 {
					b.Fatalf("%d frames, want the root cause's and the one all share", len(got))
				}
			}
		}).AllocedBytesPerOp()
	}
	short, long := javaChain(2000), javaChain(4000)
	if a, b := allocated(short), allocated(long); a == 0 || b > 3*a {
		t.Errorf("%d bytes for 2000 causes, %d for 4000: more than three times as much", a, b)
	}
}

// What was raised stands at the column of its traceback, the source text of
// a frame deeper. That, not the order of the lines, tells the two apart: a
// frame may have several source lines or none.
func TestPythonRootCauseReadsIndentation(t *testing.T) {
	const wrapper = `
During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
    cart.add_item(sku)
RuntimeError: failed`
	addItem := frames(StackPython, "shop/cart.py", "add_item")
	cases := []struct {
		name, stack string
		want        []StackFrame
		typ         string
	}{
		{"a call over several source lines", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    result = self.repo.save(
        sku,
        order_id
    )
ValueError: locked
` + wrapper, addItem, "ValueError"},
		{"source lines with markers under them", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    result = self.repo.save(
             ~~~~~~~~~~~~~~^
        sku,
        ^^^^
        order_id
        ^^^^^^^^
    )
    ^
ValueError: locked
` + wrapper, addItem, "ValueError"},
		{"no source lines", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
KeyError: 'sku'

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
RuntimeError: failed`, addItem, "KeyError"},
		{"no source lines, several frames", `Traceback (most recent call last):
  File "/app/shop/api.py", line 17, in post
  File "/app/shop/cart.py", line 42, in add_item
KeyError: 'sku'

The above exception was the direct cause of the following exception:

Traceback (most recent call last):
  File "/app/shop/web.py", line 5, in handle
RuntimeError: failed`, frames(StackPython, "shop/cart.py", "add_item", "shop/api.py", "post"), "KeyError"},
		{"a first sub-exception that was never raised", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     raise ExceptionGroup("checkout", errors)
  | ExceptionGroup: checkout (2 sub-exceptions)
  +-+---------------- 1 ----------------
    | ValueError: locked
    +---------------- 2 ----------------
    | Traceback (most recent call last):
    |   File "/app/shop/retry.py", line 9, in later
    |     raise LookupError(sku)
    | LookupError: 'kettle'
    +------------------------------------`, frames(StackPython, "shop/api.py", "post"), "ExceptionGroup"},
		{"a group in a group whose first member was never raised", `  + Exception Group Traceback (most recent call last):
  |   File "/app/shop/api.py", line 17, in post
  |     raise ExceptionGroup("checkout", errors)
  | ExceptionGroup: checkout (1 sub-exception)
  +-+---------------- 1 ----------------
    | Exception Group Traceback (most recent call last):
    |   File "/app/shop/cart.py", line 42, in add_all
    |     raise ExceptionGroup("cart", errors)
    | ExceptionGroup: cart (2 sub-exceptions)
    +-+---------------- 1 ----------------
      | ValueError: locked
      +---------------- 2 ----------------
      | Traceback (most recent call last):
      |   File "/app/shop/retry.py", line 9, in later
      |     raise LookupError(sku)
      | LookupError: 'kettle'
      +------------------------------------`, frames(StackPython, "shop/cart.py", "add_all"), "ExceptionGroup"},
		// Where a logger stripped the indentation, the line after a frame is
		// its source text and the next one what was raised.
		{"indentation stripped", `Traceback (most recent call last):
File "/app/shop/cart.py", line 42, in add_item
raise ValueError("locked")
ValueError: locked

During handling of the above exception, another exception occurred:

Traceback (most recent call last):
File "/app/shop/api.py", line 17, in post
cart.add_item(sku)
RuntimeError: failed`, addItem, "ValueError"},
		{"a source line that names a traceback", `Traceback (most recent call last):
  File "/app/shop/cart.py", line 42, in add_item
    log("Traceback (most recent call last)")
ValueError: locked
` + wrapper, addItem, "ValueError"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ, _ := parseStack(tc.stack)
			if !reflect.DeepEqual(got, tc.want) || typ != tc.typ {
				t.Errorf("frames %+v type %q\nwant %+v %q", got, typ, tc.want, tc.typ)
			}
		})
	}
	// The key of a chain is that of its root cause alone, with or without
	// source lines.
	chain := mustFingerprint(t, Event{Type: "RuntimeError", StackTrace: cases[2].stack})
	alone := mustFingerprint(t, Event{Type: "KeyError",
		StackTrace: "Traceback (most recent call last):\n  File \"/app/shop/cart.py\", line 42, in add_item\nKeyError: 'sku'"})
	if chain.Key != alone.Key || chain.Type != "KeyError" {
		t.Errorf("chain %s (%s), root cause alone %s", chain.Key, chain.Type, alone.Key)
	}
}

// The traces below are CPython 3.14's, of a script deployed under /srv.

// pythonGroup is a group whose only member is a group that was never
// raised, so the outer group's traceback is all there is.
func pythonGroup(fn, line string) string {
	return `  + Exception Group Traceback (most recent call last):
  |   File "/srv/app/shop.py", line 50, in <module>
  |     fn()
  |     ~~^^
  |   File "/srv/app/shop.py", line ` + line + `, in ` + fn + `
  |     raise ExceptionGroup("batch", [ExceptionGroup("part", [ValueError("missing")])])
  | ExceptionGroup: batch (1 sub-exception)
  +-+---------------- 1 ----------------
    | ExceptionGroup: part (1 sub-exception)
    +-+---------------- 1 ----------------
      | ValueError: missing
      +------------------------------------
`
}

// pythonChain is an exception raised in fn and wrapped in a RuntimeError.
func pythonChain(fn, raise, raised string) string {
	return pythonRaised(fn, raise, raised) + `
The above exception was the direct cause of the following exception:

Traceback (most recent call last):
  File "/srv/app/shop.py", line 50, in <module>
    fn()
    ~~^^
  File "/srv/app/shop.py", line 24, in chain
    raise RuntimeError("wrapped") from e
RuntimeError: wrapped
`
}

func pythonRaised(fn, raise, raised string) string {
	return `Traceback (most recent call last):
  File "/srv/app/shop.py", line 22, in chain
    f()
    ~^^
  File "/srv/app/shop.py", line 12, in ` + fn + `
    ` + raise + `
` + raised + `
`
}

// A group whose first member has no traceback keeps its own as the root,
// also where that member is a group itself.
func TestPythonNestedGroupKeepsTheOuterTraceback(t *testing.T) {
	alpha := mustFingerprint(t, Event{Type: "ExceptionGroup", Message: "batch (1 sub-exception)", StackTrace: pythonGroup("alpha", "4")})
	beta := mustFingerprint(t, Event{Type: "ExceptionGroup", Message: "batch (1 sub-exception)", StackTrace: pythonGroup("beta", "7")})
	if want := frames(StackPython, "app/shop.py", "alpha", "app/shop.py", "<module>"); alpha.Rung != RungAppFrames ||
		!reflect.DeepEqual(alpha.Frames, want) || alpha.Type != "ExceptionGroup" {
		t.Errorf("rung %v, type %q, frames %+v, want %+v", alpha.Rung, alpha.Type, alpha.Frames, want)
	}
	if beta.Rung != RungAppFrames || alpha.Key == beta.Key {
		t.Errorf("two throw sites: %s on rung %v and %s on rung %v", alpha.Key, alpha.Rung, beta.Key, beta.Rung)
	}
}

// What was raised is named as Python names a class: in any script, and
// with the function it is local to.
func TestPythonRaisedTypeNames(t *testing.T) {
	for _, tc := range []struct{ fn, raise, raised, typ string }{
		{"op", `raise Failure("local failure")`, "op.<locals>.Failure: local failure", "op.<locals>.Failure"},
		{"echec", `raise Échec("raté")`, "Échec: raté", "Échec"},
		{"quoted", `raise ValueError("saw Traceback (most recent call last) in output")`,
			"ValueError: saw Traceback (most recent call last) in output", "ValueError"},
		{"quoted", `raise ValueError(log)`, "ValueError: the log says: Traceback (most recent call last):", "ValueError"},
		{"bare", "raise shop.errors.Échec2", "shop.errors.Échec2", "shop.errors.Échec2"},
	} {
		chain := mustFingerprint(t, Event{Type: "RuntimeError", StackTrace: pythonChain(tc.fn, tc.raise, tc.raised)})
		alone := mustFingerprint(t, Event{Type: tc.typ, StackTrace: pythonRaised(tc.fn, tc.raise, tc.raised)})
		if chain.Type != tc.typ || chain.Key != alone.Key {
			t.Errorf("%s: the chain is %s (%s), its root alone %s (%s)", tc.fn, chain.Key, chain.Type, alone.Key, alone.Type)
		}
	}
	// Where the indentation was stripped the line after a frame is its
	// source text, and the first line after that which reads like a name is
	// what was raised. That holds in any script, for the type and for the
	// second source line of a statement, which is then taken for it.
	strip := func(stack string) string {
		lines := strings.Split(stack, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}
		return strings.Join(lines, "\n")
	}
	if _, typ, _ := parseStack(strip(pythonChain("echec", `raise Échec("raté")`, "Échec: raté"))); typ != "Échec" {
		t.Errorf("stripped: type %q", typ)
	}
	if _, typ, _ := parseStack(strip(pythonChain("echec", "raise Échec(\n    détail", "Échec: raté"))); typ != "détail" {
		t.Errorf("stripped, a second source line: type %q; if it is the raised type now, say so in the package's known gaps", typ)
	}
}

// An exception that is handled by raising a group is still the root: the
// group's header starts another traceback, however deep it is indented.
func TestPythonGroupRaisedWhileHandling(t *testing.T) {
	const root = `Traceback (most recent call last):
  File "/srv/app/shop.py", line 34, in wrap
    root()
    ~~~~^^
  File "/srv/app/shop.py", line 27, in root
    raise ValueError("no stock")
ValueError: no stock
`
	const handled = root + `
During handling of the above exception, another exception occurred:

  + Exception Group Traceback (most recent call last):
  |   File "/srv/app/shop.py", line 50, in <module>
  |     fn()
  |     ~~^^
  |   File "/srv/app/shop.py", line 41, in wrap
  |     raise ExceptionGroup("cleanup failed", errs)
  | ExceptionGroup: cleanup failed (1 sub-exception)
  +-+---------------- 1 ----------------
    | Traceback (most recent call last):
    |   File "/srv/app/shop.py", line 38, in wrap
    |     sub1()
    |     ~~~~^^
    |   File "/srv/app/shop.py", line 30, in sub1
    |     raise KeyError("sku")
    | KeyError: 'sku'
    +------------------------------------
`
	alone := mustFingerprint(t, Event{Type: "ValueError", StackTrace: root})
	group := mustFingerprint(t, Event{Type: "ExceptionGroup", StackTrace: handled})
	if want := frames(StackPython, "app/shop.py", "root", "app/shop.py", "wrap"); group.Type != "ValueError" ||
		!reflect.DeepEqual(group.Frames, want) || group.Key != alone.Key {
		t.Errorf("handled by a group: %s (%s) %+v, the root alone %s", group.Key, group.Type, group.Frames, alone.Key)
	}
}

// An arrow introduces an inner exception only where a qualified type and a
// colon, or the end of the line, follow it; and an inner exception without
// frames is not the one the frames under it belong to.
func TestDotNetInnerExceptionTypes(t *testing.T) {
	moved := func(state string) string {
		return "Example.Shop.StateException: order moved Open ---> " + state +
			"\n   at Example.Shop.Orders.Move(String id)\n   at Example.Shop.Api.Post()"
	}
	cases := []struct {
		name, stack string
		want        []StackFrame
		typ         string
	}{
		{"an arrow in a message", moved("Pending"), frames(StackDotNet, "Example.Shop.Orders", "Move", "Example.Shop.Api", "Post"), ""},
		{"an arrow before a word and a colon", moved("Pending: retry later"),
			frames(StackDotNet, "Example.Shop.Orders", "Move", "Example.Shop.Api", "Post"), ""},
		{"an arrow before a qualified name in a sentence", moved("Example.Shop.Pending after a retry"),
			frames(StackDotNet, "Example.Shop.Orders", "Move", "Example.Shop.Api", "Post"), ""},
		{"an inner exception without frames", `Example.Shop.CheckoutException: failed
 ---> Example.Shop.Inner: thrown without a stack
   --- End of inner exception stack trace ---
   at Example.Shop.Checkout.Run()`, frames(StackDotNet, "Example.Shop.Checkout", "Run"), ""},
		{"the innermost of three has no frames", `Example.Shop.CheckoutException: failed
 ---> Example.Shop.CartException: cart failed
 ---> Example.Shop.Inner: thrown without a stack
   --- End of inner exception stack trace ---
   at Example.Shop.Cart.Add(String sku)
   --- End of inner exception stack trace ---
   at Example.Shop.Checkout.Run()`, frames(StackDotNet, "Example.Shop.Cart", "Add"), "Example.Shop.CartException"},
		{"a type without a message", "Example.Shop.CheckoutException: failed\n ---> System.InvalidOperationException\n" +
			"   at Example.Shop.Cart.Add(String sku)\n   --- End of inner exception stack trace ---\n   at Example.Shop.Checkout.Run()",
			frames(StackDotNet, "Example.Shop.Cart", "Add"), "System.InvalidOperationException"},
		{"a nested and a generic type", "Example.Shop.CheckoutException: failed\n ---> Example.Shop.Cart+LockedException`1[System.String]: locked\n" +
			"   at Example.Shop.Cart.Add(String sku)\n   --- End of inner exception stack trace ---\n   at Example.Shop.Checkout.Run()",
			frames(StackDotNet, "Example.Shop.Cart", "Add"), "Example.Shop.Cart+LockedException`1"},
		// .NET prints an inner exception on a line of its own, so there a
		// type needs no namespace to be told from a word of the message.
		{"a type without a namespace on its own line", `Example.Shop.OrderException: order failed
 ---> CartLockedException: cart 7
   at Example.Shop.Cart.Lock()
   --- End of inner exception stack trace ---
   at Example.Shop.Orders.Place()`, frames(StackDotNet, "Example.Shop.Cart", "Lock"), "CartLockedException"},
		{"a generic type without a namespace, in an aggregate", "System.AggregateException: One or more errors occurred. (cart 7)\n" +
			" ---> (Inner Exception #0) Locked`1[System.String]: cart 7<---\n   at Example.Shop.Cart.Lock()",
			frames(StackDotNet, "Example.Shop.Cart", "Lock"), "Locked`1"},
		{"a word without a colon on its own line", "Example.Shop.StateException: order moved Open\n ---> Pending\n" +
			"   at Example.Shop.Orders.Move(String id)", frames(StackDotNet, "Example.Shop.Orders", "Move"), ""},
		{"words and a colon on their own line", "Example.Shop.StateException: order moved Open\n ---> Pending now: retry\n" +
			"   at Example.Shop.Orders.Move(String id)", frames(StackDotNet, "Example.Shop.Orders", "Move"), ""},
		{"a qualified type on its own line is read once", "Example.Shop.OrderException: order failed\n" +
			" ---> Example.Shop.CartLockedException: cart 7\n   --- End of inner exception stack trace ---\n   at Example.Shop.Orders.Place()",
			frames(StackDotNet, "Example.Shop.Orders", "Place"), ""},
		{"three deep, the innermost without a namespace or frames", `System.Exception: outer
 ---> MyApp.OrderException: order failed
 ---> CartLockedException: cart 7
   --- End of inner exception stack trace ---
   at MyApp.Order.Submit()
   --- End of inner exception stack trace ---
   at MyApp.Api.Post()`, frames(StackDotNet, "MyApp.Order", "Submit"), "MyApp.OrderException"},
		// AggregateException.ToString prints its first inner exception as
		// any exception does, then every further one behind an arrow of
		// its own and closed by "<---". Those are siblings of the first,
		// not causes of it.
		{"an aggregate whose first inner exception has no frames", `System.AggregateException: One or more errors occurred. (cart 7) (query failed)
 ---> MyApp.CartException: cart 7
   --- End of inner exception stack trace ---
   at MyApp.Order.Submit()
 ---> (Inner Exception #1) MyApp.DbException: query failed
   at MyApp.Db.Query()<---
`, frames(StackDotNet, "MyApp.Order", "Submit"), ""},
		{"an aggregate that was never thrown", `System.AggregateException: One or more errors occurred. (cart 7) (query failed)
 ---> MyApp.CartException: cart 7
   --- End of inner exception stack trace ---
 ---> (Inner Exception #1) MyApp.DbException: query failed
   at MyApp.Db.Query()<---
`, frames(StackDotNet, "MyApp.Db", "Query"), "MyApp.DbException"},
		{"a type in another script", "Boutique.CommandeException: échec\n ---> Boutique.Panier.ÉchecException: verrouillé\n" +
			"   at Boutique.Panier.Ajouter(String sku)\n   --- End of inner exception stack trace ---\n   at Boutique.Commande.Lancer()",
			frames(StackDotNet, "Boutique.Panier", "Ajouter"), "Boutique.Panier.ÉchecException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ, _ := parseStack(tc.stack)
			if !reflect.DeepEqual(got, tc.want) || typ != tc.typ {
				t.Errorf("frames %+v type %q\nwant %+v %q", got, typ, tc.want, tc.typ)
			}
		})
	}
	a := mustFingerprint(t, Event{Type: "Example.Shop.StateException", StackTrace: moved("Pending")})
	b := mustFingerprint(t, Event{Type: "Example.Shop.StateException", StackTrace: moved("Closed")})
	if a.Key != b.Key || a.Type != "Example.Shop.StateException" {
		t.Errorf("a word of the message is in the key: %s (%s) and %s (%s)", a.Key, a.Type, b.Key, b.Type)
	}
}

// Compiler-generated names carry ordinals that move with every edit of the
// class, and no two builds agree on them.
func TestDotNetGeneratedNames(t *testing.T) {
	cases := []struct{ line, module, function string }{
		{"at Example.Shop.Cart.<>c.<<AddItem>b__5_0>d__12.MoveNext()", "Example.Shop.Cart", "AddItem"},
		{"at Example.Shop.Cart.<AddItemAsync>d__12`1.MoveNext()", "Example.Shop.Cart", "AddItemAsync"},
		{"at Example.Shop.Cart.<>c__DisplayClass5_0`1.<AddItem>b__0(Item item)", "Example.Shop.Cart", "AddItem"},
		{"at Example.Shop.Cart.<>c__DisplayClass5_0`1.<<AddItem>b__0>d.MoveNext()", "Example.Shop.Cart", "AddItem"},
		{"at Example.Shop.Cart.<Create>g__Validate|1_0`1(Int32 quantity)", "Example.Shop.Cart.Create", "Validate"},
		{"at Example.Shop.Cart.<Cancel>g__Validate|3_1(Int32 quantity)", "Example.Shop.Cart.Cancel", "Validate"},
		{"at Example.Shop.Cart.<<Create>g__Validate|1_0>g__Check|1_1(Int32 quantity)", "Example.Shop.Cart.Create.Validate", "Check"},
		{"at Example.Shop.CartService+<AddItemAsync>d__12.MoveNext () [0x0001a] in <3f2b8c1e>:0", "Example.Shop.CartService", "AddItemAsync"},
		{"at Example.Shop.CartService+<>c__DisplayClass5_0.<AddItem>b__0 (Example.Shop.Item item) [0x00007] in <3f2b8c1e>:0",
			"Example.Shop.CartService", "AddItem"},
		{"at Example.Shop.Cart+Line.Total () [0x00000] in <3f2b8c1e>:0", "Example.Shop.Cart.Line", "Total"},
		// Three deep, each kind once.
		{"at Example.Shop.Cart.<<<Run>b__4_0>g__Check|4_1>d__3.MoveNext()", "Example.Shop.Cart.Run", "Check"},
		// Brackets that hold no method name, or that nothing generated
		// follows, stay as they are written, and so does what wraps them.
		{"at Example.Shop.Cart.<Add|x>b__0()", "Example.Shop.Cart", "<Add|x>b__0"},
		{"at Example.Shop.Cart.<>b__0()", "Example.Shop.Cart", "<>b__0"},
		{"at Example.Shop.Cart.<<Add>x>b__0()", "Example.Shop.Cart", "<<Add>x>b__0"},
		{"at Example.Shop.Cart.<Add>b__()", "Example.Shop.Cart", "<Add>b__"},
		{"at Example.Shop.Cart.<Add>g__|1_0()", "Example.Shop.Cart", "<Add>g__|1_0"},
		{"at Example.Shop.Cart.<Add>g__Check|()", "Example.Shop.Cart", "<Add>g__Check|"},
		{"at Example.Shop.Cart.<Add>d__.MoveNext()", "Example.Shop.Cart.<Add>d__", "MoveNext"},
		{"at Example.Shop.Cart.<Add>d__12.Dispose()", "Example.Shop.Cart.<Add>d__12", "Dispose"},
		{"at Example.Shop.Cart.<Add>d__12.MoveNextStep()", "Example.Shop.Cart.<Add>d__12", "MoveNextStep"},
		{"at Example.Shop.Cart.Add>b__0()", "Example.Shop.Cart", "Add>b__0"},
		{"at Example.Shop.Cart.<Step2>g__Check3|1_0()", "Example.Shop.Cart.Step2", "Check3"},
		// A method that implements an interface explicitly is named with it.
		{"at Example.Shop.Cart.<Example.Shop.ICart.Add>b__3_0()", "Example.Shop.Cart.Example.Shop.ICart", "Add"},
		// In the name of a generated type the compiler writes the dots of
		// the interface as hyphens. Written by hand from that rule.
		{"at Example.Shop.Cart.<Example-Shop-ICart-AddItemAsync>d__12.MoveNext()", "Example.Shop.Cart.Example.Shop.ICart", "AddItemAsync"},
		{"at Example.Shop.Cart.<Example-Shop-ICart-AddItemAsync>d__13.MoveNext()", "Example.Shop.Cart.Example.Shop.ICart", "AddItemAsync"},
		{"at Example.Shop.Cart.<Add-Item>x()", "Example.Shop.Cart", "<Add-Item>x"},
		{"at Example.Shop.Cart.<Add-Item>x.<Run>b__0()", "Example.Shop.Cart.<Add-Item>x", "Run"},
		{"at Example.Shop.Cart.<<Example-Shop-ICart-Add>b__0>d.MoveNext()", "Example.Shop.Cart.Example.Shop.ICart", "Add"},
		// The ordinals end where the next name starts.
		{"at Example.Shop.Cart.<Run>g__Check|1_0<Add>b__2()", "Example.Shop.Cart.Run", "CheckAdd"},
		// The closure class of a generic method.
		{"at MyApp.Cart.<>c__5`1.<Add>b__5_0(Item item)", "MyApp.Cart", "Add"},
		{"at MyApp.Cart.<>c__6`1.<Add>b__6_0(Item item)", "MyApp.Cart", "Add"},
		{"at MyApp.Cart.<>c__x.<Add>b__6_0(Item item)", "MyApp.Cart.<>c__x", "Add"},
		// A name in any script: an ordinal behind it goes like any other.
		{"at Boutique.Panier.<Réserver>d__12.MoveNext()", "Boutique.Panier", "Réserver"},
		{"at Boutique.Panier.<Réserver>d__13.MoveNext()", "Boutique.Panier", "Réserver"},
		{"at Boutique.Panier.<>c.<Réserver>b__5_0(Article a)", "Boutique.Panier", "Réserver"},
		{"at Boutique.Panier.<Réserver>g__Vérifie|5_1(Int32 n)", "Boutique.Panier.Réserver", "Vérifie"},
		{"at Boutique.Programme.<Démarrer>$(String[] args)", "Boutique.Programme", "Démarrer"},
		{"at Магазин.Корзина.<<Добавить>b__0>d.MoveNext()", "Магазин.Корзина", "Добавить"},
	}
	for _, tc := range cases {
		f, ok := dotnetFrame(tc.line)
		if !ok || f.Module != tc.module || f.Function != tc.function {
			t.Errorf("%s\n got %q %q %v\nwant %q %q", tc.line, f.Module, f.Function, ok, tc.module, tc.function)
		}
	}
}

// dotnetNestedTrace is a .NET trace of the given number of frame lines, each
// a lambda nested in a lambda to the given depth.
func dotnetNestedTrace(depth, lines int) string {
	return "System.InvalidOperationException: Cart 7 is locked\n" + strings.Repeat(
		"   at Example.Shop.Cart."+strings.Repeat("<", depth)+"Run"+strings.Repeat(">b__0", depth)+"(Item item)\n", lines)
}

// A generated name is unwrapped in one pass, however deep it nests: the
// work and the memory are in proportion to the text, at any depth a line
// has room for.
func TestDotNetNestedNamesAreReadInLinearTime(t *testing.T) {
	want := frames(StackDotNet, "Example.Shop.Cart", "Run")
	cost := func(stack string) (time.Duration, uint64) {
		t.Helper()
		best, least := time.Duration(0), uint64(0)
		for i := range 3 {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			got := ParseStackFrames(stack)
			took := time.Since(start)
			runtime.ReadMemStats(&after)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("frames %+v, want %+v", got, want)
			}
			if allocated := after.TotalAlloc - before.TotalAlloc; i == 0 || allocated < least {
				least = allocated
			}
			if i == 0 || took < best {
				best = took
			}
		}
		return best, least
	}
	// As deep as a line can hold, on as many lines as the text limit takes.
	depth := (stackLineMax - len(dotnetNestedTrace(0, 1))) / len("<>b__0")
	line := len(dotnetNestedTrace(depth, 1)) - len(dotnetNestedTrace(depth, 0))
	worst := dotnetNestedTrace(depth, (stackTextMax-len(dotnetNestedTrace(depth, 0)))/line)
	if line > stackLineMax || len(worst) > stackTextMax || len(worst) < stackTextMax*9/10 {
		t.Fatalf("a line of %d bytes, a trace of %d: want the limits %d and %d", line, len(worst), stackLineMax, stackTextMax)
	}
	took, allocated := cost(worst)
	if limit := uint64(dotnetNestedAllocFactor * len(worst)); allocated > limit {
		t.Errorf("%d bytes allocated for %d bytes of text at depth %d, want at most %d", allocated, len(worst), depth, limit)
	}
	if took > dotnetNestedTimeLimit {
		t.Errorf("%v for %d bytes of text at depth %d, want at most %v", took, len(worst), depth, dotnetNestedTimeLimit)
	}
	// Twice the depth is twice the text, and must not be four times the
	// memory.
	_, shallow := cost(dotnetNestedTrace(150, 8))
	if _, deep := cost(dotnetNestedTrace(300, 8)); deep > 3*shallow {
		t.Errorf("%d bytes allocated at depth 150, %d at depth 300: more than three times as much", shallow, deep)
	}
}

const (
	// dotnetNestedAllocFactor bounds the bytes allocated per byte of the
	// deepest trace the limits admit. One unwrapping pass per level of
	// nesting allocated 1,500 times the text.
	dotnetNestedAllocFactor = 64
	// dotnetNestedTimeLimit bounds reading that trace, with room for the
	// race detector on a busy machine: it takes 20 ms without the detector
	// and a quarter of a second with it. One pass per level took 2 s
	// without.
	dotnetNestedTimeLimit = 1500 * time.Millisecond
)

// A closure, the wrapper of a go or defer statement and a closure in a
// closure are numbered in the order they stand in their function, so the
// numbers move when one is added above. None stays in a name: the code is
// the function's.
func TestGoGeneratedNames(t *testing.T) {
	cases := []struct{ line, module, function string }{
		{"main.run.func1()", "main", "run"},
		{"main.run.func2()", "main", "run"},
		{"main.run.func1.1()", "main", "run"},
		{"main.run.func1.2.3(0x1)", "main", "run"},
		{"main.run.func1.gowrap2()", "main", "run"},
		{"main.run.gowrap1()", "main", "run"},
		{"main.run.deferwrap2()", "main", "run"},
		{"shop/internal/cart.(*Service).AddItem.func1(0x1)", "shop/internal/cart", "Service.AddItem"},
		{"shop/internal/cart.(*Service).AddItem.func1.deferwrap1(0x1)", "shop/internal/cart", "Service.AddItem"},
		{"shop/internal/cart.glob..func1()", "shop/internal/cart", "glob"},
		{"shop/internal/cart.init.0()", "shop/internal/cart", "init"},
		{"shop/internal/cart.init.func1()", "shop/internal/cart", "init"},
		{"created by main.run.func1 in goroutine 1", "main", "run"},
		// A function may be named like a generated one, and a name only
		// ends that way where a number does.
		{"main.func1()", "main", "func1"},
		{"shop/internal/cart..func1()", "shop/internal/cart", ".func1"},
		{"main.gowrap1(0x1)", "main", "gowrap1"},
		{"main.run.func()", "main", "run.func"},
		{"main.run.func1x()", "main", "run.func1x"},
		{"main.run.wrap1()", "main", "run.wrap1"},
		{"main.(*V2).Get(0x1)", "main", "V2.Get"},
		{"main.run.func1.Close(0x1)", "main", "run.func1.Close"},
	}
	for _, tc := range cases {
		f, ok := goFrame(tc.line)
		if !ok || f.Module != tc.module || f.Function != tc.function {
			t.Errorf("%s\n got %q %q %v\nwant %q %q", tc.line, f.Module, f.Function, ok, tc.module, tc.function)
		}
	}
}

// A module on the vendor list is that module and what is below it, not
// every module whose path starts the same way.
func TestGoVendorModulesMatchWhole(t *testing.T) {
	for module, want := range map[string]bool{
		"github.com/lib/pq": false, "github.com/lib/pq/oid": false, "github.com/lib/pq-tools": true,
		"github.com/mattn/go-sqlite3": false, "github.com/mattn/go-sqlite3-wrapper/db": true,
		"github.com/jackc/pgx/v5": false, "golang.org/x/net/http2": false, "github.com/lib": true,
	} {
		if got := goInApp(StackFrame{Module: module, Function: "Open", Syntax: StackGo}); got != want {
			t.Errorf("goInApp(%s) = %v, want %v", module, got, want)
		}
	}
}

// A frame written in assembly has a file line like any other, and a
// dependency shows by it.
func TestGoAssemblyFileLines(t *testing.T) {
	stack := "goroutine 1 [running]:\npanic({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" +
		"shop.example.invalid/zstd.sequenceDecs_decode_amd64()\n" +
		"\t/go/pkg/mod/shop.example.invalid/zstd@v1.17.0/zstd/seqdec_amd64.s:123 +0x1d\nmain.main()\n\t/app/main.go:12 +0x25\n"
	got := ParseStackFrames(stack)
	want := []StackFrame{{Module: "shop.example.invalid/zstd", Function: "sequenceDecs_decode_amd64", Syntax: StackGo, Dependency: true},
		{Module: "main", Function: "main", Syntax: StackGo}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frames %+v, want %+v", got, want)
	}
	if !goOwns("/go/pkg/mod/shop.example.invalid/zstd@v1.17.0/zstd/seqdec_amd64.s:123 +0x1d") || goOwns("see notes.s:12 for more") ||
		goOwns("/app/main.sh:12") {
		t.Error("goOwns: an assembly file line is Go's, prose and other files are not")
	}
}

// The runtime prints more than the plain dump shows: a system header with
// the goroutine's pointers, frame pointers after a file line, "_" for an
// argument it cannot read. A dump that starts without a header still ends
// at the first one.
func TestGoRuntimeDumpForms(t *testing.T) {
	dump := func(header, background string) string {
		return "panic: cart locked\n\n" + header + `
main.process(0xc000012345, 0x2)
	/app/main.go:42 +0x1d
main.main()
	/app/main.go:12 +0x25

goroutine 2 gp=0xc000002a80 m=nil [force gc (idle)]:
` + background + `()
	/app/worker.go:7 +0x33
`
	}
	want := frames(StackGo, "main", "process", "main", "main")
	for name, header := range map[string]string{
		"plain header":  "goroutine 1 [running]:",
		"system header": "goroutine 1 gp=0xc000002380 m=0 mp=0x5a3c20 [running]:",
		"no header":     "",
	} {
		if got := ParseStackFrames(dump(header, "main.background")); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
		a := mustFingerprint(t, Event{Type: "panic", StackTrace: dump(header, "main.background")})
		b := mustFingerprint(t, Event{Type: "panic", StackTrace: dump(header, "main.sweeper")})
		if a.Key != b.Key {
			t.Errorf("%s: another goroutine's function is in the key: %s, %s", name, a.Key, b.Key)
		}
	}

	dependency := StackFrame{Module: "shop.example.invalid/queue", Function: "Client.Send", Syntax: StackGo, Dependency: true}
	for name, stack := range map[string]string{
		"frame pointers after the file": "goroutine 1 [running]:\nshop.example.invalid/queue.(*Client).Send(0xc000012345)\n" +
			"\t/go/pkg/mod/shop.example.invalid/queue@v1.4.2/client.go:88 +0x1d fp=0xc00007df60 sp=0xc00007df00 pc=0x48a2bd\n",
		"a space in the module cache's path": "goroutine 1 [running]:\nshop.example.invalid/queue.(*Client).Send(0xc000012345)\n" +
			"\t/Users/deploy/Go Workspace/pkg/mod/shop.example.invalid/queue@v1.4.2/client.go:88 +0x1d\n",
		"the autogenerated wrapper of a dependency": "goroutine 1 [running]:\nshop.example.invalid/queue.(*Client).Send(0xc000012345)\n" +
			"\t/go/pkg/mod/shop.example.invalid/queue@v1.4.2/client.go:88 +0x1d\n" +
			"shop.example.invalid/queue.(*Client).Send(0xc000012345)\n\t<autogenerated>:1 +0x2a\n",
		"the wrapper above the method": "goroutine 1 [running]:\nshop.example.invalid/queue.(*Client).Send(0xc000012345)\n\t<autogenerated>:1 +0x2a\n" +
			"shop.example.invalid/queue.(*Client).Send(0xc000012345)\n" +
			"\t/go/pkg/mod/shop.example.invalid/queue@v1.4.2/client.go:88 +0x1d\n",
	} {
		if got := ParseStackFrames(stack); !reflect.DeepEqual(got, []StackFrame{dependency}) {
			t.Errorf("%s: %+v, want %+v", name, got, dependency)
		}
	}

	got := ParseStackFrames("goroutine 1 [running]:\nmain.Process({{{...}}}, _)\n\t/app/main.go:42 +0x1d\nmain.main()\n\t/app/main.go:12 +0x25\n")
	if want := frames(StackGo, "main", "Process", "main", "main"); !reflect.DeepEqual(got, want) {
		t.Errorf("an unreadable argument: %+v, want %+v", got, want)
	}
	// Prose with a file name in it is not a file line.
	if goOwns("see the notes in cart.go:12") || !goOwns("/Users/deploy/Go Workspace/shop/cart.go:12 +0x1d") {
		t.Error("goOwns reads prose as a file line, or not a path with a space")
	}
}

// A module is the same file wherever it is deployed, and two files are two
// modules: a directory counts as a release only where a release is put.
func TestSourceModulesKeepDirectoriesOfTheCode(t *testing.T) {
	py := func(path string) string { return `File "` + path + `", line 42, in add_item` }
	js := func(loc string) string { return "at addItem (" + loc + ":42:17)" }
	for line, want := range map[string]string{
		js("/app/api/1.0/handlers.js"):                           "api/1.0/handlers.js",
		js("/app/api/2.0/handlers.js"):                           "api/2.0/handlers.js",
		js("/app/billing/current/invoice.js"):                    "billing/current/invoice.js",
		js("/app/tax/current/invoice.js"):                        "tax/current/invoice.js",
		js("/app/current/src/cart.js"):                           "src/cart.js",
		js("/app/1.4.2/src/cart.js"):                             "src/cart.js",
		js("/var/www/shop/releases/1.4.2/src/cart.js"):           "src/cart.js",
		js("/data/shop/versions/20261126101112/src/cart.js"):     "src/cart.js",
		js("src/cart.js"):                                        "src/cart.js",
		js("./src/cart.js"):                                      "src/cart.js",
		js("events.js"):                                          "node:events",
		js("internal/modules/cjs/loader.js"):                     "node:internal/modules/cjs/loader",
		js("/app/custom_node_modules/pkg/index.js"):              "custom_node_modules/pkg/index.js",
		js("node_modules/pkg/index.js"):                          "node_modules/pkg/index.js",
		py("/app/mysite-packages/pkg/mod.py"):                    "mysite-packages/pkg/mod.py",
		py("site-packages/pkg/mod.py"):                           "site-packages/pkg/mod.py",
		js("file:///C:/app/shop/cart.js"):                        "shop/cart.js",
		js(`C:\app\shop\cart.js`):                                "shop/cart.js",
		js("http://localhost:3000/static/js/main.js"):            "static/js/main.js",
		js("https://shop.example.invalid/app/static/js/main.js"): "app/static/js/main.js",
		js("https://shop.example.invalid/assets/1.4.2/main.js"):  "main.js",
		js("https://shop.example.invalid/main.js"):               "main.js",
		js("webpack://shop/./src/cart.js"):                       "src/cart.js",
	} {
		got := ParseStackFrames(line)
		if len(got) != 1 || got[0].Module != want {
			t.Errorf("%s: module %+v, want %q", line, got, want)
		}
	}
	for line, inApp := range map[string]bool{
		js("src/cart.js"): true, js("events.js"): false, js("/app/custom_node_modules/pkg/index.js"): true,
		py("/app/mysite-packages/pkg/mod.py"): true, py("/app/.venv/lib/python3.12/site-packages/pkg/mod.py"): false,
	} {
		if got := ParseStackFrames(line); len(got) != 1 || frameInApp(got[0]) != inApp {
			t.Errorf("%s: in-app is not %v: %+v", line, inApp, got)
		}
	}
}

// A trace whose breaks arrived escaped reads like the same trace with real
// ones, also where a Windows path holds a backslash before an "n" or a "t".
func TestEscapedBreaksLeaveWindowsPaths(t *testing.T) {
	real := "Error: cart locked\n    at addItem (C:\\app\\node_modules\\cart\\index.js:42:17)\n" +
		"    at run (C:\\app\\src\\tools\\main.js:7:3)\n    at C:\\app\\src\\new\\index.js:1:1"
	escaped := strings.ReplaceAll(real, "\n", `\n`)
	want := frames(StackJS, "node_modules/cart/index.js", "addItem", "src/tools/main.js", "run", "src/new/index.js", FrameAnonymous)
	for name, stack := range map[string]string{"real": real, "escaped": escaped} {
		if got := ParseStackFrames(stack); !reflect.DeepEqual(got, want) {
			t.Errorf("%s breaks: %+v\nwant %+v", name, got, want)
		}
	}
	py := "Traceback (most recent call last):\n  File \"C:\\app\\shop\\nested\\tasks.py\", line 42, in add_item\n    raise ValueError(sku)\nValueError: kettle"
	if got, want := ParseStackFrames(strings.ReplaceAll(py, "\n", `\n`)), ParseStackFrames(py); len(want) != 1 || !reflect.DeepEqual(got, want) {
		t.Errorf("python, escaped: %+v, want %+v", got, want)
	}
	// Lines at the first column are split too: the header of a cause, a Go
	// frame.
	chain := "com.example.shop.CheckoutException: failed\n\tat com.example.shop.Api.post(Api.java:12)\n" +
		"Caused by: java.lang.IllegalStateException: locked\n\tat com.example.shop.Cart.add(Cart.java:42)\n\t... 1 more"
	if got, want := ParseStackFrames(strings.ReplaceAll(strings.ReplaceAll(chain, "\n", `\n`), "\t", `\t`)), ParseStackFrames(chain); len(want) != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("java chain, escaped: %+v, want %+v", got, want)
	}
	dump := "goroutine 1 [running]:\nmain.process(0xc000012345)\n\t/app/main.go:42 +0x1d\nmain.main()\n\t/app/main.go:12 +0x25"
	if got, want := ParseStackFrames(strings.ReplaceAll(strings.ReplaceAll(dump, "\n", `\n`), "\t", `\t`)), ParseStackFrames(dump); len(want) != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("go dump, escaped: %+v, want %+v", got, want)
	}
}

// An escaped trace is one whatever stands at its ends: a real line break
// behind it, which a log shipper adds, does not make its breaks real.
func TestEscapedTraceWithARealBreakAtItsEnds(t *testing.T) {
	chain := "com.example.shop.CheckoutException: failed\n\tat com.example.shop.Api.post(Api.java:12)\n" +
		"Caused by: java.lang.IllegalStateException: locked\n\tat com.example.shop.Cart.add(Cart.java:42)\n\t... 1 more"
	escaped := strings.ReplaceAll(strings.ReplaceAll(chain, "\n", `\n`), "\t", `\t`)
	want := ParseStackFrames(chain)
	for _, stack := range []string{escaped + "\n", escaped + "\r\n", "\n" + escaped, escaped + "\n\n", escaped + `\n` + "\n"} {
		if got := ParseStackFrames(stack); len(want) != 2 || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %+v, want %+v", stack[len(stack)-12:], got, want)
		}
	}
	// A real break inside it shows that the breaks are real.
	if got := ParseStackFrames(escaped + "\n" + "\tat com.example.shop.Late.run(Late.java:1)"); len(got) != 1 {
		t.Errorf("a real break inside: %+v", got)
	}
}

// In an escaped trace a Windows path ends at the colon before its line
// number, so the break behind the number is one.
func TestEscapedBreaksBehindAWindowsPath(t *testing.T) {
	escape := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\n", `\n`), "\t", `\t`) }
	dump := "goroutine 1 [running]:\nshop/cart.Fail(0x1)\n\tC:\\app\\cart.go:10\nshop/cart.Checkout(0x1)\n\tC:\\app\\cart.go:20\nmain.main()\n\tC:\\app\\main.go:5"
	if got, want := ParseStackFrames(escape(dump)), ParseStackFrames(dump); len(want) != 3 || !reflect.DeepEqual(got, want) {
		t.Errorf("go dump: %+v, want %+v", got, want)
	}
	v8 := "Error: outer\n    at checkout (C:\\app\\src\\api.js:3:1) {\n  [cause]: Error: inner\n      at Cart.add (C:\\app\\src\\cart.js:10:5)\n}"
	if got, want := ParseStackFrames(escape(v8)), ParseStackFrames(v8); len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("v8 cause: %+v, want %+v", got, want)
	}
	v8 = "Error: x\n    at Cart.add (C:\\app\\src\\cart.js:10:5)\n[stack]: next\n    at run (C:\\app\\src\\new.js:1:2)"
	if got, want := ParseStackFrames(escape(v8)), ParseStackFrames(v8); len(want) != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("v8, a line at the first column: %+v, want %+v", got, want)
	}
	// The drive's colon ends nothing.
	if got := unescapeBreaks(`at run (C:\app\new\tools.js:1:2)\n at x`); got != "at run (C:\\app\\new\\tools.js:1:2)\n at x" {
		t.Errorf("unescaped %q", got)
	}
}

// What is read of a trace is bounded before any pass over it: text past
// the limit costs nothing.
func TestStackTextIsCutBeforeItIsRead(t *testing.T) {
	frame := `\n\tat com.example.shop.Cart.add(Cart.java:12)`
	allocated := func(stack string) uint64 {
		least := uint64(0)
		for i := range 3 {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			got := ParseStackFrames(stack)
			runtime.ReadMemStats(&after)
			if len(got) != 1 {
				t.Fatalf("%d frames, want one", len(got))
			}
			if a := after.TotalAlloc - before.TotalAlloc; i == 0 || a < least {
				least = a
			}
		}
		return least
	}
	head := "java.lang.IllegalStateException: locked" + frame + `\n\t`
	atLimit := allocated(head + strings.Repeat(`\`, stackTextMax-len(head)))
	if past := allocated(head + strings.Repeat(`\`, 8*stackTextMax)); past > atLimit*3/2 {
		t.Errorf("%d bytes allocated for a trace at the limit, %d for eight times the text", atLimit, past)
	}
	// The cut is on the bytes that arrive, and the line it goes through is
	// not read.
	late := `\tat com.example.shop.Late.run(Late.java:1)\n`
	past := `\tat com.example.shop.Past.run(Past.java:1)`
	trace := func(n int) string { return head + strings.Repeat("x", n) + `\n` + late + past }
	n := stackTextMax - len(trace(0)) + len(past)
	if got := ParseStackFrames(trace(n)); len(got) != 2 {
		t.Errorf("a frame whose line ends at byte %d: %d frames, want two", stackTextMax, len(got))
	}
	if got := ParseStackFrames(trace(n + 1)); len(got) != 1 {
		t.Errorf("a frame whose line ends past byte %d: %d frames, want one", stackTextMax, len(got))
	}
}

// Cases for conditions no other test moved a key for.
func TestFrameReadersAtTheirEdges(t *testing.T) {
	// A package vendored inside a vendored one is the innermost.
	f, ok := goFrame("shop/vendor/shop.example.invalid/db/vendor/golang.org/x/net/http2.(*Framer).ReadFrame(0x1)")
	if !ok || f.Module != "golang.org/x/net/http2" || !f.Dependency {
		t.Errorf("a nested vendor directory: %+v", f)
	}
	// Mono's notation without the offset: a frame where every parameter
	// starts with a type, prose otherwise; with the offset, a frame.
	for line, want := range map[string]bool{
		"at Example.Shop.Cart.Add (System.String sku)":                  true,
		"at Example.Shop.Cart.Add (System.String sku, Int32 count)":     true,
		"at Example.Shop.Cart.Add ()":                                   true,
		"at Example.Shop.checkout (final step)":                         false,
		"at Example.Shop.Cart.Add (final step) [0x0001a] in <abc>:0":    true,
		"at Example.Shop.Cart.Add (System.String sku) [0x0001a] in <a>": true,
		"at checkout (System.String sku) [0x0001a] in <abc>:0":          false,
	} {
		if _, ok := dotnetFrame(line); ok != want {
			t.Errorf("%q is a frame: %v, want %v", line, ok, want)
		}
	}
	// A location of the runtime's own is its module as it stands, whatever
	// directories it names.
	for loc, want := range map[string]string{
		"node:internal/modules/cjs/loader:1760:14": "node:internal/modules/cjs/loader",
		"node:internal/deps/node_modules/x:1:2":    "node:internal/deps/node_modules/x",
		"node:internal/releases/1.4.2/x:1:2":       "node:internal/releases/1.4.2/x",
	} {
		if got := jsModule(loc); got != want {
			t.Errorf("jsModule(%q) = %q, want %q", loc, got, want)
		}
	}
	// Of two release directories the last one ends what is cut, and a
	// user's directory is cut with the home it stands in.
	for path, want := range map[string]string{
		"/app/releases/1.4.2/versions/2.0.1/src/cart.js": "src/cart.js",
		"/app/releases/1.4.2/src/cart.js":                "src/cart.js",
		"/Users/ann/shop/src/cart.js":                    "shop/src/cart.js",
		"/home/ann/shop/src/cart.js":                     "shop/src/cart.js",
		"/Users/cart.js":                                 "/Users/cart.js",
	} {
		if got := sourceModule(path); got != want {
			t.Errorf("sourceModule(%q) = %q, want %q", path, got, want)
		}
	}
	// An arrow in a line behind the frames of a .NET root cause introduces
	// no type.
	_, typ, _ := parseStack("System.Exception: outer\n ---> MyApp.InnerException: locked\n   at MyApp.Cart.Add()\n" +
		"Note: handled ---> MyApp.OtherException: logged\n")
	if typ != "MyApp.InnerException" {
		t.Errorf("an arrow behind the frames: type %q", typ)
	}
	// A trace of exactly the limit is read to its last byte.
	frame := "\tat com.example.shop.Cart.add(Cart.java:12)"
	last := "\n\tat com.example.shop.Last.run(Last.java:1)"
	exact := frame + "\n" + strings.Repeat("x", stackTextMax-len(frame)-1-len(last)) + last
	if got := ParseStackFrames(exact); len(exact) != stackTextMax || len(got) != 2 {
		t.Errorf("a trace of %d bytes that ends in a frame: %d frames, want two", len(exact), len(got))
	}
}

// How the JVM's count of shared frames is read where the text is not what
// a JVM prints, and where a section has nothing but generated frames.
func TestJavaSharedFramesAtTheirEdges(t *testing.T) {
	const top = "java.lang.IllegalStateException: m\n\tat a.F.f1(F.java:1)\n\tat a.F.f2(F.java:2)\n\tat a.F.f3(F.java:3)\n"
	for name, tc := range map[string]struct {
		stack string
		want  []StackFrame
	}{
		// A cause without frames of its own is what the next one shares
		// its frames with.
		"a cause of shared frames alone": {top + "Caused by: a.B: b\n\t... 2 more\nCaused by: a.C: c\n\tat a.C.c1(C.java:1)\n\t... 3 more\n",
			frames(StackJava, "a.C", "c1", "a.F", "f2", "a.F", "f3")},
		"a cause that shares none": {top + "Caused by: a.B: b\n\t... 0 more\nCaused by: a.C: c\n\tat a.C.c1(C.java:1)\n\t... 3 more\n",
			frames(StackJava, "a.C", "c1")},
		"a count above what there is": {top + "Caused by: a.C: c\n\tat a.C.c1(C.java:1)\n\t... 7 more\n",
			frames(StackJava, "a.C", "c1", "a.F", "f1", "a.F", "f2", "a.F", "f3")},
		// The first section has nothing to share frames with.
		"a count in the first section": {"java.lang.IllegalStateException: m\n\tat a.B.c(B.java:1)\n\t... 1 more\n",
			frames(StackJava, "a.B", "c")},
		// Shared frames that are all generated leave the cause its own.
		"shared frames that are generated": {"java.lang.IllegalStateException: m\n\tat a.F$$Lambda$1/0x01.run(Unknown Source)\n" +
			"\tat a.F$$Lambda$2/0x02.run(Unknown Source)\nCaused by: a.C: c\n\tat a.C.c1(C.java:1)\n\t... 1 more\n",
			frames(StackJava, "a.C", "c1")},
	} {
		if got := ParseStackFrames(tc.stack); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}

// Frameworks and libraries that are not the application, by the frames a
// trace shows of them.
func TestCommonLibrariesAreNotTheApplication(t *testing.T) {
	for _, module := range []string{
		"io.micronaut.http.server.netty.RoutingInBoundHandler", "io.ktor.server.netty.NettyApplicationCallHandler",
		"org.aspectj.runtime.reflect.JoinPointImpl", "net.bytebuddy.implementation.MethodDelegation",
		"io.reactivex.internal.operators.observable.ObservableMap", "io.reactivex.rxjava3.core.Observable",
		"com.google.gson.Gson", "com.rabbitmq.client.impl.ChannelN", "org.jooq.impl.AbstractQuery",
		"org.mybatis.spring.SqlSessionTemplate", "com.stripe.net.LiveStripeResponseGetter", "io.sentry.Sentry",
		"com.squareup.okhttp.Call", "retrofit2.OkHttpCall",
	} {
		if frameInApp(StackFrame{Module: module, Function: "run", Syntax: StackJava}) {
			t.Errorf("Java: %s counts as the application's", module)
		}
	}
	for _, module := range []string{
		"Polly.Policy", "MediatR.Mediator", "MassTransit.Middleware.ConsumerMessageFilter", "Castle.DynamicProxy.AbstractInvocation",
		"Autofac.Core.Resolving.ResolveOperation", "Serilog.Core.Logger", "NLog.Logger", "AutoMapper.Mapper",
		"RabbitMQ.Client.Impl.ModelBase", "Confluent.Kafka.Consumer",
	} {
		if frameInApp(StackFrame{Module: module, Function: "Run", Syntax: StackDotNet}) {
			t.Errorf(".NET: %s counts as the application's", module)
		}
	}
	// A prefix is whole names: what only starts like one is the
	// application's.
	for _, f := range []StackFrame{
		{Module: "PollyShop.Cart", Function: "Add", Syntax: StackDotNet}, {Module: "RabbitMQ.Shop.Cart", Function: "Add", Syntax: StackDotNet},
		{Module: "io.sentryshop.Cart", Function: "add", Syntax: StackJava}, {Module: "retrofit2shop.Cart", Function: "add", Syntax: StackJava},
	} {
		if !frameInApp(f) {
			t.Errorf("%s does not count as the application's", f.Module)
		}
	}
}

// A Go trace that is not a panic's gives no frames to a key, which is the
// common case: an error logged with debug.Stack. That is known before its
// frames are read (goAtFault), and they are not read.
func TestGoTraceNotAtFaultIsNotParsed(t *testing.T) {
	var dump strings.Builder
	dump.WriteString("goroutine 1 [running]:\n")
	for i := range 70 {
		fmt.Fprintf(&dump, "shop.example.invalid/cart.(*Service).Step%d(0xc000012345, {0xc00001a0c0, 0x6})\n\t/app/cart/service.go:%d +0x1d\n", i, 40+i)
	}
	seen, crash := dump.String(), "panic: bad state\n\n"+dump.String()
	if frames, typ := faultStack(seen); frames != nil || typ != "" {
		t.Errorf("a trace that is no panic's: %d frames, type %q", len(frames), typ)
	}
	if frames, _ := faultStack(crash); len(frames) != 70 {
		t.Errorf("a panic's trace: %d frames, want 70", len(frames))
	}
	allocs := func(stack string) float64 {
		return testing.AllocsPerRun(20, func() { FingerprintExceptions([]Event{{Type: "E", StackTrace: stack}}) })
	}
	if a, b := allocs(seen), allocs(crash); a*4 > b {
		t.Errorf("%v allocations for a trace that is no panic's, %v for the panic's: its frames were read", a, b)
	}
}

// The test before the frames gives the answer the frames give.
func TestGoAtFaultAgreesWithTheFrames(t *testing.T) {
	const (
		head  = "goroutine 1 [running]:\n"
		frame = "main.process(0xc000012345)\n\t/app/main.go:42 +0x1d\n"
		main  = "main.main()\n\t/app/main.go:12 +0x25\n"
	)
	for stack, want := range map[string]bool{
		head + frame + main: false,
		frame + main:        false,
		"panic: bad state\n\n" + head + frame + main:                                                           true,
		"fatal error: all goroutines are asleep\n\n" + head + frame:                                            true,
		"panic: bad state\n" + frame:                                                                           true,
		head + "panic: bad state\n" + frame:                                                                    false,
		frame + "panic: bad state\n" + main:                                                                    false,
		head + "panic({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" + frame:                  true,
		head + frame + "runtime.gopanic({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" + main: true,
		head + "runtime.goPanicIndex(0x5, 0x3)\n\t/usr/local/go/src/runtime/panic.go:115 +0x74\n" + frame:      true,
		head + "runtime.sigpanic()\n\t/usr/local/go/src/runtime/signal_unix.go:917 +0x35b\n" + frame:           true,
		head + "runtime.throw({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:1101 +0x48\n" + frame:          true,
		head + frame + "runtime.throw({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:1101 +0x48\n" + main:   true,
		head + "runtime.Gosched()\n\t/usr/local/go/src/runtime/proc.go:1 +0x1\n" + frame:                       false,
		head + "shop/vendor/runtime.gopanic(0x1)\n\t/app/x.go:1 +0x1\n" + frame:                                true,
		head + "shop.example.invalid/panic.Recover(0x1)\n\t/app/x.go:1 +0x1\n" + frame:                         false,
		head + "shop.example.invalid/cart.panicIfLocked(0x1)\n\t/app/x.go:1 +0x1\n" + frame:                    false,
		// The panic of another goroutine of the dump is not this one's.
		head + frame + "\ngoroutine 7 [running]:\npanic({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" + main: false,
		head + "\ngoroutine 7 [running]:\npanic({0x1, 0x2})\n\t/usr/local/go/src/runtime/panic.go:770 +0x132\n" + main:         true,
		"": false,
	} {
		lines := stackLines(stack)
		_, _, atFault := goRootCause(lines)
		if got := goAtFault(lines); got != atFault || got != want {
			t.Errorf("%q: goAtFault %v, the frames say %v, want %v", stack, got, atFault, want)
		}
	}
}

// Two notations that own as many lines: the one whose line comes first.
func TestStackNotationTieGoesToTheFirstLine(t *testing.T) {
	java, dotnet := "\tat com.example.shop.Cart.add(Cart.java:42)\n", "   at Example.Shop.Cart.Add(String sku)\n"
	if got := ParseStackFrames(java + dotnet); len(got) == 0 || got[0].Syntax != StackJava {
		t.Errorf("Java first: %+v", got)
	}
	if got := ParseStackFrames(dotnet + java); len(got) == 0 || got[0].Syntax != StackDotNet {
		t.Errorf(".NET first: %+v", got)
	}
}

// A getter and a setter are functions of their own.
func TestJavaScriptAccessors(t *testing.T) {
	for line, want := range map[string]string{
		"at get left (/app/src/box.js:4:11)":                 "get left",
		"at set left (/app/src/box.js:8:11)":                 "set left",
		"at Box.get right [as right] (/app/src/box.js:4:11)": "Box.get right",
		"at get (/app/src/box.js:4:11)":                      "get",
	} {
		got := ParseStackFrames(line)
		if len(got) != 1 || got[0].Function != want || got[0].Module != "src/box.js" {
			t.Errorf("%s: %+v, want function %q", line, got, want)
		}
	}
	caller := "\n    at render (/app/src/page.js:9:3)"
	a := mustFingerprint(t, Event{Type: "TypeError", StackTrace: "TypeError: x\n    at get left (/app/src/box.js:4:11)" + caller})
	b := mustFingerprint(t, Event{Type: "TypeError", StackTrace: "TypeError: x\n    at get right (/app/src/box.js:5:11)" + caller})
	if a.Key == b.Key {
		t.Errorf("two getters share the key %s", a.Key)
	}
}

// V8 prints a function by its display name, which is any text: a method
// with a string or a symbol for its key, a bound function. Once a trace is
// known to be JavaScript's, a frame is whatever stands before a location
// with a line and a column. The traces are Node 24's.
func TestJavaScriptDisplayNames(t *testing.T) {
	trace := func(top string) string {
		return "Error: sku 7 is locked\n    at " + top + "\n    at dispatch (/srv/app/shop.js:8:41)\n" +
			"    at Object.<anonymous> (/srv/app/shop.js:11:9)\n    at Module._compile (node:internal/modules/cjs/loader:1760:14)\n"
	}
	keys := map[string]string{}
	for top, function := range map[string]string{
		"cart-added (/srv/app/shop.js:2:26)":         "cart-added",
		"user:created (/srv/app/shop.js:3:28)":       "user:created",
		"my handler (/srv/app/shop.js:4:26)":         "my handler",
		"[evt] (/srv/app/shop.js:5:33)":              "[evt]",
		"Object.plain (/srv/app/shop.js:6:19)":       "Object.plain",
		"bound addItem (/srv/app/shop.js:9:42)":      "bound addItem",
		"async my handler (/srv/app/shop.js:4:26)":   "my handler",
		"(the) end [as last] (/srv/app/shop.js:4:2)": "(the) end",
	} {
		fp := mustFingerprint(t, Event{Type: "Error", StackTrace: trace(top)})
		want := frames(StackJS, "app/shop.js", function, "app/shop.js", "dispatch", "app/shop.js", FrameAnonymous)
		if !reflect.DeepEqual(fp.Frames, want) {
			t.Errorf("%s: frames %+v, want %+v", top, fp.Frames, want)
		}
		if other, dup := keys[fp.Key]; dup && function != "my handler" {
			t.Errorf("%s and %s share the key %s", top, other, fp.Key)
		}
		keys[fp.Key] = top
	}
	// Prose stays prose, above the frames of a trace and alone. A location
	// has a line and a column, or is one of V8's words.
	want := frames(StackJS, "app/shop.js", "dispatch", "app/shop.js", FrameAnonymous, "node:internal/modules/cjs/loader", "Module._compile")
	for _, prose := range []string{
		"at least 3 items (max 5)", "at the checkout step (see cart 7)", "at 10:11:12 the cart (id 7:12) was locked",
		"at checkout step (retry 2:1 failed)x", "at step two (cart:12)", "at checkout step (cart:2:1) again", "at my handler (10:11:12)",
	} {
		if got := ParseStackFrames(trace(prose)); !reflect.DeepEqual(got, want) {
			t.Errorf("%q above the frames: %+v", prose, got)
		}
		if jsOwns(prose) {
			t.Errorf("%q marks a trace as JavaScript's", prose)
		}
	}
	for _, line := range []string{"at cart-added (/srv/app/shop.js:2:26)", "at my handler (/srv/app/shop.js:4:26)"} {
		if jsOwns(line) {
			t.Errorf("%q marks a trace as JavaScript's: only a plain name does", line)
		}
	}
	if got := ParseStackFrames("at my handler (native)\nat my handler (<anonymous>)\n" + trace("x (/srv/app/a.js:1:1)")); len(got) != 6 {
		t.Errorf("V8's words for code without a file: %+v", got)
	}
}

// A dependency is the same module whatever a URL says behind it: a query
// that busts a cache is not part of its name, and names no directory.
func TestJavaScriptModuleQueries(t *testing.T) {
	for loc, want := range map[string]string{
		"https://shop.example.invalid/node_modules/lib/index.js?v=100:1:2": "node_modules/lib/index.js",
		"https://shop.example.invalid/node_modules/lib/index.js?v=101:1:2": "node_modules/lib/index.js",
		"http://localhost:5173/src/a.js?t=/node_modules/x:1:2":             "src/a.js",
		"http://localhost:5173/src/a.js?t=1700000000:1:2":                  "src/a.js",
		"/srv/app/node_modules/lib/index.js:1:2":                           "node_modules/lib/index.js",
	} {
		if got := jsModule(loc); got != want {
			t.Errorf("jsModule(%q) = %q, want %q", loc, got, want)
		}
	}
	if !jsInApp(StackFrame{Module: jsModule("http://localhost:5173/src/a.js?t=/node_modules/x:1:2"), Syntax: StackJS}) {
		t.Error("a query made a dependency of the application's file")
	}
}

func TestRootCauseBoundaries(t *testing.T) {
	cases := []struct {
		name, stack string
		want        []StackFrame
		typ         string
	}{
		// A trace whose indentation was stripped ends at its first
		// suppressed block: nothing shows where the block does.
		{"java: a suppressed block without indentation",
			"a.B: x\nat a.B.b(B.java:1)\nSuppressed: c.D: y\nat c.D.d(D.java:2)\nCaused by: e.F: z\nat e.F.f(F.java:3)",
			frames(StackJava, "a.B", "b"), ""},
		// A blank line does not end a suppressed block: what follows it at
		// the block's depth is still the suppressed exception's.
		{"java: a blank line inside a suppressed block",
			"a.B: x\n\tat a.B.b(B.java:1)\n\tSuppressed: c.D: y\n\t\tat c.D.d(D.java:2)\n\n\t\tat c.D.e(D.java:3)\n" +
				"\tCaused by: e.F: z\n\t\tat e.F.f(F.java:4)",
			frames(StackJava, "a.B", "b"), ""},
		{"dotnet: two inner exceptions introduced on one line",
			"A.Outer: x ---> B.Middle: y ---> C.Inner: z\n   at N.S.Type.Method(String s)",
			frames(StackDotNet, "N.S.Type", "Method"), "C.Inner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ, _ := parseStack(tc.stack)
			if !reflect.DeepEqual(got, tc.want) || typ != tc.typ {
				t.Errorf("frames %+v type %q\nwant %+v %q", got, typ, tc.want, tc.typ)
			}
		})
	}
}
