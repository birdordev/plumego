package rest

import (
	"errors"
	"net/http"
	"strings"

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
