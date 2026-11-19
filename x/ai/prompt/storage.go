package prompt

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"
)

// MemoryStorage stores and returns copies of template slices and metadata.
// Callers must treat reference values inside Variable.Default as immutable.
type MemoryStorage struct {
	templates map[string]*Template
	mu        sync.RWMutex
}

// NewMemoryStorage creates a new memory storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		templates: make(map[string]*Template),
	}
}

// Save implements Storage.
func (s *MemoryStorage) Save(ctx context.Context, tmpl *Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.templates[tmpl.ID] = cloneTemplate(tmpl)
	return nil
}

// Load implements Storage.
func (s *MemoryStorage) Load(ctx context.Context, templateID string) (*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tmpl, ok := s.templates[templateID]
	if !ok {
		return nil, fmt.Errorf("template not found: %s", templateID)
	}

	return cloneTemplate(tmpl), nil
}

// Delete implements Storage.
func (s *MemoryStorage) Delete(ctx context.Context, templateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.templates, templateID)
	return nil
}

// ListVersions implements Storage.
func (s *MemoryStorage) ListVersions(ctx context.Context, name string) ([]*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var versions []*Template
	for _, tmpl := range s.templates {
		if tmpl.Name == name {
			versions = append(versions, cloneTemplate(tmpl))
		}
	}

	// Sort by version (simple string comparison for now)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version < versions[j].Version
	})

	return versions, nil
}

// ListByTag implements Storage.
func (s *MemoryStorage) ListByTag(ctx context.Context, tag string) ([]*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Template
	for _, tmpl := range s.templates {
		for _, t := range tmpl.Tags {
			if t == tag {
				result = append(result, cloneTemplate(tmpl))
				break
			}
		}
	}

	return result, nil
}

func cloneTemplate(tmpl *Template) *Template {
	copied := *tmpl
	copied.Variables = append([]Variable{}, tmpl.Variables...)
	copied.Tags = append([]string{}, tmpl.Tags...)
	copied.Metadata = maps.Clone(tmpl.Metadata)
	return &copied
}

// Count returns the number of templates.
func (s *MemoryStorage) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.templates)
}

// Clear removes all templates.
func (s *MemoryStorage) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates = make(map[string]*Template)
}
