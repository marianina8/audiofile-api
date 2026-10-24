package curated

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// refreshWindow is how long before expiry a cached token is replaced.
const refreshWindow = 60 * time.Second

// TokenSource hands out the bearer token the server uses to call the API.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	// Invalidate drops the cached token so the next Token call fetches a new one.
	Invalidate()
}

type machineToken struct {
	tokenURL, clientID string
	secret             func(ctx context.Context) (string, error)
	hc                 *http.Client
	now                func() time.Time

	mu      sync.Mutex
	tok     string
	expires time.Time
}

// NewMachineToken returns a TokenSource that runs the OAuth client-credentials
// flow for the machine client and caches the token until shortly before it expires.
func NewMachineToken(tokenURL, clientID string, secret func(ctx context.Context) (string, error), hc *http.Client) TokenSource {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &machineToken{tokenURL: tokenURL, clientID: clientID, secret: secret, hc: hc, now: time.Now}
}

func (m *machineToken) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tok != "" && m.now().Add(refreshWindow).Before(m.expires) {
		return m.tok, nil
	}
	secret, err := m.secret(ctx)
	if err != nil {
		// AWS errors can name the account and role; report only the cause.
		return "", errors.New("could not read the machine client secret")
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("token request: bad token URL")
	}
	req.SetBasicAuth(m.clientID, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.hc.Do(req)
	if err != nil {
		// Do's error embeds the URL and the dial error the host; report only the cause.
		return "", fmt.Errorf("token request failed: %w", transportError(err, "the token endpoint"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request: token endpoint returned %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.AccessToken == "" {
		return "", errors.New("token request: unreadable token response")
	}
	m.tok = body.AccessToken
	m.expires = m.now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return m.tok, nil
}

func (m *machineToken) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tok = ""
}

// transportError maps a failed request to a plain cause. The original error
// can carry the URL, host and port, so none of it is kept.
func transportError(err error, target string) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return errors.New("timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("cancelled")
	}
	return errors.New("could not reach " + target)
}

// SSMSecret reads a SecureString parameter, decrypted, and caches it after the
// first successful read.
func SSMSecret(name string) func(ctx context.Context) (string, error) {
	var (
		mu    sync.Mutex
		value string
	)
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if value != "" {
			return value, nil
		}
		// AWS errors can name the account and role, so they are not passed on.
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return "", errors.New("could not load AWS config")
		}
		out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
		if err != nil {
			return "", errors.New("could not read the machine client secret")
		}
		value = aws.ToString(out.Parameter.Value)
		return value, nil
	}
}
