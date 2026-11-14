package backend

import (
	"context"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
	"github.com/xtruder/openbao-git-proxy/internal/gitroute"
)

const (
	repositoryPrefix = "repo/"
	// defaultCredential is used by repositories that name none; usually the
	// GitHub token.
	defaultCredential = "default"
)

// repository is one Allowed Repository: where the proxy forwards
// /<name>.git and with which credential.
type repository struct {
	URL        string `json:"url"`
	Credential string `json:"credential"`
}

func (b *backend) pathRepository() *framework.Path {
	return &framework.Path{
		Pattern: "repo/" + namePattern,
		Fields: map[string]*framework.FieldSchema{
			"name":       nameField["name"],
			"url":        {Type: framework.TypeString, Description: "The repository's HTTPS clone URL upstream."},
			"credential": {Type: framework.TypeString, Description: "Name of the credential to send upstream (default \"default\" on create)."},
		},
		ExistenceCheck: existenceCheck(repositoryPrefix),
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.readRepository},
			logical.CreateOperation: &framework.PathOperation{Callback: b.writeRepository},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.writeRepository},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.deleteRepository},
		},
		HelpSynopsis: "A repository the proxy forwards as /<name>.git.",
	}
}

func (b *backend) pathRepositories() *framework.Path {
	return &framework.Path{
		Pattern: "repos/?$",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{
				Callback: func(ctx context.Context, request *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
					keys, err := request.Storage.List(ctx, repositoryPrefix)
					return logical.ListResponse(keys), err
				},
			},
		},
		HelpSynopsis: "List repository names; the authorizer's discover path for access/*.",
	}
}

func loadRepository(ctx context.Context, storage logical.Storage, name string) (*repository, error) {
	entry, err := storage.Get(ctx, repositoryPrefix+name)
	if err != nil || entry == nil {
		return nil, err
	}

	var r repository
	return &r, entry.DecodeJSON(&r)
}

func loadRepositories(ctx context.Context, storage logical.Storage) (map[string]repository, error) {
	names, err := storage.List(ctx, repositoryPrefix)
	if err != nil {
		return nil, err
	}

	repositories := make(map[string]repository, len(names))
	for _, name := range names {
		r, err := loadRepository(ctx, storage, name)
		if err != nil {
			return nil, err
		}

		if r != nil {
			repositories[name] = *r
		}
	}

	return repositories, nil
}

func (b *backend) readRepository(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	r, err := loadRepository(ctx, request.Storage, data.Get("name").(string))
	if err != nil || r == nil {
		return nil, err
	}

	return &logical.Response{Data: map[string]any{"url": r.URL, "credential": r.Credential}}, nil
}

func (b *backend) writeRepository(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	if !gitroute.ValidRepositoryName(name) {
		return logical.ErrorResponse("invalid repository name %q: use lowercase letters, digits, '-', '_' and '.', "+
			"without a .git suffix, and not sys, auth, identity or cubbyhole, which the authorizer never grants", name), nil
	}

	b.writeLock.Lock()
	defer b.writeLock.Unlock()

	// An update keeps the fields it doesn't name: resetting the credential to
	// the default would send another host's token, such as GitHub's, to this
	// repository's host.
	r := repository{Credential: defaultCredential}
	if existing, err := loadRepository(ctx, request.Storage, name); err != nil {
		return nil, err
	} else if existing != nil {
		r = *existing
	}

	if value, ok := data.GetOk("url"); ok {
		r.URL = value.(string)
	}

	if value, ok := data.GetOk("credential"); ok {
		r.Credential = value.(string)
	}

	if err := gitroute.ValidUpstreamURL(r.URL); err != nil {
		return logical.ErrorResponse(err.Error()), nil //nolint:nilerr // A bad URL is the caller's error, reported as such.
	}

	credential, err := loadCredential(ctx, request.Storage, r.Credential)
	if err != nil {
		return nil, err
	}

	if credential == nil {
		return logical.ErrorResponse("credential %q doesn't exist; write credential/%s first", r.Credential, r.Credential), nil
	}

	entry, err := logical.StorageEntryJSON(repositoryPrefix+name, r)
	if err != nil {
		return nil, err
	}

	return nil, request.Storage.Put(ctx, entry)
}

func (b *backend) deleteRepository(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	b.writeLock.Lock()
	defer b.writeLock.Unlock()
	return nil, request.Storage.Delete(ctx, repositoryPrefix+data.Get("name").(string))
}
