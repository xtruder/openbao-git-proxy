# OpenBao Git Proxy

Lets a Requester fetch from and push to git repositories that no credential can be scoped to, by forwarding its git traffic with upstream credentials that only the proxy holds. OpenBao Grants decide which Requester may use which repository.

## Language

**Requester**:
An OpenBao identity, such as an Agent Session or a Named Identity from openbao-authorizer, that presents its own OpenBao token to the proxy.
_Avoid_: client, caller, user

**Grant**:
A permission an Approver gave a Requester through openbao-authorizer, with its own expiry. Here it is always a capability on an **Access Path**.
_Avoid_: approval, access token

**Allowed Repository**:
A repository the operator configured in the git-proxy mount, under a **Repository Name**, with its upstream clone URL and the **Upstream Credential** to use there. The proxy forwards nothing else.
_Avoid_: allowlist entry, whitelisted repo

**Repository Name**:
The operator-chosen name of an **Allowed Repository**, such as `widgetshop`. Requesters address the repository by it, never by its upstream path, and it names the repository's **Access Path**.
_Avoid_: slug, repo path

**Access Path**:
The OpenBao path that stands for one **Allowed Repository**, such as `git-proxy/access/widgetshop`. It holds nothing secret; the capabilities a Requester's token has on it count: `read` to fetch, `update` to push.
_Avoid_: marker path, permission path

**Upstream Credential**:
A username and token that the proxy sends to a git host for the **Allowed Repositories** that name it. One usually covers many repositories, such as a classic GitHub PAT. Requesters never see it.
_Avoid_: PAT (in prose about the role), GitHub token

**Proxy Token**:
The proxy's own OpenBao token. It reads the configuration and **Upstream Credentials**; nothing decides access with it.
_Avoid_: service token

## Relationships

- A **Requester** may fetch an **Allowed Repository** when its token has `read` or `update` on the repository's **Access Path**, and push when it has `update`.
- A **Grant** on an **Access Path** comes from openbao-authorizer in any Grant scope; the proxy only asks OpenBao what the Requester's token may do.
- Each **Allowed Repository** names one **Upstream Credential**; an Upstream Credential may serve many.
- An **Upstream Credential** reaches whatever its account can upstream, so the **Allowed Repositories** and the Grants on their **Access Paths** together are what scope it.

## Flagged ambiguities

- "Token" alone is ambiguous: a Requester's OpenBao token is the password git sends to the proxy; an **Upstream Credential** is what the proxy sends to the git host; the **Proxy Token** is the proxy's own OpenBao login. Say which one.
- "Marker path" was the earlier name of the **Access Path**, when it was a policy-only path outside any mount.
