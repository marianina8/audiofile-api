package api

// Route describes one API operation. Path is in OpenAPI form (/tracks/{id}).
// Scope is the Cognito resource-server scope required, or "" for public routes.
type Route struct {
	Method string
	Path   string
	Scope  string
}

// Routes is the single source of truth for the API surface. openapi.yaml and
// template.yaml must list exactly these routes (see TestSpecCoverage).
func Routes() []Route {
	return []Route{
		{Method: "GET", Path: "/health"},
		{Method: "POST", Path: "/tracks/uploads", Scope: "audiofile/write"},
		{Method: "GET", Path: "/tracks", Scope: "audiofile/read"},
		{Method: "GET", Path: "/tracks/{id}", Scope: "audiofile/read"},
		{Method: "PATCH", Path: "/tracks/{id}", Scope: "audiofile/write"},
		{Method: "DELETE", Path: "/tracks/{id}", Scope: "audiofile/write"},
		{Method: "GET", Path: "/tracks/{id}/stream", Scope: "audiofile/read"},
		{Method: "GET", Path: "/tracks/{id}/status", Scope: "audiofile/read"},
		{Method: "GET", Path: "/search", Scope: "audiofile/read"},
		{Method: "GET", Path: "/artists", Scope: "audiofile/read"},
		{Method: "GET", Path: "/artists/{id}", Scope: "audiofile/read"},
		{Method: "GET", Path: "/artists/{id}/tracks", Scope: "audiofile/read"},
		{Method: "GET", Path: "/artists/{id}/albums", Scope: "audiofile/read"},
		{Method: "GET", Path: "/albums", Scope: "audiofile/read"},
		{Method: "GET", Path: "/albums/{id}", Scope: "audiofile/read"},
		{Method: "GET", Path: "/albums/{id}/tracks", Scope: "audiofile/read"},
		{Method: "GET", Path: "/genres", Scope: "audiofile/read"},
		{Method: "GET", Path: "/tags", Scope: "audiofile/read"},
		{Method: "POST", Path: "/tracks/{id}/tags", Scope: "audiofile/write"},
		{Method: "DELETE", Path: "/tracks/{id}/tags/{tag}", Scope: "audiofile/write"},
	}
}
