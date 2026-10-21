// Package mcphost holds the mcp-host SAM template; this test checks the
// auth wiring that sam validate cannot.
package mcphost

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const mcpURL = "https://${Api}.execute-api.${AWS::Region}.amazonaws.com/v1/mcp"

// get returns the value node for key in mapping node m, or nil.
func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func path(m *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		m = get(m, k)
	}
	return m
}

func resources(t *testing.T) *yaml.Node {
	t.Helper()
	b, err := os.ReadFile("template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	r := get(doc.Content[0], "Resources")
	if r == nil {
		t.Fatal("no Resources")
	}
	return r
}

// ofType returns the logical IDs and bodies of resources of the given type.
func ofType(r *yaml.Node, typ string) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	for i := 0; i+1 < len(r.Content); i += 2 {
		if v := get(r.Content[i+1], "Type"); v != nil && v.Value == typ {
			out[r.Content[i].Value] = r.Content[i+1]
		}
	}
	return out
}

func subValue(t *testing.T, n *yaml.Node, what string) string {
	t.Helper()
	if n == nil || n.Tag != "!Sub" {
		t.Fatalf("%s: want a !Sub string, got %+v", what, n)
	}
	return n.Value
}

func TestOutputsAndResourceServerUseMcpURL(t *testing.T) {
	b, err := os.ReadFile("template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if got := subValue(t, path(doc.Content[0], "Outputs", "McpUrl", "Value"), "McpUrl"); got != mcpURL {
		t.Fatalf("McpUrl = %q, want %q", got, mcpURL)
	}
	r := resources(t)
	rs := ofType(r, "AWS::Cognito::UserPoolResourceServer")
	if len(rs) != 1 {
		t.Fatalf("want one resource server, got %d", len(rs))
	}
	for _, n := range rs {
		if got := subValue(t, path(n, "Properties", "Identifier"), "Identifier"); got != mcpURL {
			t.Fatalf("Identifier = %q, want %q", got, mcpURL)
		}
	}
	ssm := ofType(r, "AWS::SSM::Parameter")
	if len(ssm) != 1 {
		t.Fatalf("want one SSM parameter, got %d", len(ssm))
	}
	for _, n := range ssm {
		if got := subValue(t, path(n, "Properties", "Value"), "SSM Value"); got != mcpURL {
			t.Fatalf("SSM Value = %q, want %q", got, mcpURL)
		}
	}
}

func TestUnauthorizedResponsePointsAtResourceMetadata(t *testing.T) {
	for _, n := range ofType(resources(t), "AWS::ApiGateway::GatewayResponse") {
		if path(n, "Properties", "ResponseType").Value != "UNAUTHORIZED" {
			continue
		}
		h := subValue(t, path(n, "Properties", "ResponseParameters", "gatewayresponse.header.WWW-Authenticate"), "WWW-Authenticate")
		want := `'Bearer resource_metadata="https://${Api}.execute-api.${AWS::Region}.amazonaws.com/v1/.well-known/oauth-protected-resource"'`
		if h != want {
			t.Fatalf("WWW-Authenticate = %s, want %s", h, want)
		}
		if path(n, "Properties", "StatusCode").Value != "401" {
			t.Fatal("UNAUTHORIZED must be 401")
		}
		return
	}
	t.Fatal("no UNAUTHORIZED gateway response")
}

// methodsByResource maps each method's ResourceId !Ref target to its body.
func methodsByResource(t *testing.T, r *yaml.Node) map[string]*yaml.Node {
	t.Helper()
	out := map[string]*yaml.Node{}
	for id, n := range ofType(r, "AWS::ApiGateway::Method") {
		ref := path(n, "Properties", "ResourceId")
		if ref == nil || ref.Tag != "!Ref" {
			t.Fatalf("%s: ResourceId must be a !Ref", id)
		}
		out[ref.Value] = n
	}
	return out
}

func TestRoutesAndAuth(t *testing.T) {
	r := resources(t)
	authz := ofType(r, "AWS::ApiGateway::Authorizer")
	if len(authz) != 1 {
		t.Fatalf("want one authorizer, got %d", len(authz))
	}
	var authzID string
	for id, n := range authz {
		authzID = id
		p := get(n, "Properties")
		if get(p, "Type").Value != "TOKEN" ||
			get(p, "IdentitySource").Value != "method.request.header.Authorization" ||
			get(p, "AuthorizerResultTtlInSeconds").Value != "300" ||
			get(p, "IdentityValidationExpression").Value != "^Bearer [-_A-Za-z0-9.]+$" {
			t.Fatalf("authorizer properties wrong")
		}
	}

	// Path of each AWS::ApiGateway::Resource, built from ParentId chains.
	paths := map[string]string{}
	res := ofType(r, "AWS::ApiGateway::Resource")
	var resolve func(id string) string
	resolve = func(id string) string {
		if p, ok := paths[id]; ok {
			return p
		}
		props := get(res[id], "Properties")
		parent := get(props, "ParentId")
		prefix := ""
		if parent.Tag == "!Ref" {
			prefix = resolve(parent.Value)
		} // otherwise !GetAtt Api.RootResourceId
		paths[id] = prefix + "/" + get(props, "PathPart").Value
		return paths[id]
	}
	byPath := map[string]*yaml.Node{}
	methods := methodsByResource(t, r)
	for id := range res {
		if m, ok := methods[id]; ok {
			byPath[resolve(id)] = m
		}
	}

	mcp := get(byPath["/mcp"], "Properties")
	if mcp == nil {
		t.Fatal("no /mcp method")
	}
	if get(mcp, "HttpMethod").Value != "ANY" || get(mcp, "AuthorizationType").Value != "CUSTOM" {
		t.Fatal("/mcp must be ANY with CUSTOM auth")
	}
	if a := get(mcp, "AuthorizerId"); a == nil || a.Tag != "!Ref" || a.Value != authzID {
		t.Fatalf("/mcp must use the TOKEN authorizer %s", authzID)
	}
	if path(mcp, "Integration", "Type").Value != "AWS_PROXY" {
		t.Fatal("/mcp must be a Lambda proxy")
	}

	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		m := get(byPath[p], "Properties")
		if m == nil {
			t.Fatalf("no method on %s", p)
		}
		if get(m, "HttpMethod").Value != "GET" || get(m, "AuthorizationType").Value != "NONE" || get(m, "AuthorizerId") != nil {
			t.Fatalf("%s must be an unauthenticated GET", p)
		}
		if path(m, "Integration", "Type").Value != "MOCK" {
			t.Fatalf("%s must be MOCK", p)
		}
	}
	if len(byPath) != 3 {
		t.Fatalf("want exactly 3 routes, got %v", len(byPath))
	}
}

func TestMetadataBody(t *testing.T) {
	want := `{"resource":"` + mcpURL + `","authorization_servers":["${BridgeUrl}"],"scopes_supported":["openid","email"],"bearer_methods_supported":["header"]}`
	n := 0
	for _, m := range ofType(resources(t), "AWS::ApiGateway::Method") {
		if path(m, "Properties", "Integration", "Type").Value != "MOCK" {
			continue
		}
		ir := path(m, "Properties", "Integration", "IntegrationResponses")
		got := subValue(t, path(ir.Content[0], "ResponseTemplates", "application/json"), "metadata body")
		if strings.TrimSpace(got) != want {
			t.Fatalf("body = %s\nwant %s", got, want)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("want 2 MOCK metadata methods, got %d", n)
	}
}
