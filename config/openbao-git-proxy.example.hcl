server {
  # Plain HTTP: terminate TLS at a reverse proxy that doesn't buffer bodies.
  listen_address = "127.0.0.1:8080"
}

openbao {
  address   = "https://openbao.example.com"
  namespace = "" # optional
  ca_file   = "" # optional
  # The proxy's own periodic token (policy: config/git-proxy-policy.hcl). It
  # reads the mount's repositories and credentials and renews itself; it
  # never decides access.
  token_file = "/run/secrets/openbao-git-proxy-token"
  # The openbao-plugin-secrets-git-proxy mount; Access Paths are
  # <mount>/access/<name>.
  mount = "git-proxy" # default
  # How often repositories and credentials are reloaded from the mount.
  refresh_interval = "1m" # default
}
