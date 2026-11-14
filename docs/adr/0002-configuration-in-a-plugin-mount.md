# Configuration in a git-proxy plugin mount; the proxy stays a separate service

The proxy's configuration, the **Upstream Credentials** and the **Allowed Repositories**, lives in a mount of a small OpenBao secrets plugin, `openbao-plugin-secrets-git-proxy`, the way `openbao-plugin-secrets-github` holds the GitHub App's. The same mount serves the **Access Paths** that Grants name. The proxy runs as its own service, reads the mount with a **Proxy Token** every `refresh_interval` (default one minute), and keeps only bootstrap settings in its HCL file: listen address, OpenBao connection, token file and mount path.

Repositories are named entries (`repo/<name>` with an upstream `url` and a `credential` name) and Requesters address them by name (`/<name>.git`), never by upstream path. The proxy therefore works with any HTTPS git host whatever its path layout (GitLab subgroups, Azure's `/_git/`), only one path segment of a request needs validating, and the authorizer's `discover` can list the repositories, since it fills one flat last segment. Credentials (`credential/<name>`: username and password) are separate from repositories because one token usually covers many repositories, so rotating it is one write, and repository entries then hold no secrets.

## Considered options

- **The HCL file plus KV for the credential.** Works, but configuration changes need a restart, a bad entry is found only when the proxy loads it, and the configuration is split across a file and a generic KV path.
- **The proxy inside the plugin, on a listener of its own.** One install step and no Proxy Token. Rejected because the plugin runs in OpenBao's container as OpenBao's user, next to the auto-unseal key and the data: a listener would expose code to unauthenticated network input there, and a code-execution bug in it would expose every secret, where in a separate container it exposes the Upstream Credentials. OpenBao also starts, stops and reloads plugin processes on its own schedule, which a long-running listener doesn't fit.
- **Credentials inline on each repository.** Simpler, but rotating a shared token means rewriting every repository, and repository entries would carry secrets.
- **Fine-grained permission sets enforced by the proxy.** Not needed: access is per repository, fetch or push.

## Consequences

- **Restart-free configuration, validated on write.** The plugin refuses non-HTTPS upstream URLs, missing credentials, deleting a credential still in use, and names the authorizer could never grant (`sys`, `auth`, `identity`, `cubbyhole`). The proxy re-validates what it loads and skips broken entries.
- **Reading a credential returns its password,** so only the Proxy Token's policy may have `read` on `credential/*`; operators get `create`, `update` and `delete`.
- **Holding `update` on an Access Path writes nothing:** the plugin handles only reads there.
- **A second binary to release** with its SHA-256, registered in the OpenBao image and reloaded on upgrades.
