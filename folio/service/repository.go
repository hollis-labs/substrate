package service

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/tools/folio/internal/manifest"
)

// repositoryOptions resolves a caller-selected boundary. Neither a preset nor
// a recorded manifest can select another directory to write into.
func repositoryOptions(opts NewOptions) (NewOptions, string, string, error) {
	root, err := filepath.EvalSymlinks(opts.RepositoryRoot)
	if err != nil {
		return opts, "", "", fmt.Errorf("repository root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return opts, "", "", err
	}
	target, err := filepath.Abs(opts.TargetDir)
	if err != nil {
		return opts, "", "", err
	}
	// Resolve the existing parent, allowing the new module directory to be absent.
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return opts, "", "", fmt.Errorf("module parent must exist: %w", err)
	}
	target = filepath.Join(parent, filepath.Base(target))
	location, err := filepath.Rel(root, target)
	if err != nil || !within(root, target) || location == "." {
		return opts, "", "", fmt.Errorf("module target must be beneath the selected repository root")
	}
	location = filepath.ToSlash(location)
	opts.Inputs = maps.Clone(opts.Inputs)
	if opts.Inputs == nil {
		opts.Inputs = map[string]any{}
	}
	if supplied, ok := opts.Inputs["repository_location"]; ok && supplied != location {
		return opts, "", "", fmt.Errorf("repository_location must match the target directory (%s)", location)
	}
	opts.Inputs["repository_location"] = location
	if name, _ := opts.Inputs["repository_name"].(string); name == "" {
		return opts, "", "", fmt.Errorf("--repository-root requires --input repository_name=<github-repository>; checkout folder names are not repository identity")
	}
	opts.TargetDir = target
	opts.repositoryPrepared = true
	return opts, root, location, nil
}

func workflowDestination(source, name string) string {
	if path.Dir(source) != ".github/workflows" {
		return ""
	}
	return path.Join(".github/workflows", name+"-"+path.Base(source))
}

func (s *Service) planInRepository(opts NewOptions) (PlanResult, error) {
	opts, _, location, err := repositoryOptions(opts)
	if err != nil {
		return PlanResult{}, err
	}
	result, err := s.Plan(opts)
	if err != nil {
		return result, err
	}
	name, _ := result.Inputs["repo_name"].(string)
	for i := range result.Files {
		source := result.Files[i].Path
		if dest := workflowDestination(source, name); dest != "" {
			result.Files[i].Path = dest
		} else {
			result.Files[i].Path = path.Join(location, source)
		}
	}
	result.Warnings = append(result.Warnings, "Paths are relative to the selected repository root; existing workflows are never replaced.")
	return result, nil
}

func (s *Service) newInRepository(opts NewOptions) (NewResult, error) {
	opts, rootPath, location, err := repositoryOptions(opts)
	if err != nil {
		return NewResult{}, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return NewResult{}, err
	}
	defer root.Close()
	if _, err := root.Lstat(location); !os.IsNotExist(err) {
		return NewResult{}, fmt.Errorf("module target %s must not exist", location)
	}
	// Render completely before publishing any file. Staging stays on the same
	// filesystem as the destination so the final module rename is atomic.
	stage, err := os.MkdirTemp(rootPath, ".folio-stage-")
	if err != nil {
		return NewResult{}, err
	}
	defer os.RemoveAll(stage)
	opts.renderTarget = opts.TargetDir
	opts.TargetDir = stage
	result, err := s.New(opts)
	if err != nil {
		return NewResult{}, err
	}
	name, _ := result.Manifest.Inputs["repo_name"].(string)
	if !fs.ValidPath(name) || strings.Contains(name, "/") || name == "." {
		return NewResult{}, fmt.Errorf("repository generation requires a valid repo_name")
	}
	result.Manifest.RepositoryLocation = location
	var created []string
	published := false
	defer func() {
		if !published {
			for _, file := range created {
				_ = root.Remove(file)
			}
		}
	}()
	for i := range result.Files {
		f := &result.Files[i]
		source := f.Path
		dest := workflowDestination(source, name)
		if dest == "" {
			f.Path = path.Join(location, source)
			continue
		}
		data, err := os.ReadFile(filepath.Join(stage, source))
		if err != nil {
			return NewResult{}, err
		}
		if err := root.MkdirAll(path.Dir(dest), 0o755); err != nil {
			return NewResult{}, err
		}
		out, err := root.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return NewResult{}, fmt.Errorf("create repository workflow %s (existing files are preserved): %w", dest, err)
		}
		created = append(created, dest)
		_, writeErr := out.Write(data)
		closeErr := out.Close()
		if writeErr != nil {
			return NewResult{}, writeErr
		}
		if closeErr != nil {
			return NewResult{}, closeErr
		}
		if err := os.Remove(filepath.Join(stage, source)); err != nil {
			return NewResult{}, err
		}
		rec := result.Manifest.Files[source]
		rec.RepositoryPath = dest
		result.Manifest.Files[source] = rec
		f.Path = dest
	}
	_ = os.Remove(filepath.Join(stage, ".github", "workflows"))
	_ = os.Remove(filepath.Join(stage, ".github"))
	if err := manifest.Write(stage, result.Manifest); err != nil {
		return NewResult{}, err
	}
	// os.Root.Rename refuses an existing destination directory. A concurrent
	// module creator therefore cannot be overwritten.
	if err := root.Rename(filepath.Base(stage), location); err != nil {
		return NewResult{}, err
	}
	published = true
	result.Warnings = append(result.Warnings, "Paths are relative to the selected repository root; workflows were created there.")
	return result, nil
}
