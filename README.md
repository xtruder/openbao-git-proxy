# OpenBao Git Proxy

Forwards git over HTTPS for a few configured GitHub repositories with one **Upstream Credential**, a classic PAT that only the proxy holds. A **Requester**, such as an AI agent's Agent Session, authenticates with its own OpenBao token, and the proxy forwards the request only when that token holds a **Grant** on the repository's **Marker Path**: `read` on `git-proxy/<owner>/<repo>` to fetch, `update` to push. Grants come from [openbao-authorizer](https://github.com/xtruder/openbao-authorizer), so an Approver decides, per repository, who may use it and for how long. See [`CONTEXT.md`](CONTEXT.md) for the vocabulary and [ADR-0001](docs/adr/0001-proxy-outside-openbao.md) for the design.

It exists for repositories no GitHub credential can be scoped to, such as an organization's repository where you are an outside collaborator: fine-grained PATs don't support those, a classic PAT reaches every repository you can, and a GitHub App needs an installation by the organization.

## Security model

- **Requester token:** git sends it as the HTTP Basic password; the username is ignored. The proxy uses it only for `sys/capabilities-self` on the Marker Path, on every request, and never stores or logs it. Requests without one get `401` with a `Basic` challenge, so git asks its credential helper; OpenBao refusing the token also gets `401`.
- **Grant check:** fetching (`git-upload-pack`, including its `info/refs`) needs `read`, pushing (`git-receive-pack`) needs `update`; `root` allows both. Otherwise the proxy answers `403` with a message naming the missing Grant, which git shows. Revoking or expiring the Grant stops the next request.
- **Allowed traffic:** only `GET /<owner>/<repo>[.git]/info/refs?service=git-upload-pack|git-receive-pack` and `POST /<owner>/<repo>[.git]/git-upload-pack|git-receive-pack` for **Allowed Repositories**. Anything else, including dumb HTTP, other query parameters, escaped characters in the path, the REST API and GraphQL, is `404` before any credential is asked for. Names are lowercased and rebuilt into the upstream path, so nothing reaches GitHub that the allow-list didn't see.
- **Upstream Credential:** the proxy reads it with its own **Proxy Token** and sends it as `Authorization` upstream. Only `Accept`, `Accept-Encoding`, `Content-Encoding`, `Content-Type`, `Git-Protocol` and `User-Agent` are forwarded; the client's `Authorization`, cookies and everything else are dropped, as are GitHub's `Set-Cookie` headers. GitHub refusing the PAT becomes `502`, not a challenge, so git keeps the Requester's token. A redirect from GitHub, such as for a renamed repository, becomes `502` too: git following it would leave the proxy and its checks behind.
- **Streaming:** request and response bodies stream in both directions and responses flush immediately, so packs are never buffered and git shows the remote's progress.
- **Logs:** one JSON line per request with method, repository, service, status and duration; never a token or `Authorization` value.
- **Threat model:** the proxy is not a boundary against code on its own host, which can read the Upstream Credential or the Proxy Token. Requesters run elsewhere.

## Architecture

```text
agent ── git, Basic auth (password = Session Token) ──> openbao-git-proxy ── PAT ──> github.com
                                                          │
                     Session Token: sys/capabilities-self │ Proxy Token: read the PAT, renew-self
                     on git-proxy/<owner>/<repo>          ▼
                                                       OpenBao <── Grants ── openbao-authorizer
```

## Build and test

```sh
make test   # race tests, including real git clone/push through the proxy when git is installed
make lint   # golangci-lint v2.14.0
make build  # bin/openbao-git-proxy
```

The [`Dockerfile`](Dockerfile) builds a static binary on Alpine running as UID/GID `65532`.

## OpenBao setup

The PAT lives in KV v2 (any path; the example uses a `kv` mount):

```sh
bao secrets enable -path=kv kv-v2
bao kv put -mount=kv git-proxy/github token=ghp_...
bao policy write openbao-git-proxy config/git-proxy-policy.hcl
bao token create -orphan -policy=openbao-git-proxy -no-default-policy \
  -period=720h -field=token >/run/secrets/openbao-git-proxy-token
```

[`config/git-proxy-policy.hcl`](config/git-proxy-policy.hcl) allows reading the PAT and `auth/token/renew-self`. The proxy renews its token at start and every 72 hours, so a periodic token never expires while it runs, and re-reads the PAT every `refresh_interval`, so rotating it needs no restart; a failed re-read keeps the last one. The proxy refuses to start without the PAT.

Requesters need `default` on their tokens (which allows `capabilities-self`), as Named and Session Tokens have. Marker Paths need no mount: capabilities come from policy alone. Make them grantable in openbao-authorizer:

```hcl
grantable "git-proxy" {
  path         = "git-proxy/+/+"
  capabilities = ["read", "update"]
  max_ttl      = "8h"
  description  = "git through openbao-git-proxy: read fetches <owner>/<repo>, update pushes."
}
```

The authorizer's Hard Denylist refuses paths with a `sys`, `auth`, `identity` or `cubbyhole` segment, so the proxy refuses to start when an Allowed Repository's owner or name is one of those.

## Configuration

```sh
./bin/openbao-git-proxy -config openbao-git-proxy.hcl
```

The HCL file is documented in [`config/openbao-git-proxy.example.hcl`](config/openbao-git-proxy.example.hcl). String attributes can reference environment variables explicitly, such as `address = "https://bao.${env.DOMAIN_NAME}"`; relative paths are resolved from the file's directory.

| Block | Attribute | Default | Meaning |
|---|---|---|---|
| `server` | `listen_address` | required | Plain HTTP listener |
| `openbao` | `address`, `namespace`, `ca_file` | | OpenBao client |
| `openbao` | `token_file` | required | The Proxy Token |
| `upstream` | `url` | `https://github.com` | Scheme and host only |
| `upstream` | `credential_path`, `credential_field` | required, `token` | Where the PAT is; KV v2 data is unwrapped |
| `upstream` | `refresh_interval` | `5m` | How often the PAT is re-read |
| `access` | `marker_prefix` | `git-proxy` | Marker Paths are `<prefix>/<owner>/<repo>`, lowercased |
| `access` | `repositories` | required | Allowed Repositories, `owner/repo` |

Terminate TLS at a reverse proxy such as Traefik. It must not buffer request or response bodies and must allow large uploads and long transfers; the proxy itself has no read or write timeout. `GET /healthz` answers `{"status":"ok"}`.

## Using it from git

Point the repository at the proxy and have git send your OpenBao token:

```ini
[url "https://gitproxy.example.com/acmecorp/widgetshop"]
    insteadOf = https://github.com/acmecorp/widgetshop
```

git sends credentials only after the `401` challenge. `bao-cred git-credential` currently answers with a secret field read from OpenBao, not with the Requester's own token, so it doesn't fit yet. The proposed change in openbao-authorizer is a `--requester-token` mode: `get` checks `capabilities-self` on the expanded path (`git-proxy/{owner}/{repo}`), files a Grant Request for missing `--capabilities` (default `read`; `read,update` where you push, since git doesn't tell the helper which it does and doesn't ask again after a refusal), and answers with the Requester's token and its expiry:

```ini
[credential "https://gitproxy.example.com"]
    useHttpPath = true
    helper = "!bao-cred git-credential --requester-token --capabilities read,update git-proxy/{owner}/{repo}"
```

Until then, request the Grant with `bao-cred request --reason TEXT --item git-proxy/acmecorp/widgetshop=read,update` and hand git the token that `bao-cred run` would use:

```ini
[credential "https://gitproxy.example.com"]
    helper = "!f() { test \"$1\" = get && printf 'username=x-bao-token\\npassword=%s\\n' \"$(bao-cred run -- printenv BAO_TOKEN)\"; }; f"
```
