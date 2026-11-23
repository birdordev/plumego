package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

var componentNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

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

func validateParameters(operation string, pathNames []string, params []Param) []error {
	type parameterKey struct{ in, name string }
	seen := make(map[parameterKey]bool, len(params))
	var failures []error
	for _, param := range params {
		key := parameterKey{in: param.In, name: param.Name}
		switch param.In {
		case "path":
			if seen[key] || !slices.Contains(pathNames, param.Name) {
				failures = append(failures, fmt.Errorf("invalid path parameter %q for %s", param.Name, operation))
			}
		case "query", "header", "cookie":
			if strings.TrimSpace(param.Name) == "" {
				failures = append(failures, fmt.Errorf("missing name for %s parameter in %s", param.In, operation))
			} else if seen[key] {
				failures = append(failures, fmt.Errorf("duplicate %s parameter %q for %s", param.In, param.Name, operation))
			}
		default:
			failures = append(failures, fmt.Errorf("invalid location %q for parameter %q in %s", param.In, param.Name, operation))
		}
		seen[key] = true
	}
	return failures
}

func validateSecurityScheme(scheme SecurityScheme) error {
	switch scheme.Type {
	case "http":
		if scheme.Scheme == "" || strings.ContainsFunc(scheme.Scheme, func(c rune) bool {
			isASCIIAlphanumeric := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
			return !isASCIIAlphanumeric && !strings.ContainsRune("!#$%&'*+-.^_`|~", c)
		}) {
			return errors.New("HTTP authentication requires a valid scheme token")
		}
	case "apiKey":
		if strings.TrimSpace(scheme.Name) == "" {
			return errors.New("API key requires a name")
		}
		switch scheme.In {
		case "header", "query", "cookie":
		default:
			return errors.New("API key location must be header, query, or cookie")
		}
	case "mutualTLS":
	case "oauth2", "openIdConnect":
		return fmt.Errorf("type %q requires fields outside the supported security scheme model", scheme.Type)
	default:
		return fmt.Errorf("invalid security scheme type %q", scheme.Type)
	}
	return nil
}

func validateDocument(doc Document) error {
	components := doc.Components
	if components == nil {
		components = &Components{}
	}
	var failures []error
	for _, name := range slices.Sorted(maps.Keys(components.SecuritySchemes)) {
		if !componentNamePattern.MatchString(name) {
			failures = append(failures, fmt.Errorf("invalid security scheme component name %q", name))
		}
		if err := validateSecurityScheme(components.SecuritySchemes[name]); err != nil {
			failures = append(failures, fmt.Errorf("security scheme %q: %w", name, err))
		}
	}
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
			target, found := resolveJSONPointer(root, pointer)
			if err != nil || !found {
				failures = append(failures, fmt.Errorf("%s refers to undefined schema reference %q", where, ref))
				continue
			}
			switch target.(type) {
			case map[string]any, bool:
			default:
				failures = append(failures, fmt.Errorf("%s schema reference %q must resolve to an object or boolean", where, ref))
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(components.Schemas)) {
		if !componentNamePattern.MatchString(name) {
			failures = append(failures, fmt.Errorf("invalid schema component name %q", name))
		}
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
				} else {
					operationIDs[op.OperationID] = where
				}
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

func resolveJSONPointer(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	parts, ok := strings.CutPrefix(pointer, "/")
	if !ok {
		return nil, false
	}
	for token := range strings.SplitSeq(parts, "/") {
		token, ok = unescapePointerToken(token)
		if !ok {
			return nil, false
		}
		switch current := value.(type) {
		case map[string]any:
			value, ok = current[token]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.ParseUint(token, 10, strconv.IntSize)
			if err != nil || index >= uint64(len(current)) || strconv.FormatUint(index, 10) != token {
				return nil, false
			}
			value = current[int(index)]
		default:
			return nil, false
		}
	}
	return value, true
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
