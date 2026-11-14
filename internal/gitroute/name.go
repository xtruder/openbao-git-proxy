package gitroute

import (
	"errors"
	"net/url"
	"slices"
	"strings"
)

// deniedNames are the segments of openbao-authorizer's Hard Denylist: an
// Access Path containing one could never be granted.
var deniedNames = []string{"sys", "auth", "identity", "cubbyhole"}

// ValidName reports whether name is a lowercase letter or digit followed by
// lowercase letters, digits, '-', '_' and '.': safe as one OpenBao path
// segment. Credentials are named this way.
func ValidName(name string) bool {
	if name == "" || (name[0] < 'a' || name[0] > 'z') && (name[0] < '0' || name[0] > '9') {
		return false
	}

	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-_.") == ""
}

// ValidRepositoryName reports whether name can name a repository: it is
// the one path segment of the proxy URL and of the repository's Access
// Path. Lowercase only, so one repository has one Access Path; no .git
// suffix, which the proxy URL may add; and nothing the authorizer would
// never grant.
func ValidRepositoryName(name string) bool {
	return ValidName(name) && !strings.HasSuffix(name, ".git") && !slices.Contains(deniedNames, name)
}

// ValidUpstreamURL checks a repository's upstream clone URL: https only,
// and nothing that changes where or how its credential is sent, so a stray
// entry can't leak the credential in clear text or to another host. The
// plugin checks it on write and the proxy again on load.
func ValidUpstreamURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return errors.New("url must be an https clone URL without user info, query or fragment")
	}

	return nil
}
