package router

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spcent/plumego/contract"
)

// normalizeStaticPrefix ensures the prefix always starts with "/"
func normalizeStaticPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return "/"
	}
	if prefix[0] != '/' {
		prefix = "/" + prefix
	}
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return "/"
	}
	return prefix
}

// getFilePathFromRequest extracts and cleans the file path from request
func getFilePathFromRequest(req *http.Request) (string, bool) {
	rc := contract.RequestContextFromContext(req.Context())
	relPath, ok := rc.Params["filepath"]
	if !ok {
		return "", false
	}

	// Security: reject empty paths
	if relPath == "" {
		return "", false
	}

	// Security: reject paths with null bytes or backslashes before cleaning.
	if strings.Contains(relPath, "\x00") || strings.Contains(relPath, "\\") {
		return "", false
	}

	if hasParentTraversal(relPath) {
		return "", false
	}

	if strings.HasPrefix(relPath, "/") {
		return "", false
	}

	// Clean using URL slash semantics before any local filesystem conversion.
	cleanPath := path.Clean(relPath)
	if cleanPath == "." || cleanPath == "/" || strings.HasPrefix(cleanPath, "/") {
		return "", false
	}

	return cleanPath, true
}

func hasParentTraversal(path string) bool {
	if path == ".." {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// Static registers a route that serves files from a local directory
// under the given URL prefix.
//
// Example:
//
//	err := r.Static("/static", "./public")
//	GET /static/js/app.js → ./public/js/app.js
func (r *Router) Static(prefix, dir string) error {
	normalizedPrefix, err := r.preflightStaticRoute(prefix)
	if err != nil {
		return err
	}
	handler, err := newDirectoryHandler(prefix, dir)
	if err != nil {
		return err
	}
	return r.registerStaticRoute(normalizedPrefix, handler)
}

// StaticFS registers a route that serves files from a custom http.FileSystem.
//
// This allows serving from embedded filesystems (e.g., embed.FS) or other backends.
//
// Example:
//
//	//go:embed public/*
//	var public embed.FS
//	sub, err := fs.Sub(public, "public")
//	if err != nil {
//	    return err
//	}
//
//	err := r.StaticFS("/assets", http.FS(sub))
//	GET /assets/index.html → served from embedded FS
func (r *Router) StaticFS(prefix string, fs http.FileSystem) error {
	normalizedPrefix, err := r.preflightStaticRoute(prefix)
	if err != nil {
		return err
	}
	if fs == nil {
		return fmt.Errorf("router static_fs %s: %w", prefix, errors.New("nil file system"))
	}
	return r.registerStaticRoute(normalizedPrefix, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		serveFromFileSystem(w, req, fs)
	}))
}

func (r *Router) preflightStaticRoute(prefix string) (string, error) {
	normalizedPrefix := normalizeStaticPrefix(prefix)
	routePath := staticRoutePath(normalizedPrefix)
	if err := r.routeRegistrationLifecycleError(http.MethodGet, routePath); err != nil {
		return "", err
	}
	if err := validateRouteSegments(routePath, compilePathSegments(routePath)); err != nil {
		return "", fmt.Errorf("router add_route %s %s: %w", http.MethodGet, routePath, err)
	}
	return normalizedPrefix, nil
}

func newDirectoryHandler(prefix, dir string) (http.Handler, error) {
	root, err := resolveStaticRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("router static %s: %w", prefix, err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		serveFromDirectory(w, req, root)
	}), nil
}

func resolveStaticRoot(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("empty directory")
	}
	absRoot, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve directory symlinks: %w", err)
	}
	info, err := os.Stat(realRoot)
	if err != nil {
		return "", fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", dir)
	}
	return realRoot, nil
}

// serveFromDirectory serves files from a local directory
func serveFromDirectory(w http.ResponseWriter, req *http.Request, root string) {
	cleanPath, ok := getFilePathFromRequest(req)
	if !ok {
		http.NotFound(w, req)
		return
	}

	fullPath := filepath.Join(root, filepath.FromSlash(cleanPath))

	realPath, ok := containedStaticFilePath(root, fullPath)
	if !ok {
		http.NotFound(w, req)
		return
	}

	f, err := os.Open(realPath)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	if info.IsDir() {
		http.NotFound(w, req)
		return
	}

	serveFileContent(w, req, f, info)
}

// serveFromFileSystem serves files from a custom http.FileSystem
func serveFromFileSystem(w http.ResponseWriter, req *http.Request, fs http.FileSystem) {
	cleanPath, ok := getFilePathFromRequest(req)
	if !ok {
		http.NotFound(w, req)
		return
	}

	// Open the file
	f, err := fs.Open(cleanPath)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	if info.IsDir() {
		http.NotFound(w, req)
		return
	}

	serveFileContent(w, req, f, info)
}

// containedStaticFilePath resolves a requested local path and verifies the
// resolved file remains inside the static root at check time. Local static
// mounts are intended for read-only or trusted roots; concurrently mutated roots
// can still race portable symlink checks before open.
func containedStaticFilePath(rootDir, requestedPath string) (string, bool) {
	absPath, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", false
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", false
	}
	if !isPathWithinRoot(rootDir, realPath) {
		return "", false
	}
	return realPath, true
}

func isPathWithinRoot(rootDir, targetPath string) bool {
	rel, err := filepath.Rel(rootDir, targetPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// registerStaticRoute registers a GET route for static file primitives.
func (r *Router) registerStaticRoute(prefix string, handler http.Handler) error {
	return r.AddRoute(http.MethodGet, staticRoutePath(prefix), handler)
}

func staticRoutePath(prefix string) string {
	if prefix == "/" {
		return "/*filepath"
	}
	return prefix + "/*filepath"
}

// serveFileContent serves file content with seeking support, using ServeContent
// when possible, falling back to raw copy for non-seekable files.
func serveFileContent(w http.ResponseWriter, req *http.Request, f http.File, info os.FileInfo) {
	if seeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, req, info.Name(), info.ModTime(), seeker)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		_, _ = io.Copy(w, f)
	}
}
