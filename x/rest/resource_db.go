package rest

import (
	"context"
	"time"

	"github.com/birdordev/plumego/contract"
)

// Repository defines the interface for database CRUD operations on a typed resource T.
type Repository[T any] interface {
	FindAll(ctx context.Context, params *QueryParams) ([]T, int64, error)
	FindByID(ctx context.Context, id string) (*T, error)
	Create(ctx context.Context, data *T) error
	Update(ctx context.Context, id string, data *T) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context, params *QueryParams) (int64, error)
	Exists(ctx context.Context, id string) (bool, error)
}

// SoftDeleteRepository marks a record deleted. The repository owns visibility of
// deleted records and returns sql.ErrNoRows for a missing or already deleted ID.
type SoftDeleteRepository interface {
	SoftDelete(ctx context.Context, id string, field string) error
}

// Validator validates a decoded request value before persistence.
// This is the canonical type used by DBResourceController; any package-local
// validator that satisfies Validate(any) error is accepted implicitly.
type Validator interface {
	Validate(v any) error
}

// DBResourceController combines BaseContextResourceController with a Repository,
// providing database-backed CRUD handlers out of the box.
type DBResourceController[T any] struct {
	*BaseContextResourceController
	repository      Repository[T]
	validator       Validator
	strictJSON      *contract.JSONRequestOptions
	onResponseError func(context.Context, error)
}

// NewDBResourceController creates a new database resource controller.
func NewDBResourceController[T any](resourceName string, repository Repository[T]) *DBResourceController[T] {
	return &DBResourceController[T]{
		BaseContextResourceController: NewBaseContextResourceController(resourceName),
		repository:                    repository,
	}
}

// WithValidator sets the validator used by Create and Update handlers.
func (c *DBResourceController[T]) WithValidator(v Validator) *DBResourceController[T] {
	c.validator = v
	return c
}

// WithStrictJSON 为创建和更新启用有大小上限的单文档 JSON 解码。
// 在路由注册前配置控制器。
func (c *DBResourceController[T]) WithStrictJSON(options contract.JSONRequestOptions) *DBResourceController[T] {
	c.strictJSON = &options
	return c
}

// WithResponseErrorObserver configures a callback for response delivery failures.
func (c *DBResourceController[T]) WithResponseErrorObserver(observe func(context.Context, error)) *DBResourceController[T] {
	c.onResponseError = observe
	return c
}

// ApplySpec applies the reusable resource specification to the controller.
func (c *DBResourceController[T]) ApplySpec(spec ResourceSpec) *DBResourceController[T] {
	if c == nil {
		return c
	}
	c.BaseContextResourceController.ApplySpec(spec)
	return c
}

// BaseModel provides common ID and timestamp fields for database models.
type BaseModel struct {
	ID        string    `json:"id" db:"id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// SoftDeleteModel extends BaseModel with a nullable soft-delete timestamp.
type SoftDeleteModel struct {
	BaseModel
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}
