package oauth

import (
	"net/url"
	"regexp"
	"strconv"
)

// Fixed redirect URIs, compared as exact strings.
var fixedRedirects = map[string]bool{
	"https://claude.ai/api/mcp/auth_callback":               true,
	"https://chatgpt.com/connector_platform_oauth_redirect": true,
	"http://localhost:6274/oauth/callback":                  true, // MCP Inspector (lab)
	"http://localhost:6274/oauth/callback/debug":            true, // MCP Inspector (lab)
}

const chatgptConnectorPrefix = "https://chatgpt.com/connector/oauth/"

var callbackIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// AllowedRedirect reports whether uri is on the bridge's redirect allowlist.
// Allowlisted entries match exactly. The only variable part is the port of
// the Claude Code loopback callbacks, http://localhost:{port}/callback and
// http://127.0.0.1:{port}/callback with port 1-65535. The per-connector
// ChatGPT callback is allowed only when chatgptCallbackID is set.
//
// Matching is by exact string against a canonical form, so case changes,
// percent-encoding, default ports, userinfo, queries, fragments and leading
// zeros all fail.
func AllowedRedirect(uri, chatgptCallbackID string) bool {
	if fixedRedirects[uri] {
		return true
	}
	if chatgptCallbackID != "" && callbackIDPattern.MatchString(chatgptCallbackID) &&
		uri == chatgptConnectorPrefix+chatgptCallbackID {
		return true
	}
	return allowedLoopback(uri)
}

func allowedLoopback(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	// Rebuild the only acceptable spelling and compare exactly.
	return uri == "http://"+host+":"+strconv.Itoa(port)+"/callback"
}
