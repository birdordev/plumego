package openapi

import (
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/birdordev/plumego/router"
)

const openAPIVersion = "3.1.0"

func lookupHint(route router.RouteInfo, hints map[string]Op) Op {
	if key := hintKey(route, hints); key != "" {
		return hints[key]
	}
	return Op{}
}

func hintKey(route router.RouteInfo, hints map[string]Op) string {
	if len(hints) == 0 {
		return ""
	}
	if route.Meta.Name != "" {
		if _, ok := hints[route.Meta.Name]; ok {
			return route.Meta.Name
		}
	}
	key := route.Method + " " + route.Path
	if _, ok := hints[key]; ok {
		return key
	}
	return ""
}

func operationFromHint(hint Op) *Operation {
	responses := hint.Responses
	if len(responses) == 0 {
		responses = defaultResponses()
	}
	cloner := schemaCloner{
		items:      make(map[*Schema]*Schema),
		properties: make(map[uintptr]map[string]Schema),
	}
	params := slices.Clone(hint.Params)
	for i := range params {
		params[i].Schema = cloner.clone(params[i].Schema)
	}
	body := hint.Body
	if body != nil {
		copy := *body
		copy.Content = cloner.cloneContent(body.Content)
		body = &copy
	}
	responses = maps.Clone(responses)
	for code, response := range responses {
		response.Content = cloner.cloneContent(response.Content)
		responses[code] = response
	}
	security := slices.Clone(hint.Security)
	for i, requirement := range security {
		security[i] = make(SecurityRequirement, len(requirement))
		for scheme, scopes := range requirement {
			security[i][scheme] = append([]string{}, scopes...)
		}
	}
	return &Operation{
		OperationID: hint.OperationID,
		Security:    security,
		Summary:     hint.Summary,
		Description: hint.Description,
		Tags:        slices.Clone(hint.Tags),
		Parameters:  params,
		RequestBody: body,
		Responses:   responses,
	}
}

func defaultResponses() map[string]Response {
	return map[string]Response{
		"200": {Description: "OK"},
	}
}

// schemaCloner records both kinds of reference before descending into them.
// A Properties map can refer to itself through a Schema value.
type schemaCloner struct {
	items      map[*Schema]*Schema
	properties map[uintptr]map[string]Schema
}

func (c schemaCloner) clone(schema Schema) Schema {
	if schema.Items != nil {
		original := schema.Items
		item, ok := c.items[original]
		if !ok {
			item = new(Schema)
			c.items[original] = item
			*item = c.clone(*original)
		}
		schema.Items = item
	}
	if schema.Properties != nil {
		original := schema.Properties
		key := reflect.ValueOf(original).Pointer()
		properties, ok := c.properties[key]
		if !ok {
			properties = make(map[string]Schema, len(original))
			c.properties[key] = properties
			for name, field := range original {
				properties[name] = c.clone(field)
			}
		}
		schema.Properties = properties
	}
	return schema
}

func (c schemaCloner) cloneContent(content map[string]MediaType) map[string]MediaType {
	result := maps.Clone(content)
	for name, media := range result {
		media.Schema = c.clone(media.Schema)
		result[name] = media
	}
	return result
}

func routePathTemplate(path string) (string, []string) {
	if path == "" || path == "/" {
		return "/", nil
	}
	parts := strings.Split(path, "/")
	var names []string
	for i, part := range parts {
		if len(part) < 2 {
			continue
		}
		switch part[0] {
		case ':', '*':
			names = append(names, part[1:])
			parts[i] = "{" + part[1:] + "}"
		}
	}
	return strings.Join(parts, "/"), names
}

// completePathParameters operates on the generated operation's owned parameters.
func completePathParameters(params []Param, names []string) []Param {
	for _, name := range names {
		i := slices.IndexFunc(params, func(p Param) bool {
			return p.In == "path" && p.Name == name
		})
		if i >= 0 {
			params[i].Required = true
		} else {
			params = append(params, PathParam(name, Schema{Type: "string"}))
		}
	}
	return params
}

func setOperation(item *PathItem, method string, op *Operation) bool {
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
	case http.MethodTrace:
		item.Trace = op
	default:
		return false
	}
	return true
}
