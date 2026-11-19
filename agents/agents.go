// Package agents defines the host-owned agent directory convention. It deals
// only in paths and names; it does not load agents or create runtime state.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigBlock is the shared app config block, independent of selected modules.
const ConfigBlock = "agents"

// DirKey names agents.dir within ConfigBlock.
const DirKey = "dir"

// DefaultDir is resolved beside the app's config file.
const DefaultDir = "agents/"

// Layout holds absolute authored and runtime roots. Construct it with Resolve.
type Layout struct {
	dir, stateRoot string
}

// Resolve reads agents.dir from the app's merged config. An absent or null key
// defaults to agents/ beside configFile; relative overrides use that same base.
// Absolute overrides are kept. configFile must name the host's config file (it
// need not exist); stateRoot is the host's runtime data directory. Both arguments
// are required; relative arguments are made absolute against the working directory.
// Call after the config module has applied SUBSTRATE_CONFIG, supplying that file
// as configFile when used. Resolve neither reads configFile nor creates paths.
func Resolve(config map[string]any, configFile, stateRoot string) (Layout, error) {
	if configFile == "" || stateRoot == "" {
		return Layout{}, fmt.Errorf("agents: config file and state root are required")
	}
	dir := DefaultDir
	if v := config[ConfigBlock]; v != nil {
		block, ok := v.(map[string]any)
		if !ok {
			return Layout{}, fmt.Errorf("agents: config block must be a map")
		}
		if v := block[DirKey]; v != nil {
			var ok bool
			dir, ok = v.(string)
			if !ok || strings.TrimSpace(dir) == "" {
				return Layout{}, fmt.Errorf("agents.dir must be a non-empty string")
			}
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(configFile), dir)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Layout{}, fmt.Errorf("agents.dir: %w", err)
	}
	stateRoot, err = filepath.Abs(stateRoot)
	if err != nil {
		return Layout{}, fmt.Errorf("agents state root: %w", err)
	}
	return Layout{dir: dir, stateRoot: stateRoot}, nil
}

// Dir returns the absolute directory containing authored agent subdirectories.
func (l Layout) Dir() string { return l.dir }

// Names lists immediate subdirectory names in lexical order, ignoring files and
// symlinks. A missing directory is an error, as are other filesystem failures.
func (l Layout) Names() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// AgentDir returns an agent's authored directory without requiring it to exist.
func (l Layout) AgentDir(name string) (string, error) {
	if l.dir == "" {
		return "", fmt.Errorf("agents: unresolved layout")
	}
	if err := checkName(name); err != nil {
		return "", err
	}
	return filepath.Join(l.dir, name), nil
}

// DataDir returns <stateRoot>/agents/<name>, distinct from authored files.
// It does not create the directory or resolve symlinks: the host owns the state
// root and its permissions. Names must be single local path components.
func (l Layout) DataDir(name string) (string, error) {
	if l.stateRoot == "" {
		return "", fmt.Errorf("agents: unresolved layout")
	}
	if err := checkName(name); err != nil {
		return "", err
	}
	return filepath.Join(l.stateRoot, ConfigBlock, name), nil
}

func checkName(name string) error {
	if name == "." || !filepath.IsLocal(name) || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("agents: name %q must be a single local path component", name)
	}
	return nil
}
