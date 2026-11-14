# OpenBao Git Proxy

Lets a Requester fetch from and push to a GitHub repository that no GitHub credential can be scoped to, by forwarding its git traffic with one upstream credential that only the proxy holds. OpenBao Grants decide which Requester may use which repository.

## Language

**Requester**:
An OpenBao identity, such as an Agent Session or a Named Identity from openbao-authorizer, that presents its own OpenBao token to the proxy.
_Avoid_: client, caller, user

**Grant**:
A permission an Approver gave a Requester through openbao-authorizer, with its own expiry. Here it is always a capability on a **Marker Path**.
_Avoid_: approval, access token

**Marker Path**:
The OpenBao path that stands for one repository, such as `git-proxy/acmecorp/widgetshop`. It holds no data; only the capabilities a Requester's token has on it count: `read` to fetch, `update` to push.
_Avoid_: repo secret, permission path

**Allowed Repository**:
A repository the operator configured the proxy to forward. A Grant on the Marker Path of any other repository reaches nothing.
_Avoid_: allowlist entry, whitelisted repo

**Upstream Credential**:
The one GitHub token, a classic PAT, that the proxy uses for every forwarded request. Requesters never see it.
_Avoid_: PAT (in prose about the role), GitHub token

**Proxy Token**:
The proxy's own OpenBao token. It reads the **Upstream Credential** and nothing decides access with it.
_Avoid_: service token

## Relationships

- A **Requester** may fetch an **Allowed Repository** when its token has `read` on that repository's **Marker Path**, and push when it has `update`.
- A **Grant** on a **Marker Path** comes from openbao-authorizer in any Grant scope; the proxy only asks OpenBao what the Requester's token may do.
- The **Upstream Credential** reaches every repository its GitHub account can, so the **Allowed Repositories** and the Marker Path Grants together are what scope it.

## Flagged ambiguities

- "Token" alone is ambiguous: a Requester's OpenBao token is the password git sends to the proxy; the **Upstream Credential** is what the proxy sends to GitHub; the **Proxy Token** is the proxy's own OpenBao login. Say which one.
