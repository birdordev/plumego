package openapi

import "github.com/birdordev/plumego/router"

// Generator builds OpenAPI documents from router route metadata.
type Generator struct {
	info Info
}

// New creates a generator with default document metadata.
func New() *Generator {
	return &Generator{
		info: Info{
			Title:   "Plumego API",
			Version: "0.0.0",
		},
	}
}

// Generate converts route metadata and optional operation hints into a document.
// It adds required string parameters for path placeholders without hints.
func (g *Generator) Generate(routes []router.RouteInfo, hints map[string]Op) Document {
	info := Info{Title: "Plumego API", Version: "0.0.0"}
	if g != nil {
		info = g.info
	}
	doc := Document{
		OpenAPI: openAPIVersion,
		Info:    info,
		Paths:   make(map[string]PathItem),
	}

	for _, route := range routes {
		path, parameterNames := routePathTemplate(route.Path)
		item := doc.Paths[path]
		op := operationFromHint(lookupHint(route, hints))
		op.Parameters = completePathParameters(op.Parameters, parameterNames)
		setOperation(&item, route.Method, op)
		doc.Paths[path] = item
	}
	return doc
}
