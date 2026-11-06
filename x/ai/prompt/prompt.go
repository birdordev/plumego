// Package prompt provides template management for AI prompts.
package prompt

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"
)

// Template represents a prompt template.
type Template struct {
	ID          string
	Name        string
	Version     string
	Content     string
	Variables   []Variable
	Model       string
	Temperature float64
	MaxTokens   int
	Tags        []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Metadata    map[string]string
}

// Variable defines a template variable.
type Variable struct {
	Name        string
	Type        string // string, int, float, bool, array, object
	Description string
	Required    bool
	Default     any
}

// Engine manages and renders prompt templates.
type Engine struct {
	storage Storage
	cache   *templateCache
	funcs   template.FuncMap
}

// NewEngine creates a new prompt engine.
func NewEngine(storage Storage, opts ...EngineOption) *Engine {
	e := &Engine{
		storage: storage,
		cache:   newTemplateCache(100), // Default cache size
		funcs:   defaultFuncMap(),
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// EngineOption configures the engine.
type EngineOption func(*Engine)

// WithCacheSize sets the cache size.
func WithCacheSize(size int) EngineOption {
	return func(e *Engine) {
		e.cache = newTemplateCache(size)
	}
}

// WithFuncs adds custom template functions.
func WithFuncs(funcs template.FuncMap) EngineOption {
	return func(e *Engine) {
		for name, fn := range funcs {
			e.funcs[name] = fn
		}
	}
}

// Register registers a new template.
func (e *Engine) Register(ctx context.Context, tmpl *Template) error {
	if tmpl.ID == "" {
		tmpl.ID = generateID(tmpl.Name, tmpl.Version)
	}

	if tmpl.Version == "" {
		tmpl.Version = "v1.0.0"
	}

	now := time.Now()
	if tmpl.CreatedAt.IsZero() {
		tmpl.CreatedAt = now
	}
	tmpl.UpdatedAt = now

	// Validate template syntax
	if _, err := e.parseTemplate(tmpl); err != nil {
		return fmt.Errorf("invalid template syntax: %w", err)
	}

	if err := e.storage.Save(ctx, tmpl); err != nil {
		return fmt.Errorf("save template: %w", err)
	}

	// Invalidate cache
	e.cache.delete(tmpl.ID)

	return nil
}

// Get retrieves a template by ID.
func (e *Engine) Get(ctx context.Context, templateID string) (*Template, error) {
	return e.storage.Load(ctx, templateID)
}

// GetByName retrieves the latest version of a template by name.
func (e *Engine) GetByName(ctx context.Context, name string) (*Template, error) {
	versions, err := e.storage.ListVersions(ctx, name)
	if err != nil {
		return nil, err
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("template not found: %s", name)
	}

	// Return the latest version (assume sorted)
	return versions[len(versions)-1], nil
}

// ListVersions lists all versions of a template.
func (e *Engine) ListVersions(ctx context.Context, name string) ([]*Template, error) {
	return e.storage.ListVersions(ctx, name)
}

// Render renders a template with the given variables.
func (e *Engine) Render(ctx context.Context, templateID string, vars map[string]any) (string, error) {
	// Get template
	tmpl, err := e.Get(ctx, templateID)
	if err != nil {
		return "", err
	}

	return e.RenderTemplate(tmpl, vars)
}

// RenderTemplate renders a template object.
func (e *Engine) RenderTemplate(tmpl *Template, vars map[string]any) (string, error) {
	// Check required variables
	for _, v := range tmpl.Variables {
		if v.Required {
			if _, ok := vars[v.Name]; !ok {
				// Use default if available
				if v.Default != nil {
					vars[v.Name] = v.Default
				} else {
					return "", fmt.Errorf("required variable missing: %s", v.Name)
				}
			}
		}
	}

	// Check cache
	parsed := e.cache.get(tmpl.ID)
	if parsed == nil {
		// Parse template
		var err error
		parsed, err = e.parseTemplate(tmpl)
		if err != nil {
			return "", fmt.Errorf("parse template: %w", err)
		}

		// Cache it
		e.cache.set(tmpl.ID, parsed)
	}

	// Execute template
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderByName renders a template by name (latest version).
func (e *Engine) RenderByName(ctx context.Context, name string, vars map[string]any) (string, error) {
	tmpl, err := e.GetByName(ctx, name)
	if err != nil {
		return "", err
	}

	return e.RenderTemplate(tmpl, vars)
}

// Delete deletes a template.
func (e *Engine) Delete(ctx context.Context, templateID string) error {
	e.cache.delete(templateID)
	return e.storage.Delete(ctx, templateID)
}

// ListByTag lists templates by tag.
func (e *Engine) ListByTag(ctx context.Context, tag string) ([]*Template, error) {
	return e.storage.ListByTag(ctx, tag)
}

// parseTemplate parses a template string.
func (e *Engine) parseTemplate(tmpl *Template) (*template.Template, error) {
	return template.New(tmpl.ID).Funcs(e.funcs).Parse(tmpl.Content)
}

// Storage interface for template persistence.
type Storage interface {
	Save(ctx context.Context, tmpl *Template) error
	Load(ctx context.Context, templateID string) (*Template, error)
	Delete(ctx context.Context, templateID string) error
	ListVersions(ctx context.Context, name string) ([]*Template, error)
	ListByTag(ctx context.Context, tag string) ([]*Template, error)
}

// templateCache caches parsed templates.
type templateCache struct {
	templates map[string]*template.Template
	maxSize   int
	mu        sync.RWMutex
}

// newTemplateCache creates a new template cache.
func newTemplateCache(maxSize int) *templateCache {
	return &templateCache{
		templates: make(map[string]*template.Template),
		maxSize:   maxSize,
	}
}

// get retrieves a cached template.
func (c *templateCache) get(id string) *template.Template {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.templates[id]
}

// set caches a template.
func (c *templateCache) set(id string, tmpl *template.Template) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple eviction: clear cache if full
	if len(c.templates) >= c.maxSize {
		c.templates = make(map[string]*template.Template)
	}

	c.templates[id] = tmpl
}

// delete removes a template from cache.
func (c *templateCache) delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.templates, id)
}

// defaultFuncMap returns default template functions.
func defaultFuncMap() template.FuncMap {
	return template.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": titleString,
		"trim":  strings.TrimSpace,
		"join": func(sep string, items []string) string {
			return strings.Join(items, sep)
		},
		"default": func(def any, val any) any {
			if val == nil || val == "" {
				return def
			}
			return val
		},
	}
}

// titleString title-cases the first rune of each word. Word separators are
// ASCII characters other than letters, digits, and underscores, plus
// non-ASCII Unicode spaces. These rules preserve the "title" template
// function's documented behavior.
func titleString(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		if isTitleSeparator(prev) {
			prev = r
			return unicode.ToTitle(r)
		}
		prev = r
		return r
	}, s)
}

// isTitleSeparator reports whether r separates words for titleString,
// matching strings.Title's rules.
func isTitleSeparator(r rune) bool {
	if r <= 0x7F {
		switch {
		case '0' <= r && r <= '9':
			return false
		case 'a' <= r && r <= 'z':
			return false
		case 'A' <= r && r <= 'Z':
			return false
		case r == '_':
			return false
		}
		return true
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	return unicode.IsSpace(r)
}

// generateID generates a template ID.
func generateID(name, version string) string {
	return fmt.Sprintf("%s:%s", name, version)
}
