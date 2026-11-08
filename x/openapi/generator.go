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
		path := routePathTemplate(route.Path)
		item := doc.Paths[path]
		op := operationFromHint(lookupHint(route, hints))
		setOperation(&item, route.Method, op)
		doc.Paths[path] = item
	}
	return doc
}
