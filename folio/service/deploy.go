package service

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"

	"github.com/hollis-labs/substrate/folio/internal/deployment"
	"github.com/hollis-labs/substrate/folio/internal/manifest"
	"gopkg.in/yaml.v3"
)

type DeployOptions struct {
	TargetDir string
	Write     bool
	// Overwrite explicitly accepts edits to generated files after reviewing a plan.
	Overwrite bool
}

type DeployFile struct {
	Path, Status string
	Content      []byte
}
type DeployResult struct {
	Warnings []string
	Files    []DeployFile
	Written  bool
}

// RenderDeploy only renders local files. It never installs a binary, starts a
// service, contacts a supervisor, or writes the authored project descriptor.
func (s *Service) RenderDeploy(opts DeployOptions) (DeployResult, error) {
	var result DeployResult
	root, err := os.OpenRoot(opts.TargetDir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	descriptorInfo, err := root.Lstat("project.yaml")
	if err != nil {
		return result, err
	}
	if !descriptorInfo.Mode().IsRegular() {
		return result, fmt.Errorf("project.yaml must be a regular authored file, not a symlink")
	}
	authored, err := root.ReadFile("project.yaml")
	if err != nil {
		return result, err
	}
	projection, err := deployment.Render(authored)
	if err != nil {
		return result, err
	}
	if projection == nil {
		return result, fmt.Errorf("project.yaml has no Folio settings.runtime projection")
	}
	rawManifest, err := root.ReadFile(manifest.ManifestFilename)
	if err != nil {
		return result, fmt.Errorf("deployment rendering requires the project's .folio.yaml: %w", err)
	}
	var m manifest.Manifest
	if err = yaml.Unmarshal(rawManifest, &m); err != nil {
		return result, err
	}
	if name, _ := m.Inputs["repo_name"].(string); name != projection.Project.ID {
		return result, fmt.Errorf("project id is immutable; descriptor id differs from generated project")
	}
	selected := projection.Runtime.OS + "/" + projection.Runtime.Arch
	builtOS, _ := m.Inputs["target_os"].(string)
	builtArch, _ := m.Inputs["target_arch"].(string)
	if builtOS+"/"+builtArch != selected {
		result.Warnings = append(result.Warnings, fmt.Sprintf("runtime selects %s; recorded build target is %s/%s. Update the CI build targets and produce a matching artifact before installation. This render does not modify CI.", selected, builtOS, builtArch))
	}
	before := map[string][]byte{}
	conflict := false
	for _, name := range slices.Sorted(maps.Keys(projection.Files)) {
		if info, statErr := root.Lstat(name); statErr == nil && os.SameFile(descriptorInfo, info) {
			return result, fmt.Errorf("deployment output %s aliases project.yaml", name)
		}
		data := projection.Files[name]
		prior, readErr := root.ReadFile(name)
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return result, readErr
		}
		before[name] = prior
		status := "unchanged"
		if !bytes.Equal(prior, data) {
			status = "replace"
			if errors.Is(readErr, fs.ErrNotExist) {
				status = "create"
			} else if rec, ok := m.Files[name]; !ok || rec.DigestAtGen != manifest.Digest(prior) {
				status = "conflict"
				conflict = true
			}
		}
		result.Files = append(result.Files, DeployFile{Path: name, Status: status, Content: data})
	}
	if !opts.Write {
		return result, nil
	}
	if conflict && !opts.Overwrite {
		return result, fmt.Errorf("generated deployment files have authored edits; review --plan then explicitly use --overwrite to replace them")
	}
	// All validation and conflict detection precedes the first write. Stage both
	// outputs and bookkeeping, then rename only regular files in the selected root.
	staged := map[string]string{}
	defer func() {
		for _, tmp := range staged {
			_ = root.Remove(tmp)
		}
	}()
	for _, f := range result.Files {
		if f.Status == "unchanged" {
			continue
		}
		rec := m.Files[f.Path]
		rec.DigestAtGen = manifest.Digest(f.Content)
		if rec.Preset == "" {
			rec.Preset = "project.yaml/settings.runtime"
		}
		if m.Files == nil {
			m.Files = map[string]manifest.FileRecord{}
		}
		m.Files[f.Path] = rec
		temp, stageErr := stageDeploy(root, f.Path, f.Content)
		if stageErr != nil {
			return result, stageErr
		}
		staged[f.Path] = temp
	}
	if len(staged) == 0 {
		return result, nil
	}
	manifestBytes, err := yaml.Marshal(m)
	if err != nil {
		return result, err
	}
	tmp, err := stageDeploy(root, manifest.ManifestFilename, manifestBytes)
	if err != nil {
		return result, err
	}
	staged[manifest.ManifestFilename] = tmp
	before[manifest.ManifestFilename] = rawManifest
	// Refuse descriptor edits that arrived while the plan was being assembled.
	current, err := root.ReadFile("project.yaml")
	if err != nil || !bytes.Equal(current, authored) {
		return result, fmt.Errorf("project.yaml changed during rendering; rerun the plan")
	}
	var replaced []string
	for _, name := range append(slices.Sorted(maps.Keys(projection.Files)), manifest.ManifestFilename) {
		temp, ok := staged[name]
		if !ok {
			continue
		}
		current, readErr := root.ReadFile(name)
		if (readErr != nil && !errors.Is(readErr, fs.ErrNotExist)) || !bytes.Equal(current, before[name]) {
			err = fmt.Errorf("%s changed during rendering", name)
		} else {
			err = root.Rename(temp, name)
		}
		if err != nil {
			// Best-effort rollback of our completed replacements; expose any rollback
			// error instead of claiming a partial application was successful.
			for _, done := range replaced {
				if before[done] == nil {
					err = errors.Join(err, root.Remove(done))
					continue
				}
				rollback, restoreErr := stageDeploy(root, done, before[done])
				if restoreErr == nil {
					restoreErr = root.Rename(rollback, done)
					_ = root.Remove(rollback)
				}
				err = errors.Join(err, restoreErr)
			}
			return result, err
		}
		replaced = append(replaced, name)
	}
	result.Written = true
	return result, nil
}

func stageDeploy(root *os.Root, name string, data []byte) (string, error) {
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing non-regular output %s", name)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	tmp := ".folio-render-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = root.Remove(tmp)
		return "", err
	}
	return tmp, nil
}
