# A git proxy outside OpenBao, authorized by capabilities-self on an Access Path

_Amended by [ADR-0002](0002-configuration-in-a-plugin-mount.md): the proxy became generic over git hosts, and the policy-only marker path `git-proxy/<owner>/<repo>` became the Access Path `git-proxy/access/<name>`, served by the plugin mount that now holds the configuration._

Some repositories can't get a credential scoped to them: on GitHub, for example, fine-grained PATs don't support repositories where the user is only an outside collaborator, a classic PAT with `repo` reaches every repository the user can, and a GitHub App needs an installation the repository's owner won't grant. So a separate HTTP service holds the broad credential as an **Upstream Credential** and forwards git smart HTTP for **Allowed Repositories** only. To decide who may use it, the service calls `sys/capabilities-self` with the Requester's own OpenBao token on the repository's **Access Path**: `read` allows fetching, `update` pushing. Requesters obtain those capabilities as ordinary Grants through openbao-authorizer, so approval, scopes, expiry and revocation work as for any other path, and the proxy has no authorization logic of its own.

## Considered options

- **An OpenBao secrets plugin that proxies git through OpenBao's API.** OpenBao parses POST bodies as JSON or form data, and gRPC plugins get neither the raw HTTP request nor a streaming response writer, so git's binary pack traffic can't pass through. (A plugin opening a listener of its own is rejected in ADR-0002.)
- **Handing Requesters the credential through a Grant.** It reaches every repository of its account, so a Grant on it would grant far more than one repository.
- **The proxy reading a secret with the Requester's token.** A push needs `update`, which can't be tested by reading; `capabilities-self` answers for both in one call.
- **Fetch and push on separate paths.** Two Grants per repository for Approvers; one path with `read` and `update` mirrors what the capabilities mean.

## Consequences

- **OpenBao is asked on every request.** A fetch costs two capabilities-self calls and a push two more. Revoking or expiring a Grant takes effect on the next request; OpenBao being down refuses git traffic (503).
- **git's credential helper must answer with the Requester's OpenBao token**, and must request push access before pushing: git doesn't tell the helper whether it fetches or pushes, and doesn't ask it again after a refusal.
- **`update` also allows fetching**, since every push reads the remote's refs; a Requester that may push needs one Grant.
- **The proxy is not a boundary against code on its own host**, which can read the Upstream Credentials and the Proxy Token; Requesters run elsewhere.
