package rest

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/birdordev/plumego/contract"

	"github.com/birdordev/plumego/router"
)

var (
	errRegisterNilRouter     = errors.New("rest register: router cannot be nil")
	errRegisterNilController = errors.New("rest register: controller cannot be nil")
)

// RouteOptions controls the standard REST route surface.
type RouteOptions struct {
	EnableBatch   bool
	EnableHead    bool
	EnableOptions bool
}

// DefaultRouteOptions returns the canonical REST route surface.
func DefaultRouteOptions() RouteOptions {
	return RouteOptions{
		EnableBatch:   true,
		EnableHead:    true,
		EnableOptions: true,
	}
}

// RegisterResourceRoutes binds the canonical REST routes for a resource controller.
func RegisterResourceRoutes(r *router.Router, prefix string, controller ResourceController, opts RouteOptions) error {
	if r == nil {
		return errRegisterNilRouter
	}
	if controller == nil {
		return errRegisterNilController
	}
	prefix = normalizePrefix(prefix)

	if err := r.AddRoute(http.MethodGet, prefix, http.HandlerFunc(controller.Index)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, resourceRoutePath(prefix, "/:id"), http.HandlerFunc(controller.Show)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPost, prefix, http.HandlerFunc(controller.Create)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPut, resourceRoutePath(prefix, "/:id"), http.HandlerFunc(controller.Update)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodDelete, resourceRoutePath(prefix, "/:id"), http.HandlerFunc(controller.Delete)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPatch, resourceRoutePath(prefix, "/:id"), http.HandlerFunc(controller.Patch)); err != nil {
		return err
	}

	if opts.EnableOptions {
		if err := r.AddRoute(http.MethodOptions, prefix, http.HandlerFunc(controller.Options)); err != nil {
			return err
		}
		if err := r.AddRoute(http.MethodOptions, resourceRoutePath(prefix, "/:id"), http.HandlerFunc(controller.Options)); err != nil {
			return err
		}
	}
	if opts.EnableHead {
		if err := r.AddRoute(http.MethodHead, prefix, http.HandlerFunc(controller.Head)); err != nil {
			return err
		}
		if err := r.AddRoute(http.MethodHead, resourceRoutePath(prefix, "/:id"), http.HandlerFunc(controller.Head)); err != nil {
			return err
		}
	}
	if opts.EnableBatch {
		if err := r.AddRoute(http.MethodPost, resourceRoutePath(prefix, "/batch"), http.HandlerFunc(controller.BatchCreate)); err != nil {
			return err
		}
		if err := r.AddRoute(http.MethodDelete, resourceRoutePath(prefix, "/batch"), http.HandlerFunc(controller.BatchDelete)); err != nil {
			return err
		}
	}
	return nil
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return "/"
	}
	return prefix
}

func resourceRoutePath(prefix, suffix string) string {
	if prefix == "/" {
		return suffix
	}
	return prefix + suffix
}

// Registrar is the route registration capability used by resource adapters.
type Registrar interface {
	AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error
}

// ResourceOperations selects optional operations for a resource registration.
// GET, POST, PUT and DELETE are registered for their normal resource paths.
type ResourceOperations struct {
	Patch, BatchCreate, BatchDelete, Options bool
}

// RegisterResource registers a resource through its caller's route registrar.
// When enabled, OPTIONS returns the methods registered on each path.
// A batch operation reserves the batch path for the selected batch methods.
func RegisterResource(r Registrar, prefix string, c ResourceController, opts ResourceOperations) error {
	if r == nil {
		return errRegisterNilRouter
	}
	if c == nil {
		return errRegisterNilController
	}
	prefix = normalizePrefix(prefix)
	type operation struct {
		method  string
		handler http.HandlerFunc
	}
	member := []operation{{http.MethodGet, c.Show}, {http.MethodPut, c.Update}, {http.MethodDelete, c.Delete}}
	if opts.Patch {
		member = append(member, operation{http.MethodPatch, c.Patch})
	}
	var batch []operation
	if opts.BatchCreate {
		batch = append(batch, operation{http.MethodPost, c.BatchCreate})
	}
	if opts.BatchDelete {
		batch = append(batch, operation{http.MethodDelete, c.BatchDelete})
	}

	if len(batch) != 0 {
		methods := make([]string, 0, len(batch)+1)
		for _, op := range batch {
			methods = append(methods, op.method)
		}
		if opts.Options {
			methods = append(methods, http.MethodOptions)
		}
		sort.Strings(methods)
		allow := strings.Join(methods, ", ")
		rejectBatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeMethodNotAllowed).Build())
		})
		for i := range member {
			handle := member[i].handler
			member[i].handler = func(w http.ResponseWriter, r *http.Request) {
				if router.Param(r, "id") == "batch" {
					rejectBatch.ServeHTTP(w, r)
					return
				}
				handle(w, r)
			}
		}
		// An exact fallback advertises batch methods for methods without a
		// member route. The router still prioritizes registered methods.
		if err := r.AddRoute(router.MethodAny, resourceRoutePath(prefix, "/batch"), rejectBatch); err != nil {
			return err
		}
	}
	routes := []struct {
		path       string
		operations []operation
	}{
		{prefix, []operation{{http.MethodGet, c.Index}, {http.MethodPost, c.Create}}},
		{resourceRoutePath(prefix, "/:id"), member},
		{resourceRoutePath(prefix, "/batch"), batch},
	}
	for _, route := range routes {
		if len(route.operations) == 0 {
			continue
		}
		methods := make([]string, 0, len(route.operations)+2)
		for _, op := range route.operations {
			if err := r.AddRoute(op.method, route.path, op.handler); err != nil {
				return err
			}
			methods = append(methods, op.method)
			if op.method == http.MethodGet {
				methods = append(methods, http.MethodHead)
			}
		}
		if opts.Options {
			methods = append(methods, http.MethodOptions)
			sort.Strings(methods)
			allow := strings.Join(methods, ", ")
			if err := r.AddRoute(http.MethodOptions, route.path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Allow", allow)
				w.WriteHeader(http.StatusNoContent)
			})); err != nil {
				return err
			}
		}
	}
	return nil
}
