// Package deployment projects Folio-owned settings.runtime into fleet/Cerberus
// files after validating the authored descriptor with Substrate's shared kinds.
package deployment

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"text/template"

	"github.com/hollis-labs/substrate/kinds"
	"gopkg.in/yaml.v3"
)

//go:embed templates/*.tmpl
var templates embed.FS

// Runtime is Folio's temporary projection inside project/1's extensible settings.
// Credentials are never accepted here: EnvFile is an operator-owned reference.
type Runtime struct {
	OS             string `yaml:"target_os"`
	Arch           string `yaml:"target_arch"`
	Host           string `yaml:"host"`
	Port           int    `yaml:"http_port,omitempty"`
	Link           string `yaml:"binary_link"`
	InstallRoot    string `yaml:"install_root"`
	BackupDatabase string `yaml:"backup_database,omitempty"`
	EnvFile        string `yaml:"env_file,omitempty"`
}

type Result struct {
	Project kinds.Project
	Runtime Runtime
	Files   map[string][]byte
}

var slug = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var host = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Render validates project structure and returns nil when no Folio runtime
// projection is declared. Other settings are not interpreted or rewritten.
func Render(data []byte) (*Result, error) {
	p, err := kinds.ParseProjectYAML(data)
	if err != nil {
		return nil, fmt.Errorf("project.yaml: %w", err)
	}
	runtime, ok := p.Settings["runtime"]
	if !ok {
		return nil, nil
	}
	var r Runtime
	raw, err := yaml.Marshal(runtime)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err = dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("settings.runtime: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("deployment needs a nonblank project name")
	}
	if p.Class != kinds.ClassApp && p.Class != kinds.ClassTool {
		return nil, fmt.Errorf("runtime projection supports class 1 and 2 projects")
	}
	u, err := url.Parse(p.Repo)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("repo must be an https://github.com/owner/repo URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || !slug.MatchString(parts[0]) || !slug.MatchString(parts[1]) {
		return nil, fmt.Errorf("repo must name an owner/repository")
	}
	service := false
	for _, i := range p.Interfaces {
		if i.Type == "daemon" {
			service = true
		}
	}
	if p.Deploy == nil {
		return nil, fmt.Errorf("runtime projection requires deployment metadata")
	}
	if p.Deploy.Fleet != "deploy.yaml" {
		return nil, fmt.Errorf("deploy.fleet must be deploy.yaml")
	}
	if service && p.Deploy.Cerberus != p.ID+".cerberus.yaml" {
		return nil, fmt.Errorf("daemon deploy.cerberus must be %s.cerberus.yaml", p.ID)
	}
	if !service && p.Deploy.Cerberus != "" {
		return nil, fmt.Errorf("cerberus requires a daemon interface")
	}
	if r.OS != "linux" && r.OS != "darwin" {
		return nil, fmt.Errorf("runtime.target_os must be linux or darwin")
	}
	if r.Arch != "amd64" && r.Arch != "arm64" {
		return nil, fmt.Errorf("runtime.target_arch must be amd64 or arm64")
	}
	if !host.MatchString(r.Host) {
		return nil, fmt.Errorf("runtime.host must be a fleet inventory alias")
	}
	if service && (r.Port < 1 || r.Port > 65535) {
		return nil, fmt.Errorf("runtime.http_port must be 1..65535")
	}
	for key, value := range map[string]string{"binary_link": r.Link, "install_root": r.InstallRoot, "env_file": r.EnvFile, "backup_database": r.BackupDatabase} {
		if (key == "env_file" || key == "backup_database") && value == "" {
			continue
		}
		if (!strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "~/")) || strings.ContainsAny(value, "\r\n\x00") || path.Clean(value) != value {
			return nil, fmt.Errorf("runtime.%s must be an absolute or home-relative clean path", key)
		}
	}
	ctx := struct {
		ID, Name, Repo, EnvPrefix string
		Service                   bool
		Runtime                   Runtime
	}{p.ID, p.Name, strings.Join(parts, "/"), strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_")), service, r}
	result := &Result{Project: *p, Runtime: r, Files: map[string][]byte{}}
	for filename, templateName := range map[string]string{p.Deploy.Fleet: "fleet.tmpl", p.Deploy.Cerberus: "cerberus.tmpl"} {
		if filename == "" {
			continue
		}
		tpl, err := template.New(templateName).Funcs(template.FuncMap{"yaml": func(v string) (string, error) { b, e := json.Marshal(v); return strings.TrimSpace(string(b)), e }}).ParseFS(templates, "templates/"+templateName)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, ctx); err != nil {
			return nil, err
		}
		result.Files[filename] = buf.Bytes()
	}
	return result, nil
}
