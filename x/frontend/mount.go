package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/birdordev/plumego/router"
)

// Mount describes an explicitly constructed frontend mount.
// It separates bundle construction from router registration.
type Mount struct {
	prefix  string
	handler http.Handler
}

type routeSpec struct {
	method string
	path   string
}

// Registrar is the minimal router contract required to mount a frontend
// handler. `router.Router` satisfies this interface.
//
// Registrars that also expose Routes() []router.RouteInfo get duplicate-route
// preflight before mutation. AddRoute-only registrars are supported as
// best-effort sequential targets and may be left partially registered if a
// later frontend route add fails.
type Registrar interface {
	AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error
}

type routeSnapshotter interface {
	Routes() []router.RouteInfo
}

// RegisterFromDir mounts a built frontend directory (e.g. Next.js `out/`)
// at the given prefix. Returns an error if the directory is missing or unreadable.
func RegisterFromDir(r Registrar, dir string, opts ...Option) error {
	mount, err := NewMountFromDir(dir, opts...)
	if err != nil {
		return err
	}
	return mount.Register(r)
}

// NewMountFromDir constructs a frontend mount from a filesystem directory.
func NewMountFromDir(dir string, opts ...Option) (*Mount, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}
	fsys, err := newLocalDirFSFromDir(dir, cfg.IndexFile)
	if err != nil {
		return nil, err
	}
	h, err := newHandlerFS(fsys, cfg)
	if err != nil {
		return nil, err
	}
	return &Mount{
		prefix:  cfg.Prefix,
		handler: h,
	}, nil
}

// RegisterFS mounts a frontend bundle served from the provided http.FileSystem.
// This is the primary entrypoint for go:embed bundles using http.FS.
//
// Passing http.Dir normalizes to the same directory-backed implementation used
// by RegisterFromDir, including canonical root resolution, index fail-fast, and
// symlink escape rejection. Other http.FileSystem implementations remain lazy:
// index existence, backend readiness, storage boundaries, and precompressed
// variant probing are evaluated during requests.
func RegisterFS(r Registrar, fsys http.FileSystem, opts ...Option) error {
	mount, err := NewMountFS(fsys, opts...)
	if err != nil {
		return err
	}
	return mount.Register(r)
}

// NewMountFS constructs a frontend mount from an http.FileSystem.
//
// http.Dir inputs are treated as directory-backed mounts with fail-fast index
// validation and precompressed variant planning. Other filesystems remain lazy
// and are appropriate for embed, generated, or remote-backed assets where
// startup validation is not possible or not desired.
func NewMountFS(fsys http.FileSystem, opts ...Option) (*Mount, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}
	fsys, err = normalizeFileSystem(fsys, cfg)
	if err != nil {
		return nil, err
	}
	h, err := newHandlerFS(fsys, cfg)
	if err != nil {
		return nil, err
	}
	return &Mount{
		prefix:  cfg.Prefix,
		handler: h,
	}, nil
}

// NewHandlerFS constructs a frontend handler without registering routes.
//
// It follows the same filesystem semantics as NewMountFS but leaves route
// ownership to the caller. Use it when an application needs custom routing or
// middleware composition around the frontend handler.
func NewHandlerFS(fsys http.FileSystem, opts ...Option) (http.Handler, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}
	fsys, err = normalizeFileSystem(fsys, cfg)
	if err != nil {
		return nil, err
	}
	return newHandlerFS(fsys, cfg)
}

func normalizeFileSystem(fsys http.FileSystem, cfg *config) (http.FileSystem, error) {
	if fsys == nil {
		return nil, errors.New("filesystem cannot be nil")
	}
	if dir, ok := fsys.(http.Dir); ok {
		return newLocalDirFSFromDir(string(dir), cfg.IndexFile)
	}
	return fsys, nil
}

func newLocalDirFSFromDir(dir, indexFile string) (localDirFS, error) {
	if dir == "" {
		dir = "."
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("frontend directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("frontend path %q is not a directory", dir)
	}
	if _, err := os.ReadDir(dir); err != nil {
		return "", fmt.Errorf("frontend directory %q not readable: %w", dir, err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("frontend directory %q absolute path: %w", dir, err)
	}
	root, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return "", fmt.Errorf("frontend directory %q real path: %w", dir, err)
	}
	fsys := localDirFS(root)
	if err := validateDirectoryIndex(fsys, indexFile); err != nil {
		return "", fmt.Errorf("frontend directory %q index %q: %w", dir, indexFile, err)
	}
	return fsys, nil
}

func newHandlerFS(fsys http.FileSystem, cfg *config) (http.Handler, error) {
	if fsys == nil {
		return nil, errors.New("filesystem cannot be nil")
	}
	variants, err := newPrecompressedVariantPlan(fsys, cfg.EnablePrecompressed)
	if err != nil {
		return nil, err
	}
	if !variants.known && len(cfg.PrecompressedPlan) > 0 {
		variants = precompressedVariantPlan{
			known:    true,
			variants: cfg.PrecompressedPlan,
		}
	}
	return &handler{
		cfg:      *cfg,
		fs:       fsys,
		variants: variants,
	}, nil
}

// Prefix returns the normalized mount prefix.
//
// It returns an empty string for a nil Mount so callers can inspect optional
// mount values without panicking.
func (m *Mount) Prefix() string {
	if m == nil {
		return ""
	}
	return m.prefix
}

// Handler returns the mounted frontend handler.
//
// It returns nil for a nil Mount so callers can inspect optional mount values
// without panicking.
func (m *Mount) Handler() http.Handler {
	if m == nil {
		return nil
	}
	return m.handler
}

// Register attaches the mount to the provided registrar.
//
// Snapshot-capable registrars get duplicate-route preflight before any frontend
// route is added. AddRoute-only registrars are registered sequentially without
// rollback.
func (m *Mount) Register(r Registrar) error {
	if m == nil {
		return errors.New("mount cannot be nil")
	}
	if r == nil {
		return errors.New("router cannot be nil")
	}
	if m.handler == nil {
		return errors.New("mount handler cannot be nil")
	}

	plan := m.routePlan()
	if err := preflightMountRoutes(r, plan); err != nil {
		return err
	}
	for _, route := range plan {
		if err := r.AddRoute(route.method, route.path, m.handler); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mount) routePlan() []routeSpec {
	if m.prefix == "/" {
		return []routeSpec{
			{method: methodAny, path: "/"},
			{method: methodAny, path: "/*filepath"},
		}
	}

	return []routeSpec{
		{method: methodAny, path: m.prefix + "/*filepath"},
		{method: methodAny, path: m.prefix},
	}
}

func preflightMountRoutes(r Registrar, plan []routeSpec) error {
	snapshotter, ok := r.(routeSnapshotter)
	if !ok {
		return nil
	}
	existing := make(map[routeSpec]struct{})
	for _, route := range snapshotter.Routes() {
		existing[routeSpec{method: route.Method, path: route.Path}] = struct{}{}
	}
	for _, route := range plan {
		if _, ok := existing[route]; ok {
			return fmt.Errorf("frontend mount route %s %s already registered", route.method, route.path)
		}
	}
	return nil
}
