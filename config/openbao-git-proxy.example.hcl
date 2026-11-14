server {
  # Plain HTTP: terminate TLS at a reverse proxy that doesn't buffer bodies.
  listen_address = "127.0.0.1:8080"
}

openbao {
  address    = "https://openbao.example.com"
  namespace  = ""
  ca_file    = "" # optional
  # The proxy's own periodic token (policy: config/git-proxy-policy.hcl). It
  # reads the upstream credential and renews itself; it never decides access.
  token_file = "/run/secrets/openbao-git-proxy-token"
}

upstream {
  url              = "https://github.com" # default; scheme and host only
  # The classic PAT, read with the proxy's token; KV v2 data is unwrapped.
  credential_path  = "kv/data/git-proxy/github"
  credential_field = "token" # default
  refresh_interval = "5m"    # default; picks up a rotated PAT without a restart
}

access {
  # Fetching <owner>/<repo> needs read on <marker_prefix>/<owner>/<repo>,
  # pushing needs update. Requesters get them as Grants.
  marker_prefix = "git-proxy" # default
  # Only these repositories are forwarded, whatever a Requester is granted.
  repositories = ["acmecorp/widgetshop"]
}
