package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeAndMatchBlacklist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bl.txt")
	content := `# comment
main.init
main.foo
github.com/x/y.bar
main.(*T).Method
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	bl, err := LoadGasZeroBlacklist(path)
	if err != nil {
		t.Fatal(err)
	}
	if bl.Len() != 4 {
		t.Fatalf("want 4 entries, got %d", bl.Len())
	}
	fn := &ast.FuncDecl{Name: ast.NewIdent("init")}
	if !bl.MatchFunc("main", fn) {
		t.Fatal("expected main.init match")
	}
	if !bl.MatchFunc("y", &ast.FuncDecl{Name: ast.NewIdent("bar")}) {
		t.Fatal("expected y.bar from import path")
	}
	if !bl.MatchFunc("main", &ast.FuncDecl{Name: ast.NewIdent("Method")}) {
		t.Fatal("expected Method match")
	}
}

func TestWrapFuncGasZero(t *testing.T) {
	src := `package main
func foo() {
	x := 1
	_ = x
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if err := WrapFuncGasZero(fn); err != nil {
		t.Fatal(err)
	}
	if err := WrapFuncGasZero(fn); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "__cvwGasSave") != 2 { // assign + defer use
		t.Fatalf("unexpected wrap count:\n%s", out)
	}
	if !strings.Contains(out, "defer registry.") || !strings.Contains(out, "SetGas(__cvwGasSave)") {
		t.Fatalf("missing defer:\n%s", out)
	}
	// second wrap should be no-op (still one assign)
	if strings.Count(out, "__cvwGasSave :=") != 1 {
		t.Fatalf("double-wrapped:\n%s", out)
	}
}
