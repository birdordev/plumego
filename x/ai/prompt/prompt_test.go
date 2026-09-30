package prompt

import (
	"fmt"
	"strings"
	"testing"
)

func TestEngine_Register(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "test-template",
		Version: "v1.0.0",
		Content: "Hello {{.Name}}!",
		Variables: []Variable{
			{Name: "Name", Type: "string", Required: true},
		},
	}

	err := engine.Register(t.Context(), tmpl)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Verify ID was generated
	if tmpl.ID == "" {
		t.Error("Template ID should be generated")
	}

	// Verify it can be retrieved
	retrieved, err := engine.Get(t.Context(), tmpl.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.Name != tmpl.Name {
		t.Errorf("Name = %v, want %v", retrieved.Name, tmpl.Name)
	}
}

func TestEngine_Register_InvalidSyntax(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "invalid-template",
		Content: "Hello {{.Name}", // Missing closing brace
	}

	err := engine.Register(t.Context(), tmpl)
	if err == nil {
		t.Error("Register() should fail with invalid template syntax")
	}
}

func TestEngine_Render(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "greeting",
		Version: "v1.0.0",
		Content: "Hello {{.Name}}! Welcome to {{.Place}}.",
		Variables: []Variable{
			{Name: "Name", Type: "string", Required: true},
			{Name: "Place", Type: "string", Required: true},
		},
	}

	engine.Register(t.Context(), tmpl)

	result, err := engine.Render(t.Context(), tmpl.ID, map[string]any{
		"Name":  "Alice",
		"Place": "Wonderland",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	expected := "Hello Alice! Welcome to Wonderland."
	if result != expected {
		t.Errorf("Render() = %v, want %v", result, expected)
	}
}

func TestEngine_Render_MissingRequired(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "test",
		Content: "Hello {{.Name}}!",
		Variables: []Variable{
			{Name: "Name", Type: "string", Required: true},
		},
	}

	engine.Register(t.Context(), tmpl)

	_, err := engine.Render(t.Context(), tmpl.ID, map[string]any{})
	if err == nil {
		t.Error("Render() should fail with missing required variable")
	}
}

func TestEngine_Render_DefaultValue(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "test",
		Content: "Hello {{.Name}}!",
		Variables: []Variable{
			{Name: "Name", Type: "string", Required: true, Default: "World"},
		},
	}

	engine.Register(t.Context(), tmpl)

	result, err := engine.Render(t.Context(), tmpl.ID, map[string]any{})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	expected := "Hello World!"
	if result != expected {
		t.Errorf("Render() = %v, want %v", result, expected)
	}
}

func TestEngine_Render_TemplateFunctions(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "test",
		Content: "{{.Text | upper}}",
	}

	engine.Register(t.Context(), tmpl)

	result, err := engine.Render(t.Context(), tmpl.ID, map[string]any{
		"Text": "hello",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	if result != "HELLO" {
		t.Errorf("Render() = %v, want HELLO", result)
	}
}

func TestEngine_GetByName(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	// Register multiple versions
	v1 := &Template{
		Name:    "test",
		Version: "v1.0.0",
		Content: "Version 1",
	}
	v2 := &Template{
		Name:    "test",
		Version: "v2.0.0",
		Content: "Version 2",
	}

	engine.Register(t.Context(), v1)
	engine.Register(t.Context(), v2)

	// Get latest version
	latest, err := engine.GetByName(t.Context(), "test")
	if err != nil {
		t.Fatalf("GetByName() error = %v", err)
	}

	if latest.Version != "v2.0.0" {
		t.Errorf("Latest version = %v, want v2.0.0", latest.Version)
	}
}

func TestEngine_ListVersions(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	// Register multiple versions
	versions := []string{"v1.0.0", "v1.1.0", "v2.0.0"}
	for _, ver := range versions {
		tmpl := &Template{
			Name:    "test",
			Version: ver,
			Content: "Content",
		}
		engine.Register(t.Context(), tmpl)
	}

	list, err := engine.ListVersions(t.Context(), "test")
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}

	if len(list) != 3 {
		t.Errorf("ListVersions() count = %v, want 3", len(list))
	}
}

func TestEngine_ListByTag(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl1 := &Template{
		Name:    "test1",
		Content: "Content 1",
		Tags:    []string{"coding", "help"},
	}
	tmpl2 := &Template{
		Name:    "test2",
		Content: "Content 2",
		Tags:    []string{"coding"},
	}
	tmpl3 := &Template{
		Name:    "test3",
		Content: "Content 3",
		Tags:    []string{"docs"},
	}

	engine.Register(t.Context(), tmpl1)
	engine.Register(t.Context(), tmpl2)
	engine.Register(t.Context(), tmpl3)

	list, err := engine.ListByTag(t.Context(), "coding")
	if err != nil {
		t.Fatalf("ListByTag() error = %v", err)
	}

	if len(list) != 2 {
		t.Errorf("ListByTag() count = %v, want 2", len(list))
	}
}

func TestEngine_RenderByName(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "greeting",
		Content: "Hello {{.Name}}!",
	}

	engine.Register(t.Context(), tmpl)

	result, err := engine.RenderByName(t.Context(), "greeting", map[string]any{
		"Name": "Bob",
	})
	if err != nil {
		t.Fatalf("RenderByName() error = %v", err)
	}

	if !strings.Contains(result, "Bob") {
		t.Errorf("RenderByName() = %v, should contain 'Bob'", result)
	}
}

func TestEngine_Delete(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	tmpl := &Template{
		Name:    "test",
		Content: "Content",
	}

	engine.Register(t.Context(), tmpl)

	// Delete
	err := engine.Delete(t.Context(), tmpl.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify deletion
	_, err = engine.Get(t.Context(), tmpl.ID)
	if err == nil {
		t.Error("Get() should fail after Delete()")
	}
}

func TestBuiltinTemplates(t *testing.T) {
	templates := BuiltinTemplates()

	if len(templates) == 0 {
		t.Error("BuiltinTemplates() should return at least one template")
	}

	// Check each template has required fields
	for _, tmpl := range templates {
		if tmpl.Name == "" {
			t.Error("Template name should not be empty")
		}
		if tmpl.Content == "" {
			t.Error("Template content should not be empty")
		}
		if tmpl.Model == "" {
			t.Error("Template model should not be empty")
		}
	}
}

func TestLoadBuiltinTemplates(t *testing.T) {
	storage := NewMemoryStorage()
	engine := NewEngine(storage)

	err := LoadBuiltinTemplates(t.Context(), engine)
	if err != nil {
		t.Fatalf("LoadBuiltinTemplates() error = %v", err)
	}

	// Verify templates were loaded
	if storage.Count() == 0 {
		t.Error("Storage should contain builtin templates")
	}

	// Try to render one
	result, err := engine.RenderByName(t.Context(), "summarizer", map[string]any{
		"Text": "This is a test text.",
	})
	if err != nil {
		t.Fatalf("RenderByName() error = %v", err)
	}

	if !strings.Contains(result, "This is a test text.") {
		t.Error("Rendered template should contain input text")
	}
}

func TestMemoryStorage(t *testing.T) {
	storage := NewMemoryStorage()

	tmpl := &Template{
		ID:      "test-1",
		Name:    "test",
		Version: "v1.0.0",
		Content: "Content",
	}

	// Save
	err := storage.Save(t.Context(), tmpl)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load
	loaded, err := storage.Load(t.Context(), "test-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.Name != tmpl.Name {
		t.Errorf("Name = %v, want %v", loaded.Name, tmpl.Name)
	}

	// Delete
	err = storage.Delete(t.Context(), "test-1")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify deletion
	_, err = storage.Load(t.Context(), "test-1")
	if err == nil {
		t.Error("Load() should fail after Delete()")
	}
}

func TestMemoryStorage_Clear(t *testing.T) {
	storage := NewMemoryStorage()

	for i := 0; i < 5; i++ {
		tmpl := &Template{
			ID:      fmt.Sprintf("test-%d", i),
			Name:    fmt.Sprintf("test-%d", i),
			Content: "Content",
		}
		_ = storage.Save(t.Context(), tmpl)
	}

	if count := storage.Count(); count != 5 {
		t.Errorf("Count() = %v, want 5", count)
	}

	storage.Clear()

	if count := storage.Count(); count != 0 {
		t.Errorf("Count() after Clear() = %v, want 0", count)
	}
}
