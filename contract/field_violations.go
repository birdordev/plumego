package contract

import (
	"errors"
	"strings"
)

// FieldViolation 用调用方声明的字段名和机器可读错误码标识无效输入。
// 响应中不包含输入值或原始错误信息。
type FieldViolation struct {
	Source string `json:"source"`
	Field  string `json:"field"`
	Code   string `json:"code"`
}

// FieldViolations 从校验错误复制安全的字段详情。
// 错误没有有效的字段详情时，保留构造器的现有内容。
func (b *ErrorBuilder) FieldViolations(err error) *ErrorBuilder {
	var reporter interface{ FieldViolations() []FieldViolation }
	if !errors.As(err, &reporter) {
		return b
	}
	var fields []FieldViolation
	for _, field := range reporter.FieldViolations() {
		switch field.Source {
		case "body", "path", "query", "header":
			if validViolationLabel(field.Field) && validViolationLabel(field.Code) {
				fields = append(fields, field)
			}
		}
		if len(fields) == 64 {
			break
		}
	}
	if len(fields) != 0 {
		return b.Detail("fields", fields)
	}
	return b
}

func validViolationLabel(value string) bool {
	return len(value) > 0 && len(value) <= 128 && strings.IndexFunc(value, func(r rune) bool {
		isLabelRune := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '[' || r == ']'
		return !isLabelRune
	}) < 0
}
