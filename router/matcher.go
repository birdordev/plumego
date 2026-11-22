package router

import "net/url"

// matchNode finds the most specific complete match and copies its parameters
// out of the pooled slice before returning.
func matchNode(root *node, parts []string, rawPath string, escaped bool) *matchResult {
	if root == nil {
		return nil
	}

	params := getParamValues()
	defer putParamValues(params)
	offset := 0
	if len(rawPath) > 0 && rawPath[0] == '/' {
		offset = 1
	}
	matched := matchBranch(root, parts, rawPath, offset, params, escaped)
	if matched == nil {
		return nil
	}

	return newMatchResult(matched, matched.fullPath, "", *params)
}

func newMatchResult(matched *node, pattern, method string, params []string) *matchResult {
	result := &matchResult{
		Handler: matched.handler, RoutePattern: pattern, RouteName: matched.routeName, RouteMethod: method, paramKeys: matched.paramKeys,
	}
	result.routeState.SetPattern(pattern)
	result.routeState.SetName(matched.routeName)
	for i, value := range params {
		result.routeState.AddParam(matched.paramKeys[i], value)
	}
	return result
}

// matchBranch tries static, parameter, and wildcard children in that order.
// A failed branch leaves params unchanged. With nil params the traversal only
// checks for a match. When capturing parameters, offset points to the current
// segment in rawPath so a wildcard can capture the suffix without joining it.
func matchBranch(current *node, parts []string, rawPath string, offset int, params *[]string, escaped bool) *node {
	if len(parts) == 0 {
		if current.handler != nil {
			return current
		}
		return nil
	}
	segment := parts[0]
	if segment == "" {
		return nil
	}
	nextOffset := offset + len(segment) + 1
	if escaped {
		segment, _ = url.PathUnescape(segment)
	}

	if child := findStaticChild(current, segment); child != nil && child.paramName == "" {
		if matched := matchBranch(child, parts[1:], rawPath, nextOffset, params, escaped); matched != nil {
			return matched
		}
	}
	if child := findParamChild(current); child != nil {
		var previous int
		if params != nil {
			previous = len(*params)
			*params = append(*params, segment)
		}
		if matched := matchBranch(child, parts[1:], rawPath, nextOffset, params, escaped); matched != nil {
			return matched
		}
		if params != nil {
			(*params)[previous] = ""
			*params = (*params)[:previous]
		}
	}
	if child := findWildChild(current); child != nil && child.handler != nil {
		if params != nil {
			tail := rawPath[offset:]
			if escaped {
				tail, _ = url.PathUnescape(tail)
			}
			*params = append(*params, tail)
		}
		return child
	}
	return nil
}
