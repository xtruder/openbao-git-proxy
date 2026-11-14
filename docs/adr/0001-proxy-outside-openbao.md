# A git proxy outside OpenBao, authorized by capabilities-self on a Marker Path

GitHub can't scope a credential to one repository of an organization where the user is only an outside collaborator: fine-grained PATs don't support such repositories, a classic PAT with `repo` reaches every repository the user can, and a GitHub App needs an installation the organization won't grant. So a separate HTTP service holds the classic PAT as the **Upstream Credential** and forwards git smart HTTP for **Allowed Repositories** only. To decide who may use it, the service calls `sys/capabilities-self` with the Requester's own OpenBao token on the repository's **Marker Path**: `read` allows fetching, `update` pushing. Requesters obtain those capabilities as ordinary Grants through openbao-authorizer, so approval, scopes, expiry and revocation work as for any other path, and the proxy has no authorization logic of its own.

## Considered options

- **An OpenBao secrets plugin that proxies git.** OpenBao parses POST bodies as JSON or form data, and gRPC plugins get neither the raw HTTP request nor a streaming response writer, so git's binary pack traffic can't pass through.
- **Handing Requesters the PAT through a Grant.** The PAT reaches every repository of the account, so a Grant on it would grant far more than one repository.
- **The proxy reading a secret with the Requester's token.** It would need a secret per repository that exists only to be readable. Capabilities don't depend on data existing, so a Marker Path that is never written needs nothing but policy.
- **Fetch and push on separate Marker Paths.** Two items per repository for Approvers; one path with `read` and `update` mirrors what the capabilities mean.

## Consequences

- **OpenBao is asked on every request.** A fetch costs two capabilities-self calls and a push two more. Revoking or expiring a Grant takes effect on the next request; OpenBao being down refuses git traffic (503).
- **git's credential helper must answer with the Requester's OpenBao token**, and must request push access before pushing: git doesn't tell the helper whether it fetches or pushes, and doesn't ask it again after a refusal.
- **Marker Paths are lowercased**, because GitHub names are case-insensitive and one repository must have one Marker Path.
- **The proxy is not a boundary against code on its own host**, which can read the Upstream Credential; Requesters run elsewhere.
