package openapi

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

func cloneComponents(components *Components) *Components {
	if components == nil {
		return nil
	}
	cloner := schemaCloner{items: make(map[*Schema]*Schema), properties: make(map[uintptr]map[string]Schema)}
	copy := &Components{SecuritySchemes: maps.Clone(components.SecuritySchemes), Schemas: maps.Clone(components.Schemas)}
	for name, schema := range copy.Schemas {
		copy.Schemas[name] = cloner.clone(schema)
	}
	return copy
}

func validateDocument(doc Document) error {
	components := doc.Components
	if components == nil {
		components = &Components{}
	}
	var failures []error
	seenRefs := make(map[string]bool)
	checkSchema := func(where string, schema Schema) {
		for _, ref := range schemaReferences(schema) {
			name, local := strings.CutPrefix(ref, "#/components/schemas/")
			if !local || seenRefs[ref] {
				continue
			}
			seenRefs[ref] = true
			name = strings.NewReplacer("~1", "/", "~0", "~").Replace(name)
			if _, ok := components.Schemas[name]; !ok {
				failures = append(failures, fmt.Errorf("%s refers to undefined schema %q", where, name))
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(components.Schemas)) {
		checkSchema("schema "+name, components.Schemas[name])
	}
	operationIDs := make(map[string]string)
	for _, path := range slices.Sorted(maps.Keys(doc.Paths)) {
		item := doc.Paths[path]
		for _, entry := range []struct {
			method string
			op     *Operation
		}{{"GET", item.Get}, {"POST", item.Post}, {"PUT", item.Put}, {"PATCH", item.Patch}, {"DELETE", item.Delete}, {"HEAD", item.Head}, {"OPTIONS", item.Options}, {"TRACE", item.Trace}} {
			op := entry.op
			if op == nil {
				continue
			}
			where := entry.method + " " + path
			if op.OperationID != "" {
				if first, ok := operationIDs[op.OperationID]; ok {
					failures = append(failures, fmt.Errorf("%s and %s share operationId %q", first, where, op.OperationID))
				}
				operationIDs[op.OperationID] = where
			}
			for _, requirement := range op.Security {
				for _, scheme := range slices.Sorted(maps.Keys(requirement)) {
					if _, ok := components.SecuritySchemes[scheme]; !ok {
						failures = append(failures, fmt.Errorf("%s refers to undefined security scheme %q", where, scheme))
					}
				}
			}
			for _, param := range op.Parameters {
				checkSchema(where, param.Schema)
			}
			if op.RequestBody != nil {
				for _, media := range slices.Sorted(maps.Keys(op.RequestBody.Content)) {
					checkSchema(where, op.RequestBody.Content[media].Schema)
				}
			}
			for _, code := range slices.Sorted(maps.Keys(op.Responses)) {
				content := op.Responses[code].Content
				for _, media := range slices.Sorted(maps.Keys(content)) {
					checkSchema(where, content[media].Schema)
				}
			}
		}
	}
	return errors.Join(failures...)
}

func schemaReferences(schema Schema) []string {
	var refs []string
	seen := schemaCloner{items: make(map[*Schema]*Schema), properties: make(map[uintptr]map[string]Schema)}
	return collectSchemaReferences(schema, seen, refs)
}

func collectSchemaReferences(schema Schema, seen schemaCloner, refs []string) []string {
	if schema.Ref != "" {
		refs = append(refs, schema.Ref)
	}
	if item := schema.Items; item != nil {
		if _, ok := seen.items[item]; !ok {
			seen.items[item] = nil
			refs = collectSchemaReferences(*item, seen, refs)
		}
	}
	if schema.Properties != nil {
		key := reflect.ValueOf(schema.Properties).Pointer()
		if _, ok := seen.properties[key]; !ok {
			seen.properties[key] = nil
			for _, field := range slices.Sorted(maps.Keys(schema.Properties)) {
				refs = collectSchemaReferences(schema.Properties[field], seen, refs)
			}
		}
	}
	return refs
}
