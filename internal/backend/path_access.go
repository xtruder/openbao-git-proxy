package backend

import (
	"context"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathAccess serves the Access Paths that Grants name. The proxy checks a
// Requester's capabilities here: read fetches, update pushes. Only reads
// are handled, so holding update lets a Requester write nothing.
func (b *backend) pathAccess() *framework.Path {
	return &framework.Path{
		Pattern: "access/" + namePattern,
		Fields:  nameField,
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{Callback: b.readAccess},
		},
		HelpSynopsis: "Grant read here to fetch the repository through the proxy, update to push.",
	}
}

// readAccess tells a Requester how to clone the repository, and nothing
// about its upstream.
func (b *backend) readAccess(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	r, err := loadRepository(ctx, request.Storage, name)
	if err != nil || r == nil {
		return nil, err
	}

	response := map[string]any{"name": name}
	c, err := loadConfig(ctx, request.Storage)
	if err != nil {
		return nil, err
	}

	if c.ProxyURL != "" {
		response["clone_url"] = c.ProxyURL + "/" + name + ".git"
	}

	return &logical.Response{Data: response}, nil
}
