# Policy of the proxy's own token. Create the token without the default
# policy; capabilities-self checks use each Requester's token instead.
path "git-proxy/repos" {
  capabilities = ["list"]
}

path "git-proxy/repo/*" {
  capabilities = ["read"]
}

# Returns upstream passwords: no other policy should allow read here.
path "git-proxy/credential/*" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}
