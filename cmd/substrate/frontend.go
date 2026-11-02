package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/substrate/bom"
	"github.com/hollis-labs/substrate/catalog"
	"github.com/hollis-labs/substrate/frontend"
	"github.com/hollis-labs/substrate/manifest"
	"github.com/hollis-labs/substrate/ui"
)

// writeFrontend generates frontend.manifest.json. Module ui.json files and the
// BOM are read from the Substrate version the app's go.mod resolves (run from
// the manifest's directory), so the manifest matches the code that will link.
func writeFrontend(m *manifest.Manifest, appDir, outDir string) error {
	root, err := goList(appDir, "-m", "-f", "{{.Dir}}", "github.com/hollis-labs/substrate")
	if err != nil {
		return fmt.Errorf("find the substrate module: %w", err)
	}
	b, err := bom.Load(filepath.Join(root, "bom", "bom.yaml"))
	if err != nil {
		return err
	}
	load := func(name string) ([]byte, bool, error) {
		cm, ok := catalog.Find(name)
		if !ok {
			return nil, false, fmt.Errorf("not in the catalogue")
		}
		dir, err := goList(appDir, "-f", "{{.Dir}}", cm.Import)
		if err != nil {
			return nil, false, err
		}
		data, err := os.ReadFile(filepath.Join(dir, "ui.json"))
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return data, err == nil, err
	}
	fm, err := frontend.Build(m, b, ui.Default, load)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(fm, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(outDir, "frontend.manifest.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func goList(dir string, args ...string) (string, error) {
	c := exec.Command("go", append([]string{"list"}, args...)...)
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
