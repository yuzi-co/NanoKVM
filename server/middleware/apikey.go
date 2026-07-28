package middleware

import (
	"net/http"
	"strings"

	"NanoKVM-Server/service/apikey"
)

const (
	apiKeyHeader = "X-API-Key"
	bearerPrefix = "Bearer "
)

// allowByAPIKey authenticates a caller that holds a key instead of a login
// session. Scripts have no browser to keep a cookie in, and the alternative
// people reached for was switching authentication off entirely.
//
// The origin rule is applied to these requests exactly as it is to session
// ones. A key travels in a header a cross-site form cannot set, so it is not
// reachable by the attack the rule exists for, but nothing is gained by
// carving out an exception and a second path through the check is a second
// place for it to go wrong.
func allowByAPIKey(r *http.Request) bool {
	secret := presentedAPIKey(r)
	if secret == "" {
		return false
	}

	return apikey.Verify(secret)
}

// presentedAPIKey pulls a key out of either header clients expect to use.
func presentedAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get(apiKeyHeader)); key != "" {
		return key
	}

	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, bearerPrefix) {
		return ""
	}

	return strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix))
}
