package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
	"github.com/xtruder/openbao-git-proxy/internal/gitroute"
)

const credentialPrefix = "credential/" // #nosec G101 -- a storage prefix, not a credential.

// credential is what the proxy sends upstream with HTTP Basic. Hosts differ
// only in the username they expect.
type credential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (b *backend) pathCredential() *framework.Path {
	return &framework.Path{
		Pattern: "credential/" + namePattern,
		Fields: map[string]*framework.FieldSchema{
			"name":     nameField["name"],
			"username": {Type: framework.TypeString, Description: "Username sent upstream, such as x-access-token (GitHub) or oauth2 (GitLab)."},
			"password": {Type: framework.TypeString, Description: "Token sent upstream as the password.", DisplayAttrs: &framework.DisplayAttributes{Sensitive: true}},
		},
		ExistenceCheck: existenceCheck(credentialPrefix),
		Operations: map[logical.Operation]framework.OperationHandler{
			// Read returns the password: only the proxy's policy should
			// allow it, while operators get create, update and delete.
			logical.ReadOperation:   &framework.PathOperation{Callback: b.readCredential},
			logical.CreateOperation: &framework.PathOperation{Callback: b.writeCredential},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.writeCredential},
			logical.DeleteOperation: &framework.PathOperation{Callback: b.deleteCredential},
		},
		HelpSynopsis: "An upstream username and password; repositories name one.",
	}
}

func (b *backend) pathCredentials() *framework.Path {
	return &framework.Path{
		Pattern: "credentials/?$",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{
				Callback: func(ctx context.Context, request *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
					keys, err := request.Storage.List(ctx, credentialPrefix)
					return logical.ListResponse(keys), err
				},
			},
		},
		HelpSynopsis: "List credential names.",
	}
}

func loadCredential(ctx context.Context, storage logical.Storage, name string) (*credential, error) {
	entry, err := storage.Get(ctx, credentialPrefix+name)
	if err != nil || entry == nil {
		return nil, err
	}

	var c credential
	return &c, entry.DecodeJSON(&c)
}

func (b *backend) readCredential(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	c, err := loadCredential(ctx, request.Storage, data.Get("name").(string))
	if err != nil || c == nil {
		return nil, err
	}

	return &logical.Response{Data: map[string]any{"username": c.Username, "password": c.Password}}, nil
}

func (b *backend) writeCredential(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	if !gitroute.ValidName(name) {
		return logical.ErrorResponse("invalid credential name %q: use lowercase letters, digits, '-', '_' and '.'", name), nil
	}

	b.writeLock.Lock()
	defer b.writeLock.Unlock()

	// An update keeps the fields it doesn't name, so rotating a token is a
	// password-only write.
	c := credential{}
	if existing, err := loadCredential(ctx, request.Storage, name); err != nil {
		return nil, err
	} else if existing != nil {
		c = *existing
	}

	if value, ok := data.GetOk("username"); ok {
		c.Username = value.(string)
	}

	if value, ok := data.GetOk("password"); ok {
		c.Password = value.(string)
	}

	if c.Username == "" || c.Password == "" || strings.ContainsAny(c.Username, ":\r\n") || strings.ContainsAny(c.Password, "\r\n") {
		return logical.ErrorResponse("username and password are required; the username can't contain ':' and neither can contain newlines"), nil
	}

	entry, err := logical.StorageEntryJSON(credentialPrefix+name, c)
	if err != nil {
		return nil, err
	}

	return nil, request.Storage.Put(ctx, entry)
}

func (b *backend) deleteCredential(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)
	b.writeLock.Lock()
	defer b.writeLock.Unlock()

	repositories, err := loadRepositories(ctx, request.Storage)
	if err != nil {
		return nil, err
	}

	var users []string
	for repositoryName, r := range repositories {
		if r.Credential == name {
			users = append(users, repositoryName)
		}
	}

	if len(users) > 0 {
		return logical.ErrorResponse("credential %q is used by %s", name, strings.Join(users, ", ")), nil
	}

	if err := request.Storage.Delete(ctx, credentialPrefix+name); err != nil {
		return nil, fmt.Errorf("delete credential: %w", err)
	}

	return nil, nil
}
