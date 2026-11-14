package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validConfig = `
server {
  listen_address = "127.0.0.1:8080"
}

openbao {
  address    = "https://bao.${env.TEST_DOMAIN}"
  token_file = "proxy-token"
}
`

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "proxy-token"), []byte("s.proxy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "proxy.hcl")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("TEST_DOMAIN", "example.com")

	cfg, err := Load(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddress != "127.0.0.1:8080" || cfg.OpenBaoAddress != "https://bao.example.com" || cfg.OpenBaoToken != "s.proxy" ||
		cfg.Mount != "git-proxy" || cfg.RefreshInterval != time.Minute {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadRefusesInvalidConfiguration(t *testing.T) {
	t.Setenv("TEST_DOMAIN", "example.com")

	for _, test := range []struct{ old, new, error string }{
		{`listen_address = "127.0.0.1:8080"`, `listen_address = ""`, "listen_address"},
		{`token_file = "proxy-token"`, `token_file = "missing"`, "token_file"},
		{`address    = "https://bao.${env.TEST_DOMAIN}"`, `address = "bao.example.com"`, "openbao.address"},
		{`token_file = "proxy-token"`, `token_file = "proxy-token"
  refresh_interval = "soon"`, "refresh_interval"},
		{`token_file = "proxy-token"`, `token_file = "proxy-token"
  mount = "git/*"`, "mount"},
		{`token_file = "proxy-token"`, `token_file = "proxy-token"
  mount = "sys/git"`, "mount"},
	} {
		contents := strings.Replace(validConfig, test.old, test.new, 1)
		if _, err := Load(writeConfig(t, contents)); err == nil || !strings.Contains(err.Error(), test.error) {
			t.Fatalf("with %s: err = %v, want one mentioning %q", test.new, err, test.error)
		}
	}
}
