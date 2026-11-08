package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/log"
)

// Type conversion utilities for configuration values.

// toString converts any value to its string representation.
func toString(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case time.Time:
		return v.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toInt converts any value to int, returning defaultValue on failure.
func toInt(value any, defaultValue int) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		if out, ok := intFromInt64(v); ok {
			return out
		}
	case uint:
		if out, ok := intFromUint64(uint64(v)); ok {
			return out
		}
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		if out, ok := intFromUint64(uint64(v)); ok {
			return out
		}
	case uint64:
		if out, ok := intFromUint64(v); ok {
			return out
		}
	case float32:
		if out, ok := intFromFloat64(float64(v)); ok {
			return out
		}
	case float64:
		if out, ok := intFromFloat64(v); ok {
			return out
		}
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		if v := strings.TrimSpace(v); v != "" {
			if intValue, err := strconv.Atoi(v); err == nil {
				return intValue
			}
		}
	}
	return defaultValue
}

const maxIntValue = int(^uint(0) >> 1)
const minIntValue = -maxIntValue - 1

func intFromInt64(v int64) (int, bool) {
	if v < int64(minIntValue) || v > int64(maxIntValue) {
		return 0, false
	}
	return int(v), true
}

func intFromUint64(v uint64) (int, bool) {
	if v > uint64(maxIntValue) {
		return 0, false
	}
	return int(v), true
}

func intFromFloat64(v float64) (int, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if v < float64(minIntValue) || v > float64(maxIntValue) {
		return 0, false
	}
	return int(v), true
}

// toFloat64 converts any value to float64, returning defaultValue on failure.
func toFloat64(value any, defaultValue float64) float64 {
	switch v := value.(type) {
	case float32:
		return float64(v)
	case float64:
		return v
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case bool:
		if v {
			return 1.0
		}
		return 0.0
	case string:
		if v := strings.TrimSpace(v); v != "" {
			if floatValue, err := strconv.ParseFloat(v, 64); err == nil {
				return floatValue
			}
		}
	}
	return defaultValue
}

// toBool converts any value to bool, returning defaultValue on failure.
func toBool(value any, defaultValue bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	case string:
		if v := strings.TrimSpace(v); v != "" {
			return parseBool(v, defaultValue)
		}
	}
	return defaultValue
}

// parseBool parses a string to bool with common true/false representations.
// Returns defaultValue for unrecognised strings.
func parseBool(value string, defaultValue bool) bool {
	if b, ok := parseBoolToken(value); ok {
		return b
	}
	return defaultValue
}

// parseBoolErr is the error-returning variant of parseBool used by setField.
func parseBoolErr(value string) (bool, error) {
	if b, ok := parseBoolToken(value); ok {
		return b, nil
	}
	return false, fmt.Errorf("invalid boolean value: %q", value)
}

func parseBoolToken(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "t":
		return true, true
	case "0", "false", "no", "n", "off", "f":
		return false, true
	default:
		return false, false
	}
}

// valuesEqual compares two configuration values for equality.
func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			return av == bv
		}
	case int:
		if bv, ok := b.(int); ok {
			return av == bv
		}
	case float64:
		if bv, ok := b.(float64); ok {
			return av == bv
		}
	case bool:
		if bv, ok := b.(bool); ok {
			return av == bv
		}
	}

	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// parseEnvLine parses a single line from an .env file.
// Returns key, value, and ok=true for valid assignment lines.
// Blank lines and # comments return ok=false.
// Quoted values (single or double) have their surrounding quotes stripped and
// escaped inner quotes unescaped.
func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}

	value = strings.TrimSpace(line[idx+1:])

	// Strip surrounding quotes and unescape inner escaped quotes.
	if len(value) >= 2 {
		q := value[0]
		if (q == '"' || q == '\'') && value[len(value)-1] == q {
			value = value[1 : len(value)-1]
			value = strings.ReplaceAll(value, string([]byte{'\\', q}), string(q))
		}
	}

	return key, value, true
}

// Key normalization utilities.

// normalizeKey converts a configuration key to normalized form (lowercase snake_case).
func normalizeKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return strings.ToLower(toSnakeCase(key))
}

// toSnakeCase converts CamelCase or UPPER_SNAKE to lowercase snake_case.
// It correctly handles consecutive uppercase acronyms:
//
//	HTTPSPort   → https_port
//	AppURL      → app_url
//	GetHTTPSConfig → get_https_config
//	APP_NAME    → app_name  (already snake_case, just lowercased)
func toSnakeCase(s string) string {
	if s == "" {
		return s
	}

	// Fast path: if no uppercase letters are present, no transformation is needed.
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}

	runes := []rune(s)
	n := len(runes)
	var result []rune

	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if !upper {
			result = append(result, r)
			continue
		}

		prevLower := i > 0 && runes[i-1] >= 'a' && runes[i-1] <= 'z'
		// Next char exists and is lowercase (e.g. the 'o' in "Port" after "HTTPS")
		nextLower := i+1 < n && runes[i+1] >= 'a' && runes[i+1] <= 'z'
		// Previous char is uppercase and not '_' (we're inside an acronym run)
		prevUpper := i > 0 && runes[i-1] >= 'A' && runes[i-1] <= 'Z'

		if i > 0 && (prevLower || (nextLower && prevUpper)) {
			result = append(result, '_')
		}
		result = append(result, r)
	}
	return strings.ToLower(string(result))
}

// normalizeData normalizes all keys in a configuration map.
// When a logger is provided, any keys that collide after normalization are warned about.
// Later entries in the iteration overwrite earlier ones on collision (last-write wins).
func normalizeData(data map[string]any, logger log.StructuredLogger) map[string]any {
	if len(data) == 0 {
		return map[string]any{}
	}

	normalized := make(map[string]any, len(data))

	// origins is allocated lazily only when a logger is present; it records
	// all original keys that map to each normalized key for collision reporting.
	var origins map[string][]string
	if logger != nil {
		origins = make(map[string][]string, len(data))
	}

	for key, value := range data {
		norm := normalizeKey(key)
		if norm == "" {
			continue
		}
		normalized[norm] = value
		if origins != nil {
			origins[norm] = append(origins[norm], key)
		}
	}

	if logger != nil {
		for norm, originals := range origins {
			if len(originals) > 1 {
				logger.Warn("Config key collision after normalization", log.Fields{
					"key":       norm,
					"originals": originals,
				})
			}
		}
	}

	return normalized
}

// lookupValue looks up a configuration value by normalizing the key to snake_case.
// All keys in the data map are stored normalized (via normalizeData), so only
// the normalized form needs to be checked.
func lookupValue(data map[string]any, key string) (any, bool) {
	if len(data) == 0 {
		return nil, false
	}
	normalized := normalizeKey(key)
	if normalized == "" {
		return nil, false
	}
	value, exists := data[normalized]
	return value, exists
}
