package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	loadDocument := sync.OnceValues(func() (any, error) {
		data, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		var value any
		err = json.Unmarshal(data, &value)
		return value, err
	})
	checkSchema := func(where string, schema Schema) {
		for _, ref := range schemaReferences(schema) {
			fragment, local := strings.CutPrefix(ref, "#")
			if !local || seenRefs[ref] {
				continue
			}
			seenRefs[ref] = true
			root, err := loadDocument()
			if err != nil {
				failures = append(failures, fmt.Errorf("%s cannot resolve a local schema reference: %w", where, err))
				continue
			}
			pointer, err := url.PathUnescape(fragment)
			if err != nil || !jsonPointerExists(root, pointer) {
				failures = append(failures, fmt.Errorf("%s refers to undefined schema reference %q", where, ref))
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

func jsonPointerExists(value any, pointer string) bool {
	if pointer == "" {
		return true
	}
	parts, ok := strings.CutPrefix(pointer, "/")
	if !ok {
		return false
	}
	for token := range strings.SplitSeq(parts, "/") {
		token, ok = unescapePointerToken(token)
		if !ok {
			return false
		}
		switch current := value.(type) {
		case map[string]any:
			value, ok = current[token]
			if !ok {
				return false
			}
		case []any:
			index, err := strconv.ParseUint(token, 10, strconv.IntSize)
			if err != nil || index >= uint64(len(current)) || strconv.FormatUint(index, 10) != token {
				return false
			}
			value = current[int(index)]
		default:
			return false
		}
	}
	return true
}

func unescapePointerToken(token string) (string, bool) {
	var decoded strings.Builder
	for {
		before, after, found := strings.Cut(token, "~")
		decoded.WriteString(before)
		if !found {
			return decoded.String(), true
		}
		if after == "" || (after[0] != '0' && after[0] != '1') {
			return "", false
		}
		if after[0] == '0' {
			decoded.WriteByte('~')
		} else {
			decoded.WriteByte('/')
		}
		token = after[1:]
	}
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
