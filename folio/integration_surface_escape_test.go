package folio_test

import (
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"html"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/tools/folio"
	"github.com/hollis-labs/tools/folio/service"
)

// A title is free text a person types. Every output it reaches has to carry it as text: a Go string
// literal, HTML element text. The other inputs are held by patterns to characters that need no escaping.
const hostileTitle = `My "Notes" \ C:\path <b>bold</b> & &amp; </title><script>window.titleProbe=1</script>`

func TestSurfacePresets_TitleIsEscaped(t *testing.T) {
	for _, preset := range []string{"surface-app", "surface-desk"} {
		t.Run(preset, func(t *testing.T) {
			dir := renderSurface(t, preset, map[string]any{"repo_name": "escape-probe", "title": hostileTitle})

			// HTML: the title is element text and nothing else; no markup escapes it.
			page := readFile(t, filepath.Join(dir, "index.html"))
			head, _, _ := strings.Cut(page, "</head>")
			if strings.Contains(head, "<script") || strings.Count(page, "</title>") != 1 {
				t.Fatalf("index.html carries the title as markup:\n%s", page)
			}
			start := strings.Index(page, "<title>") + len("<title>")
			end := strings.Index(page, "</title>")
			if got := html.UnescapeString(page[start:end]); got != hostileTitle {
				t.Errorf("index.html title reads %q, want %q", got, hostileTitle)
			}

			parseEveryDataFile(t, dir)
			if preset == "surface-desk" {
				checkGoTitle(t, filepath.Join(dir, "main.go"))
			}
		})
	}
}

// checkGoTitle parses and gofmt-checks main.go and reads the Title literal back.
func checkGoTitle(t *testing.T, path string) {
	t.Helper()
	src := readFile(t, path)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		t.Fatalf("main.go is not valid Go: %v", err)
	}
	if string(formatted) != src {
		t.Error("main.go is not gofmt-clean")
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Title" {
			lit, ok := kv.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("Title is not a string literal")
			}
			got, err = strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("main.go has no Title field")
	}
	if got != hostileTitle {
		t.Errorf("main.go Title is %q, want %q", got, hostileTitle)
	}
}

// parseEveryDataFile parses every generated JSON and YAML file, so a value written into one cannot break it.
func parseEveryDataFile(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		var v any
		switch filepath.Ext(path) {
		case ".json":
			if err := json.Unmarshal([]byte(readFile(t, path)), &v); err != nil {
				t.Errorf("%s is not valid JSON: %v", path, err)
			}
		case ".yml", ".yaml":
			if err := yaml.Unmarshal([]byte(readFile(t, path)), &v); err != nil {
				t.Errorf("%s is not valid YAML: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A title is one line: control characters (a newline would break the Markdown heading) are refused
// before anything is written.
func TestSurfacePresets_TitleIsOneLine(t *testing.T) {
	for _, preset := range []string{"surface-app", "surface-desk"} {
		for _, title := range []string{"Two\nLines", "Tab\there", "Bell\x07"} {
			target := filepath.Join(t.TempDir(), "app")
			svc := service.New(service.Options{BundledFS: folio.BundledPresets, BundledRoot: "presets", UserDir: t.TempDir(), FolioVersion: folio.Version})
			_, err := svc.New(service.NewOptions{PresetID: preset, TargetDir: target, Inputs: map[string]any{"repo_name": "line-probe", "title": title}})
			if err == nil {
				t.Errorf("%s accepted the title %q", preset, title)
			}
		}
	}
}
