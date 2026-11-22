package openapi

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/router"
)

// Generator builds OpenAPI documents from router route metadata.
type Generator struct {
	info Info
}

// New creates a generator with default document metadata.
func New() *Generator {
	return &Generator{info: defaultInfo()}
}

func defaultInfo() Info {
	return Info{Title: "Plumego API", Version: "0.0.0"}
}

// Generate converts route metadata and optional operation hints into a document.
// It adds required string parameters for path placeholders without hints.
// It includes GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, and TRACE routes.
func (g *Generator) Generate(routes []router.RouteInfo, hints map[string]Op) Document {
	info := defaultInfo()
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
		if setOperation(&item, route.Method, op) {
			doc.Paths[path] = item
		}
	}
	return doc
}

// GenerateStrict 要求为每条受支持的路由显式声明响应。
// 路径规范化后出现重复操作时返回错误。
func (g *Generator) GenerateStrict(routes []router.RouteInfo, hints map[string]Op) (Document, error) {
	var failures []error
	operations := make(map[string]bool, len(routes))
	for _, route := range routes {
		if !setOperation(&PathItem{}, route.Method, &Operation{}) {
			continue
		}
		path, _ := routePathTemplate(route.Path)
		key := route.Method + " " + path
		if operations[key] {
			failures = append(failures, fmt.Errorf("duplicate operation %s", key))
		}
		operations[key] = true
		responses := lookupHint(route, hints).Responses
		if len(responses) == 0 {
			failures = append(failures, fmt.Errorf("missing explicit responses for %s", key))
			continue
		}
		for code, response := range responses {
			value, err := strconv.Atoi(code)
			isStatus := err == nil && len(code) == 3 && value >= 100 && value <= 599
			isRange := len(code) == 3 && code[0] >= '1' && code[0] <= '5' && code[1:] == "XX"
			if (code != "default" && !isStatus && !isRange) || strings.TrimSpace(response.Description) == "" {
				failures = append(failures, fmt.Errorf("invalid response %q for %s", code, key))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return Document{}, err
	}
	return g.Generate(routes, hints), nil
}
