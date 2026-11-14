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
	"github.com/zclconf/go-cty/cty"
)

const (
	defaultMount           = "git-proxy"
	defaultRefreshInterval = time.Minute
)

// deniedSegments are the segments of openbao-authorizer's Hard Denylist: no
// Access Path under a mount containing one could be granted.
var deniedSegments = []string{"sys", "auth", "identity", "cubbyhole"}

// Config is the complete validated configuration. Everything else, the
// repositories and their credentials, lives in the git-proxy mount.
type Config struct {
	ListenAddress    string
	OpenBaoAddress   string
	OpenBaoNamespace string
	OpenBaoCAFile    string
	OpenBaoToken     string
	Mount            string
	RefreshInterval  time.Duration
}

type fileConfig struct {
	Server  serverConfig  `hcl:"server,block"`
	OpenBao openBaoConfig `hcl:"openbao,block"`
}

type serverConfig struct {
	ListenAddress string `hcl:"listen_address"`
}

type openBaoConfig struct {
	Address         string `hcl:"address"`
	Namespace       string `hcl:"namespace,optional"`
	CAFile          string `hcl:"ca_file,optional"`
	TokenFile       string `hcl:"token_file"`
	Mount           string `hcl:"mount,optional"`
	RefreshInterval string `hcl:"refresh_interval,optional"`
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
		Mount:            strings.Trim(orDefault(raw.OpenBao.Mount, defaultMount), "/"),
		RefreshInterval:  defaultRefreshInterval,
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

	if raw.OpenBao.RefreshInterval != "" {
		cfg.RefreshInterval, err = time.ParseDuration(raw.OpenBao.RefreshInterval)
		if err != nil || cfg.RefreshInterval <= 0 {
			return Config{}, errors.New("openbao.refresh_interval must be a positive duration such as 1m")
		}
	}

	if err = validateMount(cfg.Mount); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateMount requires a concrete path that the authorizer's Hard
// Denylist doesn't refuse, or no Access Path under it could be granted.
func validateMount(mount string) error {
	for segment := range strings.SplitSeq(mount, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "*+") ||
			slices.Contains(deniedSegments, strings.ToLower(segment)) {
			return fmt.Errorf("openbao.mount %q must be a concrete OpenBao path without a sys, auth, identity or cubbyhole segment", mount)
		}
	}

	return nil
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
