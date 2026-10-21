// Package mcphost holds the mcp-host SAM template; this test checks the
// auth wiring that sam validate cannot.
package mcphost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"slices"
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

// at walks keys from n and fails with the missing path instead of panicking.
func at(t *testing.T, n *yaml.Node, what string, keys ...string) *yaml.Node {
	t.Helper()
	for i, k := range keys {
		n = get(n, k)
		if n == nil {
			t.Fatalf("%s: missing %s", what, strings.Join(keys[:i+1], "."))
		}
	}
	return n
}

// str is at() for a scalar's value.
func str(t *testing.T, n *yaml.Node, what string, keys ...string) string {
	t.Helper()
	v := at(t, n, what, keys...)
	if v.Kind != yaml.ScalarNode {
		t.Fatalf("%s: %s is not a scalar", what, strings.Join(keys, "."))
	}
	return v.Value
}

// tagged is str() for a scalar that must carry the given tag (!Ref, !Sub).
func tagged(t *testing.T, n *yaml.Node, tag, what string, keys ...string) string {
	t.Helper()
	v := at(t, n, what, keys...)
	if v.Kind != yaml.ScalarNode || v.Tag != tag {
		t.Fatalf("%s: %s must be a %s scalar, got tag %q", what, strings.Join(keys, "."), tag, v.Tag)
	}
	return v.Value
}

func template(t *testing.T) *yaml.Node {
	t.Helper()
	b, err := os.ReadFile("template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Content) != 1 {
		t.Fatal("template.yaml: want one YAML document")
	}
	return doc.Content[0]
}

func resources(t *testing.T) *yaml.Node {
	t.Helper()
	return at(t, template(t), "template", "Resources")
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

func TestOutputsAndResourceServerUseMcpURL(t *testing.T) {
	if got := tagged(t, template(t), "!Sub", "outputs", "Outputs", "McpUrl", "Value"); got != mcpURL {
		t.Fatalf("McpUrl = %q, want %q", got, mcpURL)
	}
	r := resources(t)
	rs := ofType(r, "AWS::Cognito::UserPoolResourceServer")
	if len(rs) != 1 {
		t.Fatalf("want one resource server, got %d", len(rs))
	}
	for id, n := range rs {
		if got := tagged(t, n, "!Sub", id, "Properties", "Identifier"); got != mcpURL {
			t.Fatalf("Identifier = %q, want %q", got, mcpURL)
		}
	}
	ssm := ofType(r, "AWS::SSM::Parameter")
	if len(ssm) != 1 {
		t.Fatalf("want one SSM parameter, got %d", len(ssm))
	}
	for id, n := range ssm {
		if got := tagged(t, n, "!Sub", id, "Properties", "Value"); got != mcpURL {
			t.Fatalf("SSM Value = %q, want %q", got, mcpURL)
		}
	}
}

func TestUnauthorizedResponsePointsAtResourceMetadata(t *testing.T) {
	for id, n := range ofType(resources(t), "AWS::ApiGateway::GatewayResponse") {
		if str(t, n, id, "Properties", "ResponseType") != "UNAUTHORIZED" {
			continue
		}
		h := tagged(t, n, "!Sub", id, "Properties", "ResponseParameters", "gatewayresponse.header.WWW-Authenticate")
		want := `'Bearer resource_metadata="https://${Api}.execute-api.${AWS::Region}.amazonaws.com/v1/.well-known/oauth-protected-resource"'`
		if h != want {
			t.Fatalf("WWW-Authenticate = %s, want %s", h, want)
		}
		if str(t, n, id, "Properties", "StatusCode") != "401" {
			t.Fatal("UNAUTHORIZED must be 401")
		}
		return
	}
	t.Fatal("no UNAUTHORIZED gateway response")
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
		want := map[string]string{
			"Type":                         "TOKEN",
			"IdentitySource":               "method.request.header.Authorization",
			"AuthorizerResultTtlInSeconds": "300",
			"IdentityValidationExpression": "^[Bb]earer [-_A-Za-z0-9.]+$",
		}
		for k, v := range want {
			if got := str(t, n, id, "Properties", k); got != v {
				t.Fatalf("%s.%s = %q, want %q", id, k, got, v)
			}
		}
	}

	// Path of each AWS::ApiGateway::Resource, built from ParentId chains.
	res := ofType(r, "AWS::ApiGateway::Resource")
	paths := map[string]string{}
	var resolve func(id string, depth int) string
	resolve = func(id string, depth int) string {
		if p, ok := paths[id]; ok {
			return p
		}
		n, ok := res[id]
		if !ok || depth > len(res) {
			t.Fatalf("ParentId %q is not an AWS::ApiGateway::Resource", id)
		}
		parent := at(t, n, id, "Properties", "ParentId")
		prefix := ""
		if parent.Tag == "!Ref" {
			prefix = resolve(parent.Value, depth+1)
		} // otherwise !GetAtt Api.RootResourceId
		paths[id] = prefix + "/" + str(t, n, id, "Properties", "PathPart")
		return paths[id]
	}
	byPath := map[string]*yaml.Node{}
	for id, m := range ofType(r, "AWS::ApiGateway::Method") {
		ref := tagged(t, m, "!Ref", id, "Properties", "ResourceId")
		p := resolve(ref, 0)
		if _, dup := byPath[p]; dup {
			t.Fatalf("two methods on %s", p)
		}
		byPath[p] = at(t, m, id, "Properties")
	}
	if len(byPath) != 3 {
		t.Fatalf("want exactly 3 routes, got %d", len(byPath))
	}

	mcp, ok := byPath["/mcp"]
	if !ok {
		t.Fatal("no /mcp method")
	}
	if str(t, mcp, "/mcp", "HttpMethod") != "ANY" || str(t, mcp, "/mcp", "AuthorizationType") != "CUSTOM" {
		t.Fatal("/mcp must be ANY with CUSTOM auth")
	}
	if tagged(t, mcp, "!Ref", "/mcp", "AuthorizerId") != authzID {
		t.Fatalf("/mcp must use the TOKEN authorizer %s", authzID)
	}
	if str(t, mcp, "/mcp", "Integration", "Type") != "AWS_PROXY" {
		t.Fatal("/mcp must be a Lambda proxy")
	}

	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		m, ok := byPath[p]
		if !ok {
			t.Fatalf("no method on %s", p)
		}
		if str(t, m, p, "HttpMethod") != "GET" || str(t, m, p, "AuthorizationType") != "NONE" || get(m, "AuthorizerId") != nil {
			t.Fatalf("%s must be an unauthenticated GET", p)
		}
		if str(t, m, p, "Integration", "Type") != "MOCK" {
			t.Fatalf("%s must be MOCK", p)
		}
	}
}

func TestMetadataBody(t *testing.T) {
	want := `{"resource":"` + mcpURL + `","authorization_servers":["${BridgeUrl}"],"scopes_supported":["openid","email"],"bearer_methods_supported":["header"]}`
	n := 0
	for id, m := range ofType(resources(t), "AWS::ApiGateway::Method") {
		if str(t, m, id, "Properties", "Integration", "Type") != "MOCK" {
			continue
		}
		ir := at(t, m, id, "Properties", "Integration", "IntegrationResponses")
		if ir.Kind != yaml.SequenceNode || len(ir.Content) != 1 {
			t.Fatalf("%s: want one integration response", id)
		}
		got := tagged(t, ir.Content[0], "!Sub", id, "ResponseTemplates", "application/json")
		if strings.TrimSpace(got) != want {
			t.Fatalf("%s body = %s\nwant %s", id, got, want)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("want 2 MOCK metadata methods, got %d", n)
	}
}

// writeNode feeds a comment- and style-independent encoding of n to h.
func writeNode(h hash.Hash, n *yaml.Node) {
	fmt.Fprintf(h, "(%d %s %q", n.Kind, n.ShortTag(), n.Value)
	for _, c := range n.Content {
		writeNode(h, c)
	}
	fmt.Fprint(h, ")")
}

// TestDeploymentNameTracksAPIShape: an AWS::ApiGateway::Deployment is a
// snapshot, so its logical ID carries a hash of every API Gateway block it
// snapshots. Any change to them renames it, and CloudFormation redeploys.
// (Parameter-only changes are covered by scripts/deploy-mcp.sh.)
func TestDeploymentNameTracksAPIShape(t *testing.T) {
	r := resources(t)
	h := sha256.New()
	for _, typ := range []string{
		"AWS::ApiGateway::Authorizer",
		"AWS::ApiGateway::GatewayResponse",
		"AWS::ApiGateway::Method",
		"AWS::ApiGateway::Resource",
	} {
		blocks := ofType(r, typ)
		ids := make([]string, 0, len(blocks))
		for id := range blocks {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			fmt.Fprintf(h, "%s %s ", typ, id)
			writeNode(h, blocks[id])
		}
	}
	want := "ApiDeployment" + hex.EncodeToString(h.Sum(nil))[:8]

	deps := ofType(r, "AWS::ApiGateway::Deployment")
	if len(deps) != 1 {
		t.Fatalf("want one deployment, got %d", len(deps))
	}
	for id, n := range deps {
		if id != want {
			t.Fatalf("API Gateway blocks changed: rename deployment %s to %s (and ApiStage.DeploymentId)", id, want)
		}
		// The deployment must snapshot after every method and gateway response.
		dependsOn := at(t, n, id, "DependsOn")
		var have []string
		for _, c := range dependsOn.Content {
			have = append(have, c.Value)
		}
		for _, typ := range []string{"AWS::ApiGateway::Method", "AWS::ApiGateway::GatewayResponse"} {
			for dep := range ofType(r, typ) {
				if !slices.Contains(have, dep) {
					t.Fatalf("%s must DependsOn %s", id, dep)
				}
			}
		}
	}
	stage := get(r, "ApiStage")
	if got := tagged(t, stage, "!Ref", "ApiStage", "Properties", "DeploymentId"); got != want {
		t.Fatalf("ApiStage.DeploymentId = %s, want %s", got, want)
	}
	if dependsOn := at(t, stage, "ApiStage", "DependsOn"); dependsOn.Value != "AuthorizerLogGroup" &&
		!slices.ContainsFunc(dependsOn.Content, func(n *yaml.Node) bool { return n.Value == "AuthorizerLogGroup" }) {
		t.Fatal("ApiStage must DependsOn AuthorizerLogGroup")
	}
}
