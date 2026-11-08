package rest

import "github.com/birdordev/plumego/router"

// ResourceSpec is the canonical reusable specification for a REST resource.
//
// It lets application code declare one resource shape and reuse it across
// controller construction, route registration, query defaults, hooks, and
// transformation behavior.
type ResourceSpec struct {
	Name    string
	Prefix  string
	Routes  RouteOptions
	Options *ResourceOptions

	QueryBuilder *QueryBuilder
	Hooks        ResourceHooks
	Transformer  ResourceTransformer
}

// DefaultResourceSpec returns the canonical reusable resource specification.
func DefaultResourceSpec(name string) ResourceSpec {
	return ResourceSpec{
		Name:    name,
		Prefix:  "/" + name,
		Routes:  DefaultRouteOptions(),
		Options: DefaultResourceOptions(),
	}
}

// WithPrefix returns a copy of spec with the given route prefix.
func (s ResourceSpec) WithPrefix(prefix string) ResourceSpec {
	s.Prefix = prefix
	return s
}

// Normalized returns a copy of spec with canonical defaults applied.
func (s ResourceSpec) Normalized() ResourceSpec {
	if s.Name == "" {
		s.Name = "resource"
	}
	if s.Prefix == "" {
		s.Prefix = "/" + s.Name
	}
	s.Prefix = normalizePrefix(s.Prefix)

	if s.Options == nil {
		s.Options = DefaultResourceOptions()
	}
	if s.QueryBuilder == nil {
		s.QueryBuilder = NewQueryBuilderFromOptions(s.Options)
	}
	if s.Hooks == nil {
		s.Hooks = &NoOpResourceHooks{}
	}
	if s.Transformer == nil {
		s.Transformer = &IdentityTransformer{}
	}
	if !s.Routes.EnableBatch && !s.Routes.EnableHead && !s.Routes.EnableOptions {
		s.Routes = DefaultRouteOptions()
	}

	return s
}

// ApplyResourceSpec applies a reusable resource specification to a context-aware controller.
func ApplyResourceSpec(controller *BaseContextResourceController, spec ResourceSpec) {
	if controller == nil {
		return
	}

	if spec.Name == "" && controller.ResourceName != "" {
		spec.Name = controller.ResourceName
	}
	spec = spec.Normalized()
	controller.Spec = spec
	controller.ResourceName = spec.Name
	if controller.ParamExtractor == nil {
		controller.ParamExtractor = NewParamExtractor()
	}
	controller.QueryBuilder = queryBuilderFromSpec(spec)
	controller.Hooks = hooksFromSpec(spec)
	controller.Transformer = transformerFromSpec(spec)
}

func queryBuilderFromSpec(spec ResourceSpec) *QueryBuilder {
	spec = spec.Normalized()
	if spec.QueryBuilder != nil {
		return spec.QueryBuilder
	}
	return NewQueryBuilderFromOptions(spec.Options)
}

func hooksFromSpec(spec ResourceSpec) ResourceHooks {
	spec = spec.Normalized()
	if spec.Options != nil && !spec.Options.EnableHooks {
		return &NoOpResourceHooks{}
	}
	if spec.Hooks != nil {
		return spec.Hooks
	}
	return &NoOpResourceHooks{}
}

func transformerFromSpec(spec ResourceSpec) ResourceTransformer {
	spec = spec.Normalized()
	if spec.Options != nil && !spec.Options.EnableTransformer {
		return &IdentityTransformer{}
	}
	if spec.Transformer != nil {
		return spec.Transformer
	}
	return &IdentityTransformer{}
}

// NewDBResource builds a repository-backed controller from a reusable spec.
func NewDBResource[T any](spec ResourceSpec, repository Repository[T]) *DBResourceController[T] {
	spec = spec.Normalized()

	controller := NewDBResourceController[T](spec.Name, repository)
	ApplyResourceSpec(controller.BaseContextResourceController, spec)
	return controller
}

// RegisterDBResource builds and registers a repository-backed controller with canonical routes.
func RegisterDBResource[T any](r *router.Router, spec ResourceSpec, repository Repository[T]) *DBResourceController[T] {
	spec = spec.Normalized()

	controller := NewDBResource[T](spec, repository)
	_ = RegisterResourceRoutes(r, spec.Prefix, controller, spec.Routes)
	return controller
}
