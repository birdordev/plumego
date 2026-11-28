package transport

import (
	"strings"

	"github.com/birdordev/plumego/x/openapi"
)

func ServiceCatalogBody(required []string, versioned bool) *openapi.RequestBody {
	fields := make(map[string]openapi.Schema, len(required))
	for _, field := range required {
		fields[field] = openapi.String
	}
	if versioned {
		fields["version"] = openapi.Integer
	}
	return &openapi.RequestBody{Required: true, Description: "必填字段包括 " + strings.Join(required, "、"),
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: fields}}}}
}
