package prompt

import (
	"fmt"
	"strings"
	"testing"
	"text/template"
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

func TestMemoryStorageOwnsTemplateContainers(t *testing.T) {
	store := NewMemoryStorage()
	ctx := t.Context()
	template := &Template{
		ID: "first", Name: "sample", Version: "1",
		Tags: []string{"original"}, Variables: []Variable{{Name: "original"}},
		Metadata: map[string]string{"color": "blue"},
	}
	if err := store.Save(ctx, template); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, &Template{ID: "second", Name: "sample", Version: "2"}); err != nil {
		t.Fatal(err)
	}
	mutate := func(tmpl *Template) {
		tmpl.Tags[0] = "changed"
		tmpl.Variables[0].Name = "changed"
		tmpl.Metadata["color"] = "red"
	}
	assertOriginal := func(tmpl *Template) {
		t.Helper()
		if tmpl.Tags[0] != "original" || tmpl.Variables[0].Name != "original" || tmpl.Metadata["color"] != "blue" {
			t.Fatalf("stored template changed: %+v", tmpl)
		}
	}

	mutate(template)
	got, err := store.Load(ctx, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(got)
	mutate(got)
	versions, err := store.ListVersions(ctx, template.Name)
	if err != nil || len(versions) != 2 || versions[0].ID != "first" || versions[1].ID != "second" {
		t.Fatalf("versions = %v, error = %v", versions, err)
	}
	assertOriginal(versions[0])
	mutate(versions[0])
	tagged, err := store.ListByTag(ctx, "original")
	if err != nil || len(tagged) != 1 || tagged[0].ID != "first" {
		t.Fatalf("tagged = %v, error = %v", tagged, err)
	}
	assertOriginal(tagged[0])
	mutate(tagged[0])
	got, err = store.Load(ctx, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal(got)
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

func TestRegisterNormalizesDefaultVersionBeforeGeneratingID(t *testing.T) {
	ctx := t.Context()
	store := NewMemoryStorage()
	engine := NewEngine(store)

	implicit := &Template{Name: "fixture", Content: "first"}
	explicit := &Template{Name: "fixture", Version: "v1.0.0", Content: "updated"}
	for _, tpl := range []*Template{implicit, explicit} {
		if err := engine.Register(ctx, tpl); err != nil {
			t.Fatal(err)
		}
	}
	if implicit.Version != "v1.0.0" || implicit.ID != explicit.ID || explicit.ID != "fixture:v1.0.0" {
		t.Errorf("implicit identity=%q @ %q; explicit identity=%q", implicit.ID, implicit.Version, explicit.ID)
	}
	if list, err := engine.ListVersions(ctx, "fixture"); err != nil || len(list) != 1 {
		t.Errorf("ListVersions = %v, %v; want one version", list, err)
	}
	if got, err := engine.RenderByName(ctx, "fixture", nil); err != nil || got != "updated" {
		t.Errorf("RenderByName = %q, %v; want updated content", got, err)
	}

	legacy := &Template{ID: "legacy:", Name: "legacy", Version: "v1.0.0", Content: "legacy content"}
	if err := store.Save(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if got, err := engine.Render(ctx, legacy.ID, nil); err != nil || got != legacy.Content {
		t.Errorf("legacy ID Render = %q, %v", got, err)
	}
	custom := &Template{ID: "custom-id", Name: "custom", Content: "custom content"}
	if err := engine.Register(ctx, custom); err != nil {
		t.Fatal(err)
	}
	if custom.ID != "custom-id" || custom.Version != "v1.0.0" {
		t.Errorf("explicit identity = %q @ %q", custom.ID, custom.Version)
	}
}

func TestEngineWithFuncsSupportsRegistrationAndRendering(t *testing.T) {
	ctx := t.Context()
	engine := NewEngine(NewMemoryStorage(), WithFuncs(template.FuncMap{
		"wrap": func(s string) string { return "[" + s + "]" },
	}))
	tpl := &Template{Name: "fixture", Content: `{{wrap "custom"}} {{upper "default"}}`}
	if err := engine.Register(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	if got, err := engine.Render(ctx, tpl.ID, nil); err != nil || got != "[custom] DEFAULT" {
		t.Errorf("Render = %q, %v", got, err)
	}
	invalid := &Template{Name: "invalid", Content: `{{missingFunction "fixture"}}`}
	if err := engine.Register(ctx, invalid); err == nil {
		t.Error("registration accepted an undefined function")
	}
	if got, err := engine.Get(ctx, invalid.ID); got != nil || err == nil {
		t.Errorf("Get for invalid template = %v, %v", got, err)
	}
}
