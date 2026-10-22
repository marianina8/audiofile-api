package oauth

import "testing"

func TestAllowedRedirect(t *testing.T) {
	const cbID = "abc123_-XYZ"
	accept := []struct{ uri, id string }{
		{"http://localhost:53122/callback", ""},
		{"http://127.0.0.1:1/callback", ""},
		{"http://localhost:65535/callback", ""},
		{"https://claude.ai/api/mcp/auth_callback", ""},
		{"https://chatgpt.com/connector_platform_oauth_redirect", ""},
		{"https://chatgpt.com/connector/oauth/" + cbID, cbID},
		{"http://localhost:6274/oauth/callback", ""},
		{"http://localhost:6274/oauth/callback/debug", ""},
		{"http://127.0.0.1:6274/oauth/callback", ""},
		{"http://127.0.0.1:6274/oauth/callback/debug", ""},
	}
	for _, c := range accept {
		if !AllowedRedirect(c.uri, c.id) {
			t.Errorf("AllowedRedirect(%q, %q) = false, want true", c.uri, c.id)
		}
	}

	reject := []struct{ uri, id string }{
		{"http://localhost:53122/evil", ""},
		{"http://evil.com/callback", ""},
		{"https://claude.ai.evil.com/api/mcp/auth_callback", ""},
		{"http://localhost:0/callback", ""},
		{"http://localhost:70000/callback", ""},
		{"https://chatgpt.com/connector/oauth/" + cbID, ""},
		{"https://chatgpt.com/connector/oauth/other", cbID},
		{"https://chatgpt.com/connector/oauth/", ""},
		// Variations on allowlisted entries.
		{"http://claude.ai/api/mcp/auth_callback", ""},
		{"https://CLAUDE.ai/api/mcp/auth_callback", ""},
		{"https://claude.ai:443/api/mcp/auth_callback", ""},
		{"https://claude.ai/api/mcp/auth_callback/", ""},
		{"https://claude.ai/api/mcp/auth%5Fcallback", ""},
		{"https://claude.ai/api/mcp/auth_callback?x=1", ""},
		{"https://claude.ai/api/mcp/auth_callback?", ""},
		{"https://claude.ai/api/mcp/auth_callback#frag", ""},
		{"https://claude.ai/api/mcp/auth_callback#", ""},
		{"https://user@claude.ai/api/mcp/auth_callback", ""},
		{"https://claude.ai@evil.com/api/mcp/auth_callback", ""},
		{" https://claude.ai/api/mcp/auth_callback", ""},
		// Loopback variations.
		{"http://localhost/callback", ""},
		{"http://localhost:/callback", ""},
		{"http://localhost:053122/callback", ""},
		{"http://localhost:+80/callback", ""},
		{"http://LOCALHOST:53122/callback", ""},
		{"HTTP://localhost:53122/callback", ""},
		{"https://localhost:53122/callback", ""},
		{"http://[::1]:53122/callback", ""},
		{"http://127.0.0.2:53122/callback", ""},
		{"http://localhost.evil.com:53122/callback", ""},
		{"http://localhost:53122/callback/", ""},
		{"http://localhost:53122/callback?x=1", ""},
		{"http://localhost:53122/callback#x", ""},
		{"http://u:p@localhost:53122/callback", ""},
		{"http://localhost:53122//callback", ""},
		// Inspector entries are fixed to port 6274 on localhost/127.0.0.1.
		{"http://localhost:6275/oauth/callback", ""},
		{"http://127.0.0.1:6275/oauth/callback", ""},
		{"http://127.0.0.1:6274/oauth/callbackx", ""},
		{"http://127.0.0.1:6274/oauth/callback?x=1", ""},
		{"http://localhost:53122/oauth/callback/debug", ""},
		{"", ""},
		{"not a url", ""},
	}
	for _, c := range reject {
		if AllowedRedirect(c.uri, c.id) {
			t.Errorf("AllowedRedirect(%q, %q) = true, want false", c.uri, c.id)
		}
	}
}

func TestAllowedRedirect_BadCallbackIDNeverMatches(t *testing.T) {
	for _, id := range []string{"a/b", "a?b", "a#b", "../x", "a b"} {
		if AllowedRedirect("https://chatgpt.com/connector/oauth/"+id, id) {
			t.Errorf("callback id %q matched", id)
		}
	}
}

func TestRedirectRegistered(t *testing.T) {
	cases := []struct {
		registered []string
		requested  string
		want       bool
	}{
		{[]string{"https://claude.ai/api/mcp/auth_callback"}, "https://claude.ai/api/mcp/auth_callback", true},
		{[]string{"http://localhost:53122/callback"}, "http://localhost:53122/callback", true},
		// Exact match only for entries that carry a port.
		{[]string{"http://localhost:53122/callback"}, "http://localhost:53123/callback", false},
		// Portless loopback entries match the same host and path on any port (RFC 8252 §7.3).
		{[]string{"http://localhost/callback"}, "http://localhost:53122/callback", true},
		{[]string{"http://127.0.0.1/callback"}, "http://127.0.0.1:1/callback", true},
		{[]string{"http://localhost/callback", "http://127.0.0.1/callback"}, "http://127.0.0.1:65535/callback", true},
		{[]string{"http://localhost/callback"}, "http://127.0.0.1:53122/callback", false},
		{[]string{"http://127.0.0.1/callback"}, "http://localhost:53122/callback", false},
		{[]string{"http://localhost/callback"}, "http://localhost/callback", true},
		{[]string{"http://localhost/callback"}, "http://localhost:53122/other", false},
		{[]string{"http://localhost/callback"}, "http://localhost:0/callback", false},
		{[]string{"http://localhost/callback"}, "http://localhost:053122/callback", false},
		{[]string{"http://localhost/callback"}, "https://localhost:53122/callback", false},
		{[]string{"http://localhost/callback"}, "http://localhost:53122/callback?x=1", false},
		{[]string{"http://localhost/callback"}, "http://localhost.evil.com:53122/callback", false},
		{[]string{"http://localhost/oauth/callback"}, "http://localhost:6274/oauth/callback", false},
		{nil, "http://localhost:53122/callback", false},
		{[]string{"https://claude.ai/api/mcp/auth_callback"}, "", false},
	}
	for _, c := range cases {
		if got := redirectRegistered(c.registered, c.requested); got != c.want {
			t.Errorf("redirectRegistered(%q, %q) = %v, want %v", c.registered, c.requested, got, c.want)
		}
	}
}

func TestAllowedRegistrationRedirect(t *testing.T) {
	for _, uri := range []string{"http://localhost/callback", "http://127.0.0.1/callback", "http://localhost:53122/callback", "https://claude.ai/api/mcp/auth_callback"} {
		if !allowedRegistrationRedirect(uri, "") {
			t.Errorf("allowedRegistrationRedirect(%q) = false, want true", uri)
		}
	}
	for _, uri := range []string{"http://localhost/evil", "http://localhost:/callback", "http://[::1]/callback", "https://localhost/callback", "http://localhost/callback/", "http://evil.com/callback"} {
		if allowedRegistrationRedirect(uri, "") {
			t.Errorf("allowedRegistrationRedirect(%q) = true, want false", uri)
		}
	}
}
