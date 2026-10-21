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
		// Inspector entries are fixed to port 6274 on localhost.
		{"http://localhost:6275/oauth/callback", ""},
		{"http://127.0.0.1:6274/oauth/callback", ""},
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
