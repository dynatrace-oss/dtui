package exckey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// exceptionKeySources are the files the key algorithm lives in, by name: a
// pattern would take in any other file of the package that happens to match.
var exceptionKeySources = []string{
	"excfingerprint.go", "excframes.go", "excframes_dotnet.go", "excframes_go.go", "excframes_java.go",
	"excframes_javascript.go", "excframes_python.go", "excmessage.go",
}

// Every file of the package that is not a test is a key source: a file the
// list does not name would hold tables and expressions no pin sees.
func TestExceptionKeySourcesAreAllListed(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, fault := range keySourceListFaults(names, exceptionKeySources) {
		t.Error(fault)
	}
	got := keySourceListFaults([]string{"a.go", "a_test.go", "b.go", "testdata", "notes.md", "c.go"}, []string{"a.go", "c.go", "d.go"})
	if want := []string{"b.go is not in exceptionKeySources", "d.go is in exceptionKeySources and is no file of the package"}; !reflect.DeepEqual(got, want) {
		t.Errorf("faults %q, want %q", got, want)
	}
}

// keySourceListFaults compares the files of the package's directory with
// the listed key sources.
func keySourceListFaults(dir, listed []string) []string {
	var faults []string
	seen := map[string]bool{}
	for _, name := range dir {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen[name] = true
		if !slices.Contains(listed, name) {
			faults = append(faults, name+" is not in exceptionKeySources")
		}
	}
	for _, name := range listed {
		if !seen[name] {
			faults = append(faults, name+" is in exceptionKeySources and is no file of the package")
		}
	}
	return faults
}

// unicodeVersionInput names the line that pins the Unicode tables of the
// toolchain, by their version.
const unicodeVersionInput = "unicode.Version"

// exceptionKeyRepin says what to do about a pinned value that changed, for
// the golden file of the given kind ("keys" or "inputs").
func exceptionKeyRepin(kind string) string {
	return "raise ExceptionKeyVersion, which starts a new golden file. Only while no release has shipped " +
		ExceptionKeyVersion + " keys: delete the line from " +
		filepath.Join(exceptionCorpusDir, ExceptionKeyVersion+"-"+kind+".golden") +
		" and run go test -run TestExceptionKey -update"
}

// readExceptionGolden reads a golden file of "value<TAB>name" lines into a
// map by name. A missing file is an error unless -update is creating it.
func readExceptionGolden(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if *updateExceptionKeys {
			return map[string]string{}
		}
		t.Fatalf("%v (new key version? run with -update to create it)", err)
	}
	pinned, err := parseExceptionGolden(string(raw))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return pinned
}

func parseExceptionGolden(raw string) (map[string]string, error) {
	pinned := map[string]string{}
	for i, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		value, name, ok := strings.Cut(line, "\t")
		if !ok || value == "" || name == "" {
			return nil, fmt.Errorf("line %d is not a value, a tab and a name: %q", i+1, line)
		}
		if _, dup := pinned[name]; dup {
			return nil, fmt.Errorf("line %d pins %q a second time", i+1, name)
		}
		pinned[name] = value
	}
	return pinned, nil
}

// A golden line that cannot be read must fail, not drop out of the pin.
func TestExceptionGoldenLinesMustParse(t *testing.T) {
	got, err := parseExceptionGolden("k1\tfirst case\nk2\tsecond\tcase\n")
	if want := map[string]string{"first case": "k1", "second\tcase": "k2"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, %v", got, err)
	}
	for _, raw := range []string{"k1 first case\n", "k1\tfirst\n\nk2\tsecond\n", "\tfirst\n", "k1\t\n", "k1\ta\nk2\ta\n", ""} {
		if got, err := parseExceptionGolden(raw); err == nil {
			t.Errorf("%q was read as %q", raw, got)
		}
	}
}

// keyInput is one package-level var or const declaration of the key
// algorithm: a table, an expression, a limit.
type keyInput struct {
	name, digest string
}

// TestExceptionKeyInputsArePinned pins everything the key algorithm is
// configured by: every package-level var and const of its files — the
// vendor and pass-through tables, the expressions, the message rules and
// their order, the limits. The corpus pins keys, but only for the inputs it
// happens to hold; a prefix added to a table or a quantifier widened in an
// expression moves keys it has no case for. So a declaration whose tokens
// change fails here, whatever the corpus says. Comments and layout are not
// part of a digest.
//
// One line is no declaration: unicode.Version, the version of the Unicode
// tables the toolchain was built with. Every reader that matches \p{L} or
// \p{N} or asks the unicode package what a letter is depends on them: the
// names of .NET, Java, JavaScript, Go and Python frames and types, and the
// quotes of a message. A Go release that changes the tables fails here so
// that somebody looks. Only a name in a script the new tables add can read
// differently, so where the corpus keys hold (TestExceptionKeysAreStable)
// this is a repin of that one line and no new key version: delete the line
// and run with -update.
//
// What this does not see is the code of the functions: a condition dropped
// from a function body changes keys without changing a declaration. Only a
// case catches that, in the corpus or in a test of the function.
//
// The fix is the one TestExceptionKeysAreStable asks for (exceptionKeyRepin).
// -update only adds declarations that are new.
func TestExceptionKeyInputsArePinned(t *testing.T) {
	golden := filepath.Join(exceptionCorpusDir, ExceptionKeyVersion+"-inputs.golden")
	pinned := readExceptionGolden(t, golden)

	var lines []string
	for _, in := range exceptionKeyInputs(t) {
		want, ok := pinned[in.name]
		switch {
		case ok && want != in.digest && in.name == unicodeVersionInput:
			t.Errorf("%s is %s, pinned %s — the toolchain's Unicode tables changed, which every reader of a name in "+
				"any script depends on. If TestExceptionKeysAreStable holds, repin without raising %s: delete the line from %s "+
				"and run go test -run TestExceptionKey -update", in.name, in.digest, want, "ExceptionKeyVersion", golden)
		case ok && want != in.digest:
			t.Errorf("%s changed — it is part of the %s key algorithm; %s",
				in.name, ExceptionKeyVersion, exceptionKeyRepin("inputs"))
		case !ok && !*updateExceptionKeys:
			t.Errorf("%s is not pinned; if no %s key depends on it yet, run with -update to add it", in.name, ExceptionKeyVersion)
		}
		delete(pinned, in.name)
		lines = append(lines, in.digest+"\t"+in.name)
	}
	for name := range pinned {
		t.Errorf("%s is pinned but no longer declared — it was part of the %s key algorithm; %s",
			name, ExceptionKeyVersion, exceptionKeyRepin("inputs"))
	}
	if *updateExceptionKeys && !t.Failed() {
		if err := os.WriteFile(golden, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The pin above covers package-level declarations only, so nothing the
// algorithm is configured by may be built where it cannot see: no expression
// compiled, no replacer built and no table of literals written inside a
// function. A table is a slice, array or map literal that holds a literal,
// as an element or as a key. For the same reason a key source has no init
// function, which could change a pinned table after it was declared, and
// imports regexp and strings under their own names, which is how the check
// knows them.
func TestExceptionKeyInputsArePackageLevel(t *testing.T) {
	eachExceptionKeySource(t, func(fset *token.FileSet, file *ast.File, _ []byte) {
		for _, fault := range keySourceFaults(fset, file) {
			t.Error(fault)
		}
	})
}

// keySourceFaults lists what in a key source the inputs pin would not see.
func keySourceFaults(fset *token.FileSet, file *ast.File) []string {
	var faults []string
	for _, imp := range file.Imports {
		if path := strings.Trim(imp.Path.Value, `"`); imp.Name != nil && (path == "regexp" || path == "strings") {
			faults = append(faults, fmt.Sprintf("%s: %s is imported as %s; import it under its own name", fset.Position(imp.Pos()), path, imp.Name.Name))
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Recv == nil && fn.Name.Name == "init" {
			faults = append(faults, fmt.Sprintf("%s: an init function; what it sets up belongs in a package-level declaration", fset.Position(fn.Pos())))
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := n.X.(*ast.Ident); ok && (pkg.Name == "regexp" || pkg.Name == "strings" && n.Sel.Name == "NewReplacer") {
					faults = append(faults, fmt.Sprintf("%s: %s.%s in %s; declare it at package level", fset.Position(n.Pos()), pkg.Name, n.Sel.Name, fn.Name.Name))
				}
			case *ast.CompositeLit:
				if isTableType(n.Type) && holdsLiteral(n) {
					faults = append(faults, fmt.Sprintf("%s: a table of literals in %s; declare it at package level", fset.Position(n.Pos()), fn.Name.Name))
				}
			}
			return true
		})
	}
	return faults
}

func TestKeySourceFaults(t *testing.T) {
	for src, want := range map[string]string{
		"package p\nfunc f() {}\n": "",
		"package p\nimport (\n\"regexp\"\n\"strings\"\n)\nvar re = regexp.MustCompile(strings.ToLower(`A`))\n": "",
		"package p\ntype t struct{}\nfunc (t) init() {}\n":                                                     "",
		"package p\nimport u \"unicode/utf8\"\nvar n = u.RuneSelf\n":                                           "",
		"package p\nvar table = []string{\"a\"}\nfunc init() { table = append(table, x) }\n":                   "an init function",
		"package p\nimport re \"regexp\"\nfunc f() { re.MustCompile(`a`) }\n":                                  "regexp is imported as re",
		"package p\nimport . \"regexp\"\nfunc f() { MustCompile(`a`) }\n":                                      "regexp is imported as .",
		"package p\nimport s \"strings\"\nfunc f() { s.NewReplacer(a, b) }\n":                                  "strings is imported as s",
		"package p\nimport \"regexp\"\nfunc f() { regexp.MustCompile(`a`) }\n":                                 "regexp.MustCompile in f",
		"package p\nimport \"strings\"\nfunc f() { strings.NewReplacer(a, b) }\n":                              "strings.NewReplacer in f",
		"package p\nfunc f() []string { return []string{\"a\"} }\n":                                            "a table of literals in f",
	} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		faults := keySourceFaults(fset, file)
		if want == "" && len(faults) > 0 || want != "" && (len(faults) != 1 || !strings.Contains(faults[0], want)) {
			t.Errorf("%q: faults %q, want %q", src, faults, want)
		}
	}
}

// holdsLiteral reports whether a composite literal has a basic literal among
// its elements or their keys: map[string]bool{"x": true} is a table of names.
func holdsLiteral(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		parts := []ast.Expr{elt}
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			parts = []ast.Expr{kv.Key, kv.Value}
		}
		for _, part := range parts {
			if _, ok := part.(*ast.BasicLit); ok {
				return true
			}
		}
	}
	return false
}

func TestHoldsLiteral(t *testing.T) {
	cases := map[string]bool{
		`[]string{"a"}`:                 true,
		`map[string]bool{"a": true}`:    true,
		`map[string]string{k: "a"}`:     true,
		`[...]int{1}`:                   true,
		`[]string{a, b}`:                false,
		`map[string]bool{a: true}`:      false,
		`[]StackFrame{all[0]}`:          false,
		`[]string{}`:                    false,
		`[]javaSection{{typ: "a"}}`:     false,
		`[]func() string{f, g}`:         false,
		`map[string][]string{"a": nil}`: true,
	}
	for src, want := range cases {
		expr, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := holdsLiteral(expr.(*ast.CompositeLit)); got != want {
			t.Errorf("holdsLiteral(%s) = %v, want %v", src, got, want)
		}
	}
}

func isTableType(e ast.Expr) bool {
	switch e.(type) {
	case *ast.ArrayType, *ast.MapType:
		return true
	}
	return false
}

// A declaration the pin does not see would be an input it does not cover.
func TestExceptionKeyInputsCoverTheTables(t *testing.T) {
	names := map[string]bool{}
	for _, in := range exceptionKeyInputs(t) {
		if in.name == unicodeVersionInput && in.digest != unicode.Version {
			t.Errorf("%s is pinned as %q, want the version itself, %s", in.name, in.digest, unicode.Version)
		}
		for _, name := range strings.Fields(strings.NewReplacer("const", "", "var", "", ",", " ").Replace(in.name)) {
			names[name] = true
		}
	}
	for _, name := range []string{
		unicodeVersionInput, "msgUnits", "ExceptionKeyVersion", "FingerprintFrames", "fingerprintKeyLen", "RungAppFrames",
		"messageRules", "msgNumberRe", "exceptionMessageInput", "ExceptionMessageMax",
		"stackNotations", "StackJava", "deployRoots", "sourceReleaseRe",
		"javaVendorPrefixes", "javaPassThrough", "javaNoSource", "javaLogArchives", "dotnetVendorPrefixes", "dotnetPassThrough",
		"jsPassThrough", "jsDependencyDir", "pythonPackageDirs", "pythonPassThrough",
		"goStdlib", "goVendorPrefixes", "goPassThrough", "goDependencyRe",
	} {
		if !names[name] {
			t.Errorf("%s is not among the pinned inputs", name)
		}
	}
}

// exceptionKeyInputs digests every package-level var and const declaration
// of the key algorithm's files, sorted by name. A const block is one input,
// since iota makes its values depend on their order.
func exceptionKeyInputs(t *testing.T) []keyInput {
	t.Helper()
	var out []keyInput
	eachExceptionKeySource(t, func(fset *token.FileSet, file *ast.File, src []byte) {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR && gen.Tok != token.CONST {
				continue
			}
			var block []string
			for _, spec := range gen.Specs {
				spec := spec.(*ast.ValueSpec)
				var names []string
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
				if gen.Tok == token.CONST {
					block = append(block, names...)
					continue
				}
				out = append(out, keyInput{"var " + strings.Join(names, ", "),
					tokenDigest(t, src[fset.Position(spec.Pos()).Offset:fset.Position(spec.End()).Offset])})
			}
			if gen.Tok == token.CONST {
				out = append(out, keyInput{"const " + strings.Join(block, ", "),
					tokenDigest(t, src[fset.Position(gen.Pos()).Offset:fset.Position(gen.End()).Offset])})
			}
		}
	})
	out = append(out, keyInput{unicodeVersionInput, unicode.Version})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func eachExceptionKeySource(t *testing.T, visit func(*token.FileSet, *ast.File, []byte)) {
	t.Helper()
	for _, path := range exceptionKeySources {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		visit(fset, file, src)
	}
}

// tokenDigest hashes the tokens of a piece of source: comments, layout, and
// the comma and line end a wrapped list closes with are left out, a string
// is taken as it is written.
func tokenDigest(t *testing.T, src []byte) string {
	t.Helper()
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile("", fset.Base(), len(src)), src, func(pos token.Position, msg string) {
		t.Fatalf("%s: %s", pos, msg)
	}, 0)
	h := sha256.New()
	comma := false
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		closing := tok == token.RBRACE || tok == token.RPAREN
		if comma && !closing {
			h.Write([]byte(",\x00"))
		}
		if comma = tok == token.COMMA; comma {
			continue
		}
		if lit == "" {
			lit = tok.String()
		}
		h.Write([]byte(lit + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func TestTokenDigestIgnoresLayoutOnly(t *testing.T) {
	same := [][2]string{
		{`x = []string{"a", "b"}`, "x = []string{\n\t\"a\", // first\n\t\"b\",\n}"},
		{`x = 5`, "x   =   5 // five"},
	}
	for _, pair := range same {
		if tokenDigest(t, []byte(pair[0])) != tokenDigest(t, []byte(pair[1])) {
			t.Errorf("%q and %q differ", pair[0], pair[1])
		}
	}
	differ := [][2]string{
		{`x = []string{"a", "b"}`, `x = []string{"a", "b", "c"}`},
		{`x = []string{"a", "b"}`, `x = []string{"b", "a"}`},
		{`x = []string{"a", "b"}`, `x = []string{"ab"}`},
		{"x = `a{1,7}`", "x = `a{1,8}`"},
		{"x = `a b`", "x = `a  b`"},
		{`x = 200`, `x = 201`},
		{`x = f(a, b)`, `x = f(a)(b)`},
	}
	for _, pair := range differ {
		if tokenDigest(t, []byte(pair[0])) == tokenDigest(t, []byte(pair[1])) {
			t.Errorf("%q and %q share a digest", pair[0], pair[1])
		}
	}
}
