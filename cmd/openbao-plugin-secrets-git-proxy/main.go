// Command openbao-plugin-secrets-git-proxy is the OpenBao secrets plugin
// that holds openbao-git-proxy's configuration: upstream credentials, the
// repositories it forwards, and the Access Paths that Grants name.
package main

import (
	"os"

	"github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/api/v2"
	"github.com/openbao/openbao/sdk/v2/plugin"
	"github.com/xtruder/openbao-git-proxy/internal/backend"
)

func main() {
	meta := &api.PluginAPIClientMeta{}
	if err := meta.FlagSet().Parse(os.Args[1:]); err != nil {
		fatal(err)
	}

	if err := plugin.ServeMultiplex(&plugin.ServeOpts{
		BackendFactoryFunc: backend.Factory,
		TLSProviderFunc:    api.VaultPluginTLSProvider(meta.GetTLSConfig()),
	}); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	hclog.New(&hclog.LoggerOptions{}).Error("plugin shutting down", "error", err)
	os.Exit(1)
}
