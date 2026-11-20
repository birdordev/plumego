package frontend

import (
	"math"
	"net/http"
	"strconv"
	"strings"
)

type weightedToken struct {
	value   string
	quality float64
}

func parseWeightedToken(part string) (weightedToken, bool) {
	part = strings.TrimSpace(part)
	if part == "" {
		return weightedToken{}, false
	}

	pieces := strings.Split(part, ";")
	value := strings.ToLower(strings.TrimSpace(pieces[0]))
	if value == "" {
		return weightedToken{}, false
	}

	q := 1.0
	for _, param := range pieces[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(parsed) || parsed < 0 || parsed > 1 {
			return weightedToken{}, false
		}
		q = parsed
	}
	return weightedToken{value: value, quality: q}, true
}

func varyOnAccept(header http.Header) {
	for _, line := range header.Values("Vary") {
		for field := range strings.SplitSeq(line, ",") {
			field = strings.TrimSpace(field)
			if field == "*" || strings.EqualFold(field, "Accept") {
				return
			}
		}
	}
	header.Add("Vary", "Accept")
}

func acceptsIndex(r *http.Request) bool {
	hasAccept := false
	acceptsXHTML := false
	bestSpecificity := 0
	bestQuality := 0.0
	for _, line := range r.Header.Values("Accept") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hasAccept = true
		for part := range strings.SplitSeq(line, ",") {
			token, ok := parseWeightedToken(part)
			if !ok {
				continue
			}
			var specificity int
			switch token.value {
			case "text/html":
				specificity = 3
			case "text/*":
				specificity = 2
			case "*/*":
				specificity = 1
			case "application/xhtml+xml":
				acceptsXHTML = acceptsXHTML || token.quality > 0
				continue
			default:
				continue
			}
			if specificity > bestSpecificity || (specificity == bestSpecificity && token.quality > bestQuality) {
				bestSpecificity = specificity
				bestQuality = token.quality
			}
		}
	}
	if bestSpecificity > 0 {
		return bestQuality > 0
	}
	return !hasAccept || acceptsXHTML
}
