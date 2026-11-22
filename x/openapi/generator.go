package openapi

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
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

// GenerateStrict 要求为每条受支持的路由显式声明响应，并校验路径模板和响应契约。
func (g *Generator) GenerateStrict(routes []router.RouteInfo, hints map[string]Op) (Document, error) {
	var failures []error
	operations := make(map[string]bool, len(routes))
	templates := make(map[string]string, len(routes))
	for _, route := range routes {
		if !setOperation(&PathItem{}, route.Method, &Operation{}) {
			continue
		}
		path, names := routePathTemplate(route.Path)
		key := route.Method + " " + path
		if operations[key] {
			failures = append(failures, fmt.Errorf("duplicate operation %s", key))
		}
		operations[key] = true
		shape := pathTemplateShape(path)
		if prior, exists := templates[shape]; !exists {
			templates[shape] = path
		} else if prior != path {
			failures = append(failures, fmt.Errorf("conflicting path templates %s and %s (%s)", prior, path, key))
		}
		hint := lookupHint(route, hints)
		pathParams := make(map[string]bool, len(names))
		for _, param := range hint.Params {
			if param.In != "path" {
				continue
			}
			if pathParams[param.Name] || !slices.Contains(names, param.Name) {
				failures = append(failures, fmt.Errorf("invalid path parameter %q for %s", param.Name, key))
			}
			pathParams[param.Name] = true
		}
		if len(hint.Responses) == 0 {
			failures = append(failures, fmt.Errorf("missing explicit responses for %s", key))
			continue
		}
		for _, code := range slices.Sorted(maps.Keys(hint.Responses)) {
			response := hint.Responses[code]
			value, err := strconv.Atoi(code)
			isStatus := err == nil && len(code) == 3 && value >= 100 && value <= 599
			isRange := len(code) == 3 && code[0] >= '1' && code[0] <= '5' && code[1:] == "XX"
			if (code != "default" && !isStatus && !isRange) || strings.TrimSpace(response.Description) == "" {
				failures = append(failures, fmt.Errorf("invalid response %q for %s", code, key))
				continue
			}
			bodyless := route.Method == http.MethodHead || code == "1XX" || isStatus && value < 200 ||
				value == http.StatusNoContent || value == http.StatusResetContent || value == http.StatusNotModified
			if bodyless && len(response.Content) != 0 {
				failures = append(failures, fmt.Errorf("response %q for %s cannot declare content", code, key))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return Document{}, err
	}
	return g.Generate(routes, hints), nil
}

func pathTemplateShape(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[i] = "{}"
		}
	}
	return strings.Join(parts, "/")
}
