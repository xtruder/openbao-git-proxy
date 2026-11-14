// Package backend is the git-proxy OpenBao secrets plugin. It holds the
// proxy's configuration, the upstream credentials and the Allowed
// Repositories, and serves the Access Paths that Grants name. It never
// proxies git itself: plugins can't stream git's traffic (ADR-0001).
package backend

import (
	"context"
	"strings"
	"sync"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

const backendHelp = `
Configuration of openbao-git-proxy: upstream credentials, the repositories
the proxy forwards, and the access/<name> paths that Grants name (read
fetches, update pushes).
`

// version is set by the linker for releases.
var version string

type backend struct {
	*framework.Backend

	// writeLock serializes writes, so a credential can't be deleted while a
	// repository that names it is being written.
	writeLock sync.Mutex
}

// Factory creates the plugin's backend.
func Factory(ctx context.Context, config *logical.BackendConfig) (logical.Backend, error) {
	b := &backend{}
	b.Backend = &framework.Backend{
		Help:        strings.TrimSpace(backendHelp),
		BackendType: logical.TypeLogical,
		PathsSpecial: &logical.Paths{
			SealWrapStorage: []string{"credential/"},
		},
		Paths: []*framework.Path{
			b.pathConfig(),
			b.pathCredential(),
			b.pathCredentials(),
			b.pathRepository(),
			b.pathRepositories(),
			b.pathAccess(),
		},
		RunningVersion: version,
	}

	if err := b.Setup(ctx, config); err != nil {
		return nil, err
	}

	return b, nil
}

// namePattern is the one path segment naming a credential or repository;
// handlers validate it, so the pattern only keeps it to one segment.
const namePattern = `(?P<name>[^/]+)`

var nameField = map[string]*framework.FieldSchema{
	"name": {Type: framework.TypeString, Description: "Name."},
}

func existenceCheck(prefix string) framework.ExistenceFunc {
	return func(ctx context.Context, request *logical.Request, data *framework.FieldData) (bool, error) {
		entry, err := request.Storage.Get(ctx, prefix+data.Get("name").(string))
		return entry != nil, err
	}
}
