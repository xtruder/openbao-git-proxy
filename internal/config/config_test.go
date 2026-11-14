package config

import (
	"os"
	"path/filepath"
	"slices"
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

upstream {
  credential_path = "kv/data/git-proxy/github"
}

access {
  repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]
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

func TestLoadAppliesDefaultsAndNormalises(t *testing.T) {
	t.Setenv("TEST_DOMAIN", "example.com")

	cfg, err := Load(writeConfig(t, validConfig))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddress != "127.0.0.1:8080" || cfg.OpenBaoAddress != "https://bao.example.com" || cfg.OpenBaoToken != "s.proxy" {
		t.Fatalf("server/openbao = %#v", cfg)
	}

	if cfg.Upstream.String() != "https://github.com" || cfg.CredentialPath != "kv/data/git-proxy/github" ||
		cfg.CredentialField != "token" || cfg.RefreshInterval != 5*time.Minute {
		t.Fatalf("upstream = %#v", cfg)
	}

	if cfg.MarkerPrefix != "git-proxy" || !slices.Equal(cfg.Repositories, []string{"acmecorp/widgetshop", "xtruder/homelab"}) {
		t.Fatalf("access = %q %q", cfg.MarkerPrefix, cfg.Repositories)
	}
}

func TestLoadRefusesInvalidConfiguration(t *testing.T) {
	t.Setenv("TEST_DOMAIN", "example.com")

	for _, test := range []struct{ old, new, error string }{
		{`listen_address = "127.0.0.1:8080"`, `listen_address = ""`, "listen_address"},
		{`token_file = "proxy-token"`, `token_file = "missing"`, "token_file"},
		{`address    = "https://bao.${env.TEST_DOMAIN}"`, `address = "bao.example.com"`, "openbao.address"},
		{`credential_path = "kv/data/git-proxy/github"`, `credential_path = ""`, "credential_path"},
		{`credential_path = "kv/data/git-proxy/github"`, `credential_path = "kv/x"
  url = "https://github.com/api"`, "upstream.url"},
		{`credential_path = "kv/data/git-proxy/github"`, `credential_path = "kv/x"
  refresh_interval = "soon"`, "refresh_interval"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = []`, "repositories"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = ["acmecorp"]`, "owner/repo"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = ["a/b/c"]`, "owner/repo"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = ["a/b", "A/B.git"]`, "duplicate"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = ["acmecorp/Auth"]`, "can't be granted"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = ["a/b"]
  marker_prefix = "git-proxy/*"`, "marker_prefix"},
		{`repositories = ["AcmeCorp/WidgetShop.git", "xtruder/homelab"]`, `repositories = ["a/b"]
  marker_prefix = "secret/Auth/git"`, "marker_prefix"},
	} {
		contents := strings.Replace(validConfig, test.old, test.new, 1)
		if _, err := Load(writeConfig(t, contents)); err == nil || !strings.Contains(err.Error(), test.error) {
			t.Fatalf("with %s: err = %v, want one mentioning %q", test.new, err, test.error)
		}
	}
}
