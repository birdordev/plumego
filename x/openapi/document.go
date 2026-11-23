package openapi

var (
	String  = Schema{Type: "string"}
	Integer = Schema{Type: "integer"}
	Boolean = Schema{Type: "boolean"}
	Array   = Schema{Type: "array"}
)

// Op carries optional operation hints for one route.
type Op struct {
	OperationID string                `json:"operationId,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Params      []Param               `json:"parameters,omitempty"`
	Body        *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses,omitempty"`
	Security    []SecurityRequirement `json:"security,omitempty"`
}

// Param describes an OpenAPI operation parameter.
type Param struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Schema      Schema `json:"schema,omitempty"`
}

// Schema describes a minimal OpenAPI schema object.
type Schema struct {
	Type       string            `json:"type,omitempty"`
	Format     string            `json:"format,omitempty"`
	Properties map[string]Schema `json:"properties,omitempty"`
	Items      *Schema           `json:"items,omitempty"`
	Ref        string            `json:"$ref,omitempty"`
}

// RequestBody describes an OpenAPI requestBody object.
type RequestBody struct {
	Description string               `json:"description,omitempty"`
	Required    bool                 `json:"required,omitempty"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// Response describes an OpenAPI response object.
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// MediaType describes an OpenAPI media type object.
type MediaType struct {
	Schema Schema `json:"schema,omitempty"`
}

// Document is the root OpenAPI document.
type Document struct {
	OpenAPI    string              `json:"openapi"`
	Info       Info                `json:"info"`
	Paths      map[string]PathItem `json:"paths"`
	Components *Components         `json:"components,omitempty"`
}

// SecurityRequirement 记录一种认证选择涉及的方案及可选 OAuth 权限范围。
type SecurityRequirement map[string][]string

// Components holds reusable schemas and security schemes.
type Components struct {
	Schemas         map[string]Schema         `json:"schemas,omitempty"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}

// SecurityScheme 声明 API 密钥或 HTTP 认证方案。
type SecurityScheme struct {
	Type         string `json:"type"`
	Name         string `json:"name,omitempty"`
	In           string `json:"in,omitempty"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

// Info describes OpenAPI document metadata.
type Info struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

// PathItem holds operations for one route path.
type PathItem struct {
	Get     *Operation `json:"get,omitempty"`
	Post    *Operation `json:"post,omitempty"`
	Put     *Operation `json:"put,omitempty"`
	Patch   *Operation `json:"patch,omitempty"`
	Delete  *Operation `json:"delete,omitempty"`
	Head    *Operation `json:"head,omitempty"`
	Options *Operation `json:"options,omitempty"`
	Trace   *Operation `json:"trace,omitempty"`
}

// Operation describes one OpenAPI operation.
type Operation struct {
	OperationID string                `json:"operationId,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Parameters  []Param               `json:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses"`
	Security    []SecurityRequirement `json:"security,omitempty"`
}
