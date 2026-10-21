package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"regexp"
)

var (
	// RFC 7636 §4.1: 43 to 128 unreserved characters.
	verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
	// An S256 challenge is a base64url SHA-256: exactly 43 characters.
	challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// s256 returns the RFC 7636 S256 code challenge for verifier.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// pkceMatches reports whether verifier is well formed and hashes to challenge.
func pkceMatches(verifier, challenge string) bool {
	if !verifierPattern.MatchString(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s256(verifier)), []byte(challenge)) == 1
}

// randomToken returns 32 random bytes as 43 base64url characters. It is used
// for bridge codes, pending-request ids and the bridge's own PKCE verifier.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeKey is the state key for a bridge authorization code: the code itself
// is never stored.
func codeKey(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
