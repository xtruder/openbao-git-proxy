# Policy of the proxy's own token. Create the token without the default
# policy; capabilities-self checks use each Requester's token instead.
path "kv/data/git-proxy/github" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}
