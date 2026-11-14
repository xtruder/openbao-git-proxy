// Package config loads and validates runtime configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/xtruder/openbao-git-proxy/internal/gitroute"
	"github.com/zclconf/go-cty/cty"
)

const (
	defaultUpstream        = "https://github.com"
	defaultCredentialField = "token"
	defaultRefreshInterval = 5 * time.Minute
	defaultMarkerPrefix    = "git-proxy"
)

// deniedSegments are the segments of the authorizer's Hard Denylist: a
// marker path containing one could never be granted.
var deniedSegments = []string{"sys", "auth", "identity", "cubbyhole"}

// Config is the complete validated configuration.
type Config struct {
	ListenAddress    string
	OpenBaoAddress   string
	OpenBaoNamespace string
	OpenBaoCAFile    string
	OpenBaoToken     string
	Upstream         *url.URL
	CredentialPath   string
	CredentialField  string
	RefreshInterval  time.Duration
	MarkerPrefix     string
	// Repositories are lowercased owner/repo names without .git.
	Repositories []string
}

type fileConfig struct {
	Server   serverConfig   `hcl:"server,block"`
	OpenBao  openBaoConfig  `hcl:"openbao,block"`
	Upstream upstreamConfig `hcl:"upstream,block"`
	Access   accessConfig   `hcl:"access,block"`
}

type serverConfig struct {
	ListenAddress string `hcl:"listen_address"`
}

type openBaoConfig struct {
	Address   string `hcl:"address"`
	Namespace string `hcl:"namespace,optional"`
	CAFile    string `hcl:"ca_file,optional"`
	TokenFile string `hcl:"token_file"`
}

type upstreamConfig struct {
	URL             string `hcl:"url,optional"`
	CredentialPath  string `hcl:"credential_path"`
	CredentialField string `hcl:"credential_field,optional"`
	RefreshInterval string `hcl:"refresh_interval,optional"`
}

type accessConfig struct {
	MarkerPrefix string   `hcl:"marker_prefix,optional"`
	Repositories []string `hcl:"repositories"`
}

// Load reads one HCL configuration and the token file it references.
func Load(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("configuration file path is required")
	}

	parser := hclparse.NewParser()
	file, diagnostics := parser.ParseHCLFile(path)
	if diagnostics.HasErrors() {
		return Config{}, fmt.Errorf("parse configuration: %s", diagnostics.Error())
	}

	var raw fileConfig
	if diagnostics := gohcl.DecodeBody(file.Body, environmentEvalContext(), &raw); diagnostics.HasErrors() {
		return Config{}, fmt.Errorf("decode configuration: %s", diagnostics.Error())
	}

	baseDirectory, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration directory: %w", err)
	}

	resolvePath := func(value string) string {
		if value == "" || filepath.IsAbs(value) {
			return value
		}

		return filepath.Join(baseDirectory, value)
	}

	cfg := Config{
		ListenAddress:    strings.TrimSpace(raw.Server.ListenAddress),
		OpenBaoAddress:   strings.TrimSpace(raw.OpenBao.Address),
		OpenBaoNamespace: raw.OpenBao.Namespace,
		OpenBaoCAFile:    resolvePath(raw.OpenBao.CAFile),
		CredentialPath:   strings.Trim(raw.Upstream.CredentialPath, "/"),
		CredentialField:  orDefault(raw.Upstream.CredentialField, defaultCredentialField),
		MarkerPrefix:     strings.Trim(orDefault(raw.Access.MarkerPrefix, defaultMarkerPrefix), "/"),
	}

	if cfg.ListenAddress == "" {
		return Config{}, errors.New("server.listen_address is required")
	}

	if address, parseErr := url.Parse(cfg.OpenBaoAddress); parseErr != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
		return Config{}, errors.New("openbao.address must be an http or https URL with a host")
	}

	if cfg.OpenBaoToken, err = readFile(resolvePath(raw.OpenBao.TokenFile), "openbao.token_file"); err != nil {
		return Config{}, err
	}

	if cfg.Upstream, err = parseUpstream(orDefault(raw.Upstream.URL, defaultUpstream)); err != nil {
		return Config{}, err
	}

	if cfg.CredentialPath == "" {
		return Config{}, errors.New("upstream.credential_path is required")
	}

	cfg.RefreshInterval = defaultRefreshInterval
	if raw.Upstream.RefreshInterval != "" {
		cfg.RefreshInterval, err = time.ParseDuration(raw.Upstream.RefreshInterval)
		if err != nil || cfg.RefreshInterval <= 0 {
			return Config{}, errors.New("upstream.refresh_interval must be a positive duration such as 5m")
		}
	}

	if err = validateMarkerPrefix(cfg.MarkerPrefix); err != nil {
		return Config{}, err
	}

	if cfg.Repositories, err = parseRepositories(raw.Access.Repositories); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// parseUpstream accepts only a scheme and host: the proxy builds every path.
func parseUpstream(value string) (*url.URL, error) {
	upstream, err := url.Parse(value)
	if err != nil || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" ||
		strings.Trim(upstream.Path, "/") != "" || upstream.RawQuery != "" || upstream.User != nil {
		return nil, errors.New("upstream.url must be a scheme and host only, such as https://github.com")
	}

	return &url.URL{Scheme: upstream.Scheme, Host: upstream.Host}, nil
}

// validateMarkerPrefix requires a concrete path that the authorizer's Hard
// Denylist doesn't refuse, or no marker path under it could be granted.
func validateMarkerPrefix(prefix string) error {
	for segment := range strings.SplitSeq(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "*+") ||
			slices.Contains(deniedSegments, strings.ToLower(segment)) {
			return fmt.Errorf("access.marker_prefix %q must be a concrete OpenBao path without a sys, auth, identity or cubbyhole segment", prefix)
		}
	}

	return nil
}

func parseRepositories(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("access.repositories must list at least one owner/repo")
	}

	repositories := make([]string, 0, len(values))
	for _, value := range values {
		owner, repo, ok := strings.Cut(strings.ToLower(value), "/")
		repo = strings.TrimSuffix(repo, ".git")
		if !ok || !gitroute.ValidName(owner) || !gitroute.ValidName(repo) {
			return nil, fmt.Errorf("access.repositories: %q is not owner/repo", value)
		}

		if slices.Contains(deniedSegments, owner) || slices.Contains(deniedSegments, repo) {
			return nil, fmt.Errorf("access.repositories: %q can't be granted: the authorizer refuses paths with a %s segment", value, strings.Join(deniedSegments, ", "))
		}

		repository := owner + "/" + repo
		if slices.Contains(repositories, repository) {
			return nil, fmt.Errorf("access.repositories: duplicate %q", value)
		}

		repositories = append(repositories, repository)
	}

	return repositories, nil
}

func orDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value == "" {
		return fallback
	}

	return value
}

func environmentEvalContext() *hcl.EvalContext {
	values := make(map[string]cty.Value)
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = cty.StringVal(value)
	}

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"env": cty.ObjectVal(values),
		},
	}
}

func readFile(path, field string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s is required", field)
	}

	contents, err := os.ReadFile(path) // #nosec G304 -- paths come from operator configuration.
	if err != nil {
		return "", fmt.Errorf("read %s: %w", field, err)
	}

	token := strings.TrimSpace(string(contents))
	if token == "" {
		return "", fmt.Errorf("%s is empty", field)
	}

	return token, nil
}
