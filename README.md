# OpenBao Git Proxy

Forwards git over HTTPS for configured repositories on any git host (GitHub, GitLab, Gitea/Forgejo, Bitbucket, Azure DevOps, self-hosted) with **Upstream Credentials** that only the proxy holds. A **Requester**, such as an AI agent's Agent Session, authenticates with its own OpenBao token, and the proxy forwards the request only when that token holds a **Grant** on the repository's **Access Path**: `read` on `git-proxy/access/<name>` to fetch, `update` to push. Grants come from [openbao-authorizer](https://github.com/xtruder/openbao-authorizer), so an Approver decides, per repository, who may use it and for how long.

Two programs come from this repository:

- **`openbao-plugin-secrets-git-proxy`**, an OpenBao secrets plugin whose mount holds the configuration: credentials, repositories and the Access Paths.
- **`openbao-git-proxy`**, the proxy, a separate service that reads the mount and forwards git.

See [`CONTEXT.md`](CONTEXT.md) for the vocabulary, [ADR-0001](docs/adr/0001-proxy-outside-openbao.md) for the proxy and its authorization, and [ADR-0002](docs/adr/0002-configuration-in-a-plugin-mount.md) for the plugin.

It exists for repositories no credential can be scoped to, such as an organization's GitHub repository where you are an outside collaborator: fine-grained PATs don't support those, a classic PAT reaches every repository you can, and a GitHub App needs an installation by the organization.

## Security model

- **Requester token:** git sends it as the HTTP Basic password; the username is ignored. The proxy uses it only for `sys/capabilities-self` on the Access Path, on every request, and never stores or logs it. Requests without one get `401` with a `Basic` challenge, so git asks its credential helper; OpenBao refusing the token also gets `401`.
- **Grant check:** fetching (`git-upload-pack`, including its `info/refs`) needs `read` or `update`, pushing (`git-receive-pack`) needs `update`; `root` allows both. Otherwise the proxy answers `403` with a message naming the missing Grant, which git shows. Revoking or expiring the Grant stops the next request.
- **Allowed traffic:** only `GET /<name>[.git]/info/refs?service=git-upload-pack|git-receive-pack` and `POST /<name>[.git]/git-upload-pack|git-receive-pack` for configured repository names. Anything else, including dumb HTTP, other query parameters, escaped characters, Git LFS and any host API, is `404` before a credential is asked for. Requests name the repository; upstream paths come only from the configuration.
- **Upstream Credentials:** the proxy reads them with its own **Proxy Token** and sends the repository's credential upstream as HTTP Basic. Only `Accept`, `Accept-Encoding`, `Content-Encoding`, `Content-Type`, `Git-Protocol` and `User-Agent` are forwarded; the client's `Authorization`, cookies and everything else are dropped, as are upstream `Set-Cookie` headers. An upstream `401` or `407` becomes `502`, not a challenge, so git keeps the Requester's token. An upstream redirect, such as for a renamed repository, becomes `502` too: git following it would leave the proxy and its checks behind. Upstream URLs must be `https`.
- **Streaming:** request and response bodies stream in both directions and responses flush immediately, so packs are never buffered and git shows the remote's progress.
- **Logs:** one JSON line per request with method, repository name, service, status and duration; never a token or password.
- **Threat model:** the proxy is not a boundary against code on its own host, which can read the Upstream Credentials and the Proxy Token. Requesters run elsewhere. The proxy deliberately doesn't run inside the plugin, so a bug in it can't reach OpenBao's own container (ADR-0002).

## Architecture

```text
agent ── git, Basic auth (password = Session Token) ──> openbao-git-proxy ── credential ──> git host
            /widgetshop.git                               │
                    Session Token: sys/capabilities-self  │ Proxy Token: list repos, read repos
                    on git-proxy/access/widgetshop        │ and credentials, renew-self
                                                          ▼
                    openbao-authorizer ── Grants ──>  OpenBao  [git-proxy/ mount: the plugin]
```

## The git-proxy mount

| Path | Who | What |
|---|---|---|
| `git-proxy/config` | operator | `proxy_url`, the proxy's public base URL, for clone URLs |
| `git-proxy/credential/<name>` | operator writes, proxy reads | `username`, `password` sent upstream |
| `git-proxy/credentials` | operator | LIST of credential names |
| `git-proxy/repo/<name>` | operator | `url` (HTTPS clone URL upstream), `credential` (default `default`) |
| `git-proxy/repos` | operator, proxy, authorizer | LIST of repository names |
| `git-proxy/access/<name>` | Requesters, through Grants | `read` fetches, `update` pushes; reading returns `name` and `clone_url` |

```sh
bao plugin register -sha256=<sha256> -command=openbao-plugin-secrets-git-proxy \
  secret openbao-plugin-secrets-git-proxy
bao secrets enable -path=git-proxy openbao-plugin-secrets-git-proxy

bao write git-proxy/config proxy_url=https://git-proxy.example.com
bao write git-proxy/credential/default username=x-access-token password=@github-pat.txt
bao write git-proxy/credential/gitlab-work username=oauth2 password=@gitlab-token.txt
bao write git-proxy/repo/widgetshop url=https://github.com/acmecorp/widgetshop.git
bao write git-proxy/repo/infra url=https://gitlab.example.com/ops/platform/infra.git credential=gitlab-work
```

Writes change only the fields they name, so rotating a token is `bao write git-proxy/credential/default password=@new.txt`; a new repository uses the `default` credential unless it names another. Hosts differ only in the credential's username: GitHub ignores it (`x-access-token` by convention), GitLab takes `oauth2` or any name with a personal access token, Bitbucket `x-token-auth`. The plugin refuses non-HTTPS upstream URLs, URLs with user info, a query or a fragment, credentials that don't exist, deleting a credential still in use, and names outside `[a-z0-9][a-z0-9-_.]*` or ending in `.git`. It also refuses `sys`, `auth`, `identity` and `cubbyhole`, which openbao-authorizer's Hard Denylist never grants. Credentials are seal-wrapped where OpenBao supports it.

Reading `credential/<name>` returns the password, so only the Proxy Token's policy may have `read` there; give operators `create`, `update`, `delete` and `list`. Access Paths handle only reads, so `update` on them lets a Requester write nothing.

## OpenBao setup

[`config/git-proxy-policy.hcl`](config/git-proxy-policy.hcl) is the Proxy Token's policy: `list` on `git-proxy/repos`, `read` on `git-proxy/repo/*` and `git-proxy/credential/*`, `update` on `auth/token/renew-self`.

```sh
bao policy write openbao-git-proxy config/git-proxy-policy.hcl
bao token create -orphan -policy=openbao-git-proxy -no-default-policy \
  -period=720h -field=token >/run/secrets/openbao-git-proxy-token
```

The proxy renews its token at start and every 72 hours, so a periodic token never expires while it runs, and reloads the repositories and credentials every `refresh_interval` (default 1m): a new repository or a rotated token applies within a minute without a restart. A failed reload keeps the last configuration; a broken entry is logged and skipped. The proxy refuses to start until it has read the mount once.

Requesters need `default` on their tokens (which allows `capabilities-self`), as Named and Session Tokens have. Make the Access Paths grantable in openbao-authorizer, and give the authorizer's policy `list` on `git-proxy/repos` so agents see the repositories:

```hcl
grantable "git-proxy" {
  path         = "git-proxy/access/*"
  capabilities = ["read", "update"]
  max_ttl      = "8h"
  description  = "git through openbao-git-proxy: read fetches one repository, update pushes to it."
  discover     = "git-proxy/repos"
}
```

## Running the proxy

```sh
./bin/openbao-git-proxy -config openbao-git-proxy.hcl
```

```hcl
server {
  listen_address = "127.0.0.1:8080" # plain HTTP; terminate TLS at a reverse proxy
}

openbao {
  address          = "https://openbao.example.com"
  namespace        = ""     # optional
  ca_file          = ""     # optional
  token_file       = "/run/secrets/openbao-git-proxy-token"
  mount            = "git-proxy" # default
  refresh_interval = "1m"        # default
}
```

See [`config/openbao-git-proxy.example.hcl`](config/openbao-git-proxy.example.hcl). String attributes can reference environment variables explicitly, such as `address = "https://bao.${env.DOMAIN_NAME}"`; relative paths are resolved from the file's directory.

Terminate TLS at a reverse proxy such as Traefik. It must not buffer request or response bodies and must allow large uploads and long transfers; the proxy itself has no read or write timeout. `GET /healthz` answers `{"status":"ok"}`.

## Using it from git

Point each repository at the proxy, rewriting HTTPS and SSH remotes alike:

```ini
[url "https://git-proxy.example.com/widgetshop.git"]
    insteadOf = https://github.com/acmecorp/widgetshop.git
    insteadOf = git@github.com:acmecorp/widgetshop.git
```

git sends credentials only after the `401` challenge, and the password must be the Requester's OpenBao token. Request the Grant with `bao-cred request --reason TEXT --item git-proxy/access/widgetshop=read` (`read,update` to push), and hand git the token that `bao-cred run` would use:

```ini
[credential "https://git-proxy.example.com"]
    helper =
    helper = "!f() { test \"$1\" = get && printf 'username=x-bao-token\\npassword=%s\\n' \"$(bao-cred run -- printenv BAO_TOKEN)\"; }; f"
```

The empty `helper =` drops helpers configured earlier for this host: a `store` or `cache` helper would otherwise keep the token. A `bao-cred git-credential` mode that requests the Grant itself and answers with the Requester's token is planned in openbao-authorizer.

Not covered: SSH itself (rewrite SSH remotes as above), `git://`, Git LFS, and host APIs, so `gh` and `glab` work only for their plain git operations.

## Build and test

```sh
make test   # race tests, including real git clone/push through the proxy when git is installed
make lint   # golangci-lint v2.14.0
make build  # bin/openbao-git-proxy and bin/openbao-plugin-secrets-git-proxy
```

The [`Dockerfile`](Dockerfile) builds the proxy image, a static binary on Alpine running as UID/GID `65532`, published as `ghcr.io/xtruder/openbao-git-proxy`. Version tags also publish both binaries for Linux amd64 and arm64 with SHA-256 checksums, for `bao plugin register -sha256`.
