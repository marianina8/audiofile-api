package oauth

import (
	"net/http"
	"strings"
)

// metadata is the RFC 8414 authorization server metadata (spec §8 A).
type metadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	// Claude uses CIMD only when this is true and "none" is a supported
	// token endpoint auth method.
	ClientIDMetadataDocumentSupported bool `json:"client_id_metadata_document_supported"`
	// RFC 9207; ChatGPT requires it.
	AuthorizationResponseISSParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
	ScopesSupported                            []string `json:"scopes_supported"`
}

// issuer returns the configured issuer, or https://<Host> as a local-test
// fallback (see Config.Issuer).
func (s *server) issuer(r *http.Request) string {
	if s.cfg.Issuer != "" {
		return strings.TrimRight(s.cfg.Issuer, "/")
	}
	return "https://" + r.Host
}

func (s *server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	iss := s.issuer(r)
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, metadata{
		Issuer:                                     iss,
		AuthorizationEndpoint:                      iss + "/authorize",
		TokenEndpoint:                              iss + "/token",
		RegistrationEndpoint:                       iss + "/register",
		ResponseTypesSupported:                     []string{"code"},
		GrantTypesSupported:                        []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:              []string{"S256"},
		TokenEndpointAuthMethodsSupported:          []string{"none"},
		ClientIDMetadataDocumentSupported:          true,
		AuthorizationResponseISSParameterSupported: true,
		ScopesSupported:                            []string{"openid", "email"},
	})
}
