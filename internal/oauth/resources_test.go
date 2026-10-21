package oauth

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	calls atomic.Int32
	fail  atomic.Bool
	pages [][]string
}

func (f *fakeSSM) GetParametersByPath(_ context.Context, in *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return nil, errors.New("ssm down")
	}
	if aws.ToString(in.Path) != "/audiofile/mcp-resources/" {
		return nil, errors.New("unexpected path")
	}
	page := 0
	if in.NextToken != nil {
		page = 1
	}
	out := &ssm.GetParametersByPathOutput{}
	for _, v := range f.pages[page] {
		out.Parameters = append(out.Parameters, types.Parameter{Value: aws.String(v)})
	}
	if page+1 < len(f.pages) {
		out.NextToken = aws.String("next")
	}
	return out, nil
}

func TestSSMResources(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	api := &fakeSSM{pages: [][]string{{"https://a.example/mcp", " "}, {"https://b.example/mcp\n"}}}
	res := SSMResources(api, "/audiofile/mcp-resources/", clock.Now)

	got, err := res(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://a.example/mcp", "https://b.example/mcp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resources = %v, want %v", got, want)
	}
	if n := api.calls.Load(); n != 2 {
		t.Errorf("calls = %d, want 2 (two pages)", n)
	}

	clock.advance(59 * time.Second)
	if _, err := res(ctx); err != nil || api.calls.Load() != 2 {
		t.Errorf("within 60 s: err %v, calls %d", err, api.calls.Load())
	}

	clock.advance(2 * time.Second)
	api.fail.Store(true)
	if got, err := res(ctx); err != nil || len(got) != 2 {
		t.Errorf("stale list on SSM failure: %v, %v", got, err)
	}

	empty := SSMResources(api, "/audiofile/mcp-resources/", clock.Now)
	if _, err := empty(ctx); err == nil {
		t.Error("want error when SSM fails and nothing is cached")
	}
}
