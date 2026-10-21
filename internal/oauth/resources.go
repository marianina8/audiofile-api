package oauth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	resourcesTTL     = 60 * time.Second
	resourcesTimeout = 3 * time.Second
)

// ParametersByPathAPI is the part of the SSM client SSMResources uses.
type ParametersByPathAPI interface {
	GetParametersByPath(ctx context.Context, in *ssm.GetParametersByPathInput, opts ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

// SSMResources returns a Config.Resources that reads every parameter value
// under prefix (each mcp-host stack writes its MCP URL there) and caches the
// list for 60 s. If a refresh fails, the last good list is served; with no
// list yet, the error is returned. now nil means time.Now.
func SSMResources(api ParametersByPathAPI, prefix string, now func() time.Time) func(ctx context.Context) ([]string, error) {
	if now == nil {
		now = time.Now
	}
	var (
		mu      sync.Mutex
		cached  []string
		fetched time.Time
		loaded  bool
	)
	return func(ctx context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		if loaded && now().Sub(fetched) < resourcesTTL {
			return cached, nil
		}
		fctx, cancel := context.WithTimeout(ctx, resourcesTimeout)
		defer cancel()
		list, err := readResources(fctx, api, prefix)
		if err != nil {
			if loaded {
				slog.Warn("oauth: resources", "outcome", "refresh_failed_serving_cached")
				return cached, nil
			}
			return nil, err
		}
		cached, fetched, loaded = list, now(), true
		return cached, nil
	}
}

func readResources(ctx context.Context, api ParametersByPathAPI, prefix string) ([]string, error) {
	if prefix == "" {
		return nil, errors.New("oauth: empty resources SSM prefix")
	}
	var out []string
	var next *string
	for {
		page, err := api.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:      aws.String(prefix),
			Recursive: aws.Bool(true),
			NextToken: next,
		})
		if err != nil {
			return nil, err
		}
		for _, p := range page.Parameters {
			if v := strings.TrimSpace(aws.ToString(p.Value)); v != "" {
				out = append(out, v)
			}
		}
		if page.NextToken == nil || *page.NextToken == "" {
			return out, nil
		}
		next = page.NextToken
	}
}

// resourceRegistered reports whether resource is a registered MCP server URL.
func (s *server) resourceRegistered(ctx context.Context, resource string) (bool, error) {
	if s.cfg.Resources == nil {
		return false, nil
	}
	list, err := s.cfg.Resources(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range list {
		if r == resource {
			return true, nil
		}
	}
	return false, nil
}
