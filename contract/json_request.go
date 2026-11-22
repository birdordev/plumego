package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

const defaultJSONBodyLimit = 1 << 20

// JSONRequestOptions 定义严格 JSON 解码选项。MaxBytes 为零时使用 1 MiB。
// 默认拒绝结构体中不存在的字段。
type JSONRequestOptions struct {
	MaxBytes           int64
	AllowUnknownFields bool
}

// DecodeJSONRequest 将单个非 null 的 JSON 文档解码到 dst，接受
// application/json 或 application/*+json，并拒绝尾部空白以外的数据。
// 解码失败可能已修改 dst。请求体仍由调用方管理。
// 返回的 APIError 可交给 WriteError，原始错误保留供调用方分类。
func DecodeJSONRequest(r *http.Request, dst any, opts JSONRequestOptions) error {
	if opts.MaxBytes < 0 {
		return NewErrorBuilder().Type(TypeInternal).Message("invalid JSON body limit").Build()
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = defaultJSONBodyLimit
	}
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return invalidJSONRequest(io.EOF)
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get(HeaderContentType))
	isJSON := mediaType == ContentTypeJSON ||
		(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json"))
	if err != nil || !isJSON {
		return NewErrorBuilder().Type(TypeUnsupportedMediaType).Message("expected a JSON request body").Build()
	}
	if r.ContentLength > opts.MaxBytes {
		return invalidJSONRequest(&http.MaxBytesError{Limit: opts.MaxBytes})
	}

	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, opts.MaxBytes))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return invalidJSONRequest(err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("additional JSON value")
		}
		return invalidJSONRequest(err)
	}
	if bytes.Equal(raw, []byte("null")) {
		return invalidJSONRequest(errors.New("JSON document is null"))
	}

	valueDecoder := json.NewDecoder(bytes.NewReader(raw))
	if !opts.AllowUnknownFields {
		valueDecoder.DisallowUnknownFields()
	}
	if err := valueDecoder.Decode(dst); err != nil {
		var invalidDestination *json.InvalidUnmarshalError
		if errors.As(err, &invalidDestination) {
			return NewErrorBuilder().Type(TypeInternal).Message("invalid JSON destination").Wrap(err).Build()
		}
		return invalidJSONRequest(err)
	}
	return nil
}

func invalidJSONRequest(err error) APIError {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		return NewErrorBuilder().Type(TypePayloadTooLarge).Message("JSON request body too large").Wrap(err).Build()
	}
	return NewErrorBuilder().Type(TypeBadRequest).Message("invalid JSON request body").Wrap(err).Build()
}
