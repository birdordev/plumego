package openapi

import "github.com/birdordev/plumego/contract"

// PathParam creates a required path parameter.
func PathParam(name string, schema Schema) Param {
	return Param{Name: name, In: "path", Required: true, Schema: schema}
}

// QueryParam creates an optional query parameter.
func QueryParam(name string, schema Schema) Param {
	return Param{Name: name, In: "query", Schema: schema}
}

// HeaderParam creates an optional header parameter.
func HeaderParam(name string, schema Schema) Param {
	return Param{Name: name, In: "header", Schema: schema}
}

// JSONContent creates an application/json media type map for request and response hints.
func JSONContent(schema Schema) map[string]MediaType {
	return map[string]MediaType{
		contract.ContentTypeJSON: {Schema: schema},
	}
}
