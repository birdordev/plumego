package openapi

import (
	"net/http"
	"strings"

	"github.com/birdordev/plumego/router"
)

const openAPIVersion = "3.1.0"

func lookupHint(route router.RouteInfo, hints map[string]Op) Op {
	if len(hints) == 0 {
		return Op{}
	}
	if route.Meta.Name != "" {
		if hint, ok := hints[route.Meta.Name]; ok {
			return hint
		}
	}
	if hint, ok := hints[route.Method+" "+route.Path]; ok {
		return hint
	}
	return Op{}
}

func operationFromHint(hint Op) *Operation {
	responses := hint.Responses
	if len(responses) == 0 {
		responses = defaultResponses()
	}
	return &Operation{
		Summary:     hint.Summary,
		Description: hint.Description,
		Tags:        append([]string(nil), hint.Tags...),
		Parameters:  append([]Param(nil), hint.Params...),
		RequestBody: hint.Body,
		Responses:   cloneResponses(responses),
	}
}

func defaultResponses() map[string]Response {
	return map[string]Response{
		"200": {Description: "OK"},
	}
}

func cloneResponses(in map[string]Response) map[string]Response {
	out := make(map[string]Response, len(in))
	for code, response := range in {
		out[code] = response
	}
	return out
}

func routePathTemplate(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if len(part) < 2 {
			continue
		}
		switch part[0] {
		case ':', '*':
			parts[i] = "{" + part[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

func setOperation(item *PathItem, method string, op *Operation) {
	switch method {
	case http.MethodGet:
		item.Get = op
	case http.MethodPost:
		item.Post = op
	case http.MethodPut:
		item.Put = op
	case http.MethodPatch:
		item.Patch = op
	case http.MethodDelete:
		item.Delete = op
	case http.MethodHead:
		item.Head = op
	case http.MethodOptions:
		item.Options = op
	}
}
