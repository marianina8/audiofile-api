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
	}
}
