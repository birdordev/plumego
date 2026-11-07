package router

// matchNode finds the most specific complete match and copies its parameters
// out of the pooled slice before returning.
func matchNode(root *node, parts []string, rawPath string) *matchResult {
	if root == nil {
		return nil
	}

	params := getParamValues()
	defer putParamValues(params)
	offset := 0
	if len(rawPath) > 0 && rawPath[0] == '/' {
		offset = 1
	}
	matched := matchBranch(root, parts, rawPath, offset, params)
	if matched == nil {
		return nil
	}

	return &matchResult{
		Handler:      matched.handler,
		ParamValues:  append([]string(nil), (*params)...),
		ParamKeys:    matched.paramKeys,
		RoutePattern: matched.fullPath,
		RouteName:    matched.routeName,
	}
}

// matchBranch tries static, parameter, and wildcard children in that order.
// A failed branch leaves params unchanged. offset points to the current
// segment in rawPath so a wildcard can capture the suffix without joining it.
func matchBranch(current *node, parts []string, rawPath string, offset int, params *[]string) *node {
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

	if child := findStaticChild(current, segment); child != nil && child.paramName == "" {
		if matched := matchBranch(child, parts[1:], rawPath, nextOffset, params); matched != nil {
			return matched
		}
	}
	if child := findParamChild(current); child != nil {
		previous := len(*params)
		*params = append(*params, segment)
		if matched := matchBranch(child, parts[1:], rawPath, nextOffset, params); matched != nil {
			return matched
		}
		(*params)[previous] = ""
		*params = (*params)[:previous]
	}
	if child := findWildChild(current); child != nil && child.handler != nil {
		*params = append(*params, rawPath[offset:])
		return child
	}
	return nil
}
