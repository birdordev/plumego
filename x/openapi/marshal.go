package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

var errWriterNil = errors.New("openapi: writer is nil")

// MarshalJSON serializes an OpenAPI document as readable JSON.
func MarshalJSON(doc Document) ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}

// MarshalYAML serializes an OpenAPI document as minimal YAML without external dependencies.
func MarshalYAML(doc Document) ([]byte, error) {
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}

	var value any
	if err := json.Unmarshal(jsonBytes, &value); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writeYAMLValue(&buf, value, 0)
	if buf.Len() == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// WriteJSON writes an OpenAPI document as JSON.
func WriteJSON(w io.Writer, doc Document) error {
	if w == nil {
		return errWriterNil
	}
	data, err := MarshalJSON(doc)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// WriteYAML writes an OpenAPI document as YAML.
func WriteYAML(w io.Writer, doc Document) error {
	if w == nil {
		return errWriterNil
	}
	data, err := MarshalYAML(doc)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func writeYAMLValue(buf *bytes.Buffer, value any, indent int) {
	switch v := value.(type) {
	case map[string]any:
		writeYAMLMap(buf, v, indent)
	case []any:
		writeYAMLList(buf, v, indent)
	default:
		buf.WriteString(formatYAMLScalar(v))
	}
}

func writeYAMLMap(buf *bytes.Buffer, value map[string]any, indent int) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		writeIndent(buf, indent)
		buf.WriteString(formatYAMLKey(key))
		child := value[key]
		if isYAMLScalar(child) {
			buf.WriteString(": ")
			buf.WriteString(formatYAMLScalar(child))
			buf.WriteByte('\n')
			continue
		}
		buf.WriteString(":\n")
		writeYAMLValue(buf, child, indent+2)
	}
}

// Keep common field names and route templates readable. Quote all other
// keys, including tokens that YAML parsers can resolve as non-string types.
func formatYAMLKey(key string) string {
	if key == "" || (key[0] != '/' && key[0] != '_' && !asciiLetter(key[0])) {
		return quoteYAMLString(key)
	}
	switch strings.ToLower(key) {
	case "null", "true", "false", "y", "n", "yes", "no", "on", "off":
		return quoteYAMLString(key)
	}
	for i := range len(key) {
		c := key[i]
		if !asciiLetter(c) && (c < '0' || c > '9') && !strings.ContainsRune("_-/{}.~", rune(c)) {
			return quoteYAMLString(key)
		}
	}
	return key
}

func asciiLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func quoteYAMLString(s string) string {
	encoded, _ := json.Marshal(s) // Strings always marshal successfully.
	return string(encoded)
}

func writeYAMLList(buf *bytes.Buffer, value []any, indent int) {
	for _, item := range value {
		writeIndent(buf, indent)
		if isYAMLScalar(item) {
			buf.WriteString("- ")
			buf.WriteString(formatYAMLScalar(item))
			buf.WriteByte('\n')
			continue
		}
		buf.WriteString("-\n")
		writeYAMLValue(buf, item, indent+2)
	}
}

func writeIndent(buf *bytes.Buffer, indent int) {
	buf.WriteString(strings.Repeat(" ", indent))
}

func isYAMLScalar(value any) bool {
	switch value := value.(type) {
	case nil, string, bool, float64:
		return true
	case map[string]any:
		return len(value) == 0
	case []any:
		return len(value) == 0
	default:
		return false
	}
}

func formatYAMLScalar(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return quoteYAMLString(v)
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case map[string]any:
		return "{}"
	case []any:
		return "[]"
	default:
		return fmt.Sprintf("%v", v)
	}
}
