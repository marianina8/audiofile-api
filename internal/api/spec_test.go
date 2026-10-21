package api

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const adminScope = "aws.cognito.signin.user.admin"

type opKey struct{ Method, Path string }

func (k opKey) String() string { return k.Method + " " + k.Path }

// diffSources compares named route sets and returns one message per route that
// is missing from a source but present in another.
func diffSources(sources map[string]map[opKey]bool) []string {
	names := make([]string, 0, len(sources))
	all := map[opKey]bool{}
	for name, set := range sources {
		names = append(names, name)
		for k := range set {
			all[k] = true
		}
	}
	sort.Strings(names)
	keys := make([]opKey, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	var msgs []string
	for _, k := range keys {
		for _, name := range names {
			if !sources[name][k] {
				msgs = append(msgs, fmt.Sprintf("route %s is missing from %s", k, name))
			}
		}
	}
	return msgs
}

func TestDiffSources(t *testing.T) {
	health := opKey{"GET", "/health"}
	extra := opKey{"GET", "/tracks"}
	got := diffSources(map[string]map[opKey]bool{
		"Routes()":      {health: true, extra: true},
		"openapi.yaml":  {health: true},
		"template.yaml": {health: true, extra: true},
	})
	want := []string{"route GET /tracks is missing from openapi.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	same := map[opKey]bool{health: true}
	if msgs := diffSources(map[string]map[opKey]bool{"a": same, "b": same}); len(msgs) != 0 {
		t.Errorf("equal sets reported %v", msgs)
	}
}

func readYAML(t *testing.T, path string) *yaml.Node {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return doc.Content[0]
}

// child returns the value node for key in a mapping node, or nil.
func child(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}

// publicRoutes are the only routes that may be unauthenticated.
var publicRoutes = map[opKey]bool{
	{"GET", "/health"}:     true,
	{"GET", "/play/{pid}"}: true,
}

func TestSpecCoverage(t *testing.T) {
	routes := map[opKey]bool{}
	scopeOf := map[opKey]string{}
	for _, r := range Routes() {
		k := opKey{r.Method, r.Path}
		routes[k] = true
		scopeOf[k] = r.Scope
		if publicRoutes[k] {
			if r.Scope != "" {
				t.Errorf("%s: public route must have no scope", k)
			}
			continue
		}
		want := "audiofile/write"
		if r.Method == "GET" {
			want = "audiofile/read"
		}
		if r.Scope != want {
			t.Errorf("%s: scope = %q, want %q", k, r.Scope, want)
		}
	}

	// openapi.yaml
	spec := readYAML(t, "../../openapi.yaml")
	specOps := map[opKey]bool{}
	paths := child(spec, "paths")
	if paths == nil {
		t.Fatal("openapi.yaml has no paths")
	}
	for i := 0; i+1 < len(paths.Content); i += 2 {
		path, item := paths.Content[i].Value, paths.Content[i+1]
		for j := 0; j+1 < len(item.Content); j += 2 {
			m := item.Content[j].Value
			if !httpMethods[m] {
				continue
			}
			op := item.Content[j+1]
			k := opKey{strings.ToUpper(m), path}
			specOps[k] = true
			if scalar(child(op, "operationId")) == "" {
				t.Errorf("%s: missing operationId", k)
			}
			if scalar(child(op, "summary")) == "" {
				t.Errorf("%s: missing summary", k)
			}
			if publicRoutes[k] {
				sec := child(op, "security")
				if sec == nil || sec.Kind != yaml.SequenceNode || len(sec.Content) != 0 {
					t.Errorf("%s: security must be an explicit empty list", k)
				}
				continue
			}
			sec := child(op, "security")
			if sec == nil || len(sec.Content) != 1 || child(sec.Content[0], "bearerAuth") == nil {
				t.Errorf("%s: security must be [{bearerAuth: []}]", k)
			}
		}
	}

	// template.yaml
	tpl := readYAML(t, "../../template.yaml")
	evs := child(child(child(child(tpl, "Resources"), "ApiFunction"), "Properties"), "Events")
	if evs == nil {
		t.Fatal("template.yaml: ApiFunction has no Events")
	}
	tplOps := map[opKey]bool{}
	publicEvents := map[opKey]bool{}
	for i := 0; i+1 < len(evs.Content); i += 2 {
		name, ev := evs.Content[i].Value, evs.Content[i+1]
		if scalar(child(ev, "Type")) != "HttpApi" {
			continue
		}
		props := child(ev, "Properties")
		k := opKey{strings.ToUpper(scalar(child(props, "Method"))), scalar(child(props, "Path"))}
		if tplOps[k] {
			t.Errorf("event %s: duplicate template event for %s", name, k)
		}
		tplOps[k] = true
		auth := child(props, "Auth")
		if scalar(child(auth, "Authorizer")) == "NONE" {
			if !publicRoutes[k] {
				t.Errorf("event %s (%s): Authorizer NONE is allowed only for GET /health and GET /play/{pid}", name, k)
			}
			publicEvents[k] = true
			continue
		}
		if scalar(child(auth, "Authorizer")) != "CognitoJwt" {
			t.Errorf("event %s (%s): Authorizer must be CognitoJwt or NONE", name, k)
		}
		var got []string
		if sc := child(auth, "AuthorizationScopes"); sc != nil {
			for _, s := range sc.Content {
				got = append(got, s.Value)
			}
		}
		want := []string{scopeOf[k], adminScope}
		if scopeOf[k] == "" || !reflect.DeepEqual(got, want) {
			t.Errorf("event %s (%s): scopes = %v, want %v", name, k, got, want)
		}
	}

	if !reflect.DeepEqual(publicEvents, publicRoutes) {
		t.Errorf("template events with Authorizer NONE = %v, want exactly %v", publicEvents, publicRoutes)
	}
	for _, m := range diffSources(map[string]map[opKey]bool{
		"Routes()":      routes,
		"openapi.yaml":  specOps,
		"template.yaml": tplOps,
	}) {
		t.Error(m)
	}
}

func TestRoutesUnique(t *testing.T) {
	seen := map[opKey]bool{}
	for _, r := range Routes() {
		k := opKey{r.Method, r.Path}
		if seen[k] {
			t.Errorf("duplicate route %s", k)
		}
		seen[k] = true
	}
}

// successCodes is the success status each handler writes.
var successCodes = map[opKey]string{
	{"POST", "/tracks/uploads"}:                    "201",
	{"DELETE", "/tracks/{id}"}:                     "204",
	{"DELETE", "/tracks/{id}/tags/{tag}"}:          "204",
	{"POST", "/playlists"}:                         "201",
	{"DELETE", "/playlists/{id}"}:                  "204",
	{"DELETE", "/playlists/{id}/tracks/{trackId}"}: "204",
	{"POST", "/playlists/{id}/player"}:             "201",
}

func enumValues(n *yaml.Node) []string {
	var out []string
	if e := child(n, "enum"); e != nil {
		for _, v := range e.Content {
			out = append(out, v.Value)
		}
	}
	return out
}

func TestSpecResponses(t *testing.T) {
	spec := readYAML(t, "../../openapi.yaml")
	paths := child(spec, "paths")
	comps := child(spec, "components")
	if paths == nil || comps == nil {
		t.Fatal("openapi.yaml needs paths and components")
	}
	for _, name := range []string{"Unauthorized", "NotFound", "BadRequest"} {
		if child(child(comps, "responses"), name) == nil {
			t.Errorf("components/responses/%s is missing", name)
		}
	}
	pathParam := regexp.MustCompile(`\{([^}]+)\}`)

	for i := 0; i+1 < len(paths.Content); i += 2 {
		path, item := paths.Content[i].Value, paths.Content[i+1]
		for j := 0; j+1 < len(item.Content); j += 2 {
			m := item.Content[j].Value
			if !httpMethods[m] {
				continue
			}
			op := item.Content[j+1]
			k := opKey{strings.ToUpper(m), path}
			resp := child(op, "responses")

			want := successCodes[k]
			if want == "" {
				want = "200"
			}
			if child(resp, want) == nil {
				t.Errorf("%s: missing success response %s", k, want)
			}
			if !publicRoutes[k] && child(resp, "401") == nil {
				t.Errorf("%s: missing 401 response", k)
			}

			declared := map[string]bool{}
			if ps := child(item, "parameters"); ps != nil {
				for _, p := range ps.Content {
					declared[resolveParamName(comps, p)] = true
				}
			}
			if ps := child(op, "parameters"); ps != nil {
				for _, p := range ps.Content {
					if scalar(child(p, "in")) == "path" || child(p, "$ref") != nil {
						declared[resolveParamName(comps, p)] = true
					}
				}
			}
			for _, mm := range pathParam.FindAllStringSubmatch(path, -1) {
				if !declared[mm[1]] {
					t.Errorf("%s: path parameter %q is not declared", k, mm[1])
				}
			}
		}
	}

	schemas := child(comps, "schemas")
	if got, want := enumValues(child(schemas, "TrackStatus")), []string{"processing", "ready", "failed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TrackStatus enum = %v, want %v", got, want)
	}
	track := child(child(schemas, "Track"), "properties")
	if got, want := enumValues(child(track, "bpm_source")), []string{"tag", "measured", "manual"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Track.bpm_source enum = %v, want %v", got, want)
	}
	status := child(track, "status")
	if ref := scalar(child(status, "$ref")); ref != "#/components/schemas/TrackStatus" {
		t.Errorf("Track.status = %q, want a ref to TrackStatus", ref)
	}
}

// resolveParamName returns the name of a parameter, following a components ref.
func resolveParamName(comps, p *yaml.Node) string {
	if ref := scalar(child(p, "$ref")); ref != "" {
		return scalar(child(child(child(comps, "parameters"), strings.TrimPrefix(ref, "#/components/parameters/")), "name"))
	}
	return scalar(child(p, "name"))
}

// resource returns the Properties node of a named template resource.
func resource(tpl *yaml.Node, name string) *yaml.Node {
	return child(child(child(tpl, "Resources"), name), "Properties")
}

// hasValue reports whether a scalar node, or any scalar in a sequence node,
// satisfies match.
func hasValue(n *yaml.Node, match func(string) bool) bool {
	if n.Kind == yaml.ScalarNode {
		return match(n.Value)
	}
	for _, c := range n.Content {
		if c.Kind == yaml.ScalarNode && match(c.Value) {
			return true
		}
	}
	return false
}

func TestTemplateSecurity(t *testing.T) {
	tpl := readYAML(t, "../../template.yaml")

	cfg := child(resource(tpl, "UserPool"), "AdminCreateUserConfig")
	if got := scalar(child(cfg, "AllowAdminCreateUserOnly")); got != "true" {
		t.Errorf("UserPool AllowAdminCreateUserOnly = %q, want true", got)
	}

	pab := child(resource(tpl, "Bucket"), "PublicAccessBlockConfiguration")
	for _, flag := range []string{"BlockPublicAcls", "BlockPublicPolicy", "IgnorePublicAcls", "RestrictPublicBuckets"} {
		if got := scalar(child(pab, flag)); got != "true" {
			t.Errorf("Bucket %s = %q, want true", flag, got)
		}
	}

	limit := child(child(resource(tpl, "Budget"), "Budget"), "BudgetLimit")
	if got := scalar(child(limit, "Amount")); got != "10" {
		t.Errorf("Budget limit = %q, want 10", got)
	}
	if got := scalar(child(limit, "Unit")); got != "USD" {
		t.Errorf("Budget unit = %q, want USD", got)
	}

	route := child(resource(tpl, "HttpApi"), "DefaultRouteSettings")
	if got := scalar(child(route, "ThrottlingRateLimit")); got != "20" {
		t.Errorf("ThrottlingRateLimit = %q, want 20", got)
	}
	if got := scalar(child(route, "ThrottlingBurstLimit")); got != "40" {
		t.Errorf("ThrottlingBurstLimit = %q, want 40", got)
	}

	cli := resource(tpl, "CliClient")
	if got := scalar(child(cli, "GenerateSecret")); got == "true" {
		t.Error("CliClient must not have a secret (GenerateSecret true)")
	}
	if child(cli, "AllowedOAuthFlows") != nil {
		t.Error("CliClient must not enable OAuth flows")
	}

	machine := child(resource(tpl, "MachineClient"), "AllowedOAuthFlows")
	var flows []string
	if machine != nil {
		for _, f := range machine.Content {
			flows = append(flows, f.Value)
		}
	}
	if !reflect.DeepEqual(flows, []string{"client_credentials"}) {
		t.Errorf("MachineClient AllowedOAuthFlows = %v, want [client_credentials]", flows)
	}

	for _, name := range []string{"ApiLogGroup", "ProcessorLogGroup"} {
		if got := scalar(child(resource(tpl, name), "RetentionInDays")); got != "14" {
			t.Errorf("%s RetentionInDays = %q, want 14", name, got)
		}
	}

	var wildcard func(n *yaml.Node)
	wildcard = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			action, res := child(n, "Action"), child(n, "Resource")
			if action != nil && res != nil && hasValue(action, func(v string) bool {
				return v == "*" || v == "s3:*" || v == "dynamodb:*"
			}) && hasValue(res, func(v string) bool { return v == "*" }) {
				t.Errorf("IAM statement at line %d grants a wildcard action on Resource \"*\"", n.Line)
			}
		}
		for _, c := range n.Content {
			wildcard(c)
		}
	}
	wildcard(child(tpl, "Resources"))

	raw, err := os.ReadFile("../../template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(^|\D)\d{12}(\D|$)`).Find(raw); m != nil {
		t.Errorf("template.yaml contains a 12-digit number (account ID?): %q", m)
	}

	cfgFile, err := os.ReadFile("../../samconfig.toml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgFile), "@") {
		t.Error("samconfig.toml must not contain an email address")
	}
}
