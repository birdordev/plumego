package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalAgentRegistry_PublishAndGet(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	metadata := &AgentMetadata{
		ID:          "test-agent",
		Name:        "Test Agent",
		Version:     "1.0.0",
		Author:      "Test Author",
		Description: "A test agent for unit testing",
		Category:    CategoryDataAnalysis,
		Tags:        []string{"test", "demo"},
		Provider:    "claude",
		Model:       "claude-3-5-sonnet-20241022",
		Prompt: PromptTemplate{
			System: "You are a helpful assistant",
		},
		License:   "MIT",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Test Publish
	err = registry.Publish(ctx, metadata)
	if err != nil {
		t.Fatalf("failed to publish agent: %v", err)
	}

	// Test Get
	retrieved, err := registry.Get(ctx, "test-agent", "1.0.0")
	if err != nil {
		t.Fatalf("failed to get agent: %v", err)
	}

	if retrieved.ID != metadata.ID {
		t.Errorf("expected ID %s, got %s", metadata.ID, retrieved.ID)
	}
	if retrieved.Name != metadata.Name {
		t.Errorf("expected Name %s, got %s", metadata.Name, retrieved.Name)
	}
}

func TestLocalAgentRegistry_DuplicatePublish(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	metadata := &AgentMetadata{
		ID:          "duplicate-agent",
		Name:        "Duplicate Agent",
		Version:     "1.0.0",
		Author:      "Test Author",
		Description: "A test agent for duplicate testing",
		Category:    CategoryGeneral,
		Provider:    "claude",
		Model:       "claude-3-5-sonnet-20241022",
		Prompt:      PromptTemplate{System: "Test"},
		License:     "MIT",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// First publish should succeed
	err = registry.Publish(ctx, metadata)
	if err != nil {
		t.Fatalf("first publish failed: %v", err)
	}

	// Second publish should fail
	err = registry.Publish(ctx, metadata)
	if err != ErrDuplicateAgent {
		t.Errorf("expected ErrDuplicateAgent, got %v", err)
	}
}

func TestLocalAgentRegistry_ListVersions(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// Publish multiple versions
	versions := []string{"1.0.0", "1.1.0", "2.0.0"}
	for _, ver := range versions {
		metadata := &AgentMetadata{
			ID:          "versioned-agent",
			Name:        "Versioned Agent",
			Version:     ver,
			Author:      "Test Author",
			Description: "A test agent with multiple versions",
			Category:    CategoryGeneral,
			Provider:    "claude",
			Model:       "claude-3-5-sonnet-20241022",
			Prompt:      PromptTemplate{System: "Test"},
			License:     "MIT",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := registry.Publish(ctx, metadata); err != nil {
			t.Fatalf("failed to publish version %s: %v", ver, err)
		}
	}

	// List versions
	listedVersions, err := registry.ListVersions(ctx, "versioned-agent")
	if err != nil {
		t.Fatalf("failed to list versions: %v", err)
	}

	if len(listedVersions) != len(versions) {
		t.Errorf("expected %d versions, got %d", len(versions), len(listedVersions))
	}

	// Versions should be sorted
	if listedVersions[0] != "1.0.0" || listedVersions[2] != "2.0.0" {
		t.Errorf("versions not sorted correctly: %v", listedVersions)
	}
}

func TestLocalAgentRegistry_Search(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// Publish test agents
	agents := []struct {
		id       string
		name     string
		category AgentCategory
		tags     []string
		provider string
	}{
		{"agent-1", "Data Analyzer", CategoryDataAnalysis, []string{"data", "analysis"}, "claude"},
		{"agent-2", "Code Generator", CategoryCodeGeneration, []string{"code", "generation"}, "openai"},
		{"agent-3", "Data Processor", CategoryDataAnalysis, []string{"data", "processing"}, "claude"},
	}

	for _, a := range agents {
		metadata := &AgentMetadata{
			ID:          a.id,
			Name:        a.name,
			Version:     "1.0.0",
			Author:      "Test Author",
			Description: "Test description for " + a.name,
			Category:    a.category,
			Tags:        a.tags,
			Provider:    a.provider,
			Model:       "test-model",
			Prompt:      PromptTemplate{System: "Test"},
			License:     "MIT",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := registry.Publish(ctx, metadata); err != nil {
			t.Fatalf("failed to publish agent %s: %v", a.id, err)
		}
	}

	// Test search by category
	t.Run("SearchByCategory", func(t *testing.T) {
		results, err := registry.Search(ctx, SearchQuery{
			Category: CategoryDataAnalysis,
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})

	// Test search by provider
	t.Run("SearchByProvider", func(t *testing.T) {
		results, err := registry.Search(ctx, SearchQuery{
			Provider: "claude",
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})

	// Test search by text
	t.Run("SearchByText", func(t *testing.T) {
		results, err := registry.Search(ctx, SearchQuery{
			Text: "data",
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})

	// Test search with limit
	t.Run("SearchWithLimit", func(t *testing.T) {
		results, err := registry.Search(ctx, SearchQuery{
			Limit: 2,
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})
}

func TestLocalAgentRegistry_Delete(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	metadata := &AgentMetadata{
		ID:          "delete-agent",
		Name:        "Delete Agent",
		Version:     "1.0.0",
		Author:      "Test Author",
		Description: "A test agent for deletion testing",
		Category:    CategoryGeneral,
		Provider:    "claude",
		Model:       "claude-3-5-sonnet-20241022",
		Prompt:      PromptTemplate{System: "Test"},
		License:     "MIT",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Publish
	if err := registry.Publish(ctx, metadata); err != nil {
		t.Fatalf("failed to publish agent: %v", err)
	}

	// Delete
	if err := registry.Delete(ctx, "delete-agent", "1.0.0"); err != nil {
		t.Fatalf("failed to delete agent: %v", err)
	}

	// Verify deletion
	_, err = registry.Get(ctx, "delete-agent", "1.0.0")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestLocalAgentRegistry_Rating(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// Publish agent
	metadata := &AgentMetadata{
		ID:          "rated-agent",
		Name:        "Rated Agent",
		Version:     "1.0.0",
		Author:      "Test Author",
		Description: "A test agent for rating testing",
		Category:    CategoryGeneral,
		Provider:    "claude",
		Model:       "claude-3-5-sonnet-20241022",
		Prompt:      PromptTemplate{System: "Test"},
		License:     "MIT",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := registry.Publish(ctx, metadata); err != nil {
		t.Fatalf("failed to publish agent: %v", err)
	}

	// Add ratings
	ratings := []*Rating{
		{TargetID: "rated-agent", UserID: "user1", Score: 5.0, CreatedAt: time.Now()},
		{TargetID: "rated-agent", UserID: "user2", Score: 4.0, CreatedAt: time.Now()},
		{TargetID: "rated-agent", UserID: "user3", Score: 5.0, CreatedAt: time.Now()},
	}

	for _, rating := range ratings {
		if err := registry.Rate(ctx, rating); err != nil {
			t.Fatalf("failed to rate agent: %v", err)
		}
	}

	// Get ratings
	retrievedRatings, err := registry.GetRatings(ctx, "rated-agent")
	if err != nil {
		t.Fatalf("failed to get ratings: %v", err)
	}

	if len(retrievedRatings) != 3 {
		t.Errorf("expected 3 ratings, got %d", len(retrievedRatings))
	}

	// Check aggregate rating (should be updated in metadata)
	updated, err := registry.Get(ctx, "rated-agent", "1.0.0")
	if err != nil {
		t.Fatalf("failed to get updated agent: %v", err)
	}

	expectedRating := (5.0 + 4.0 + 5.0) / 3.0
	if updated.Rating != expectedRating {
		t.Errorf("expected rating %.2f, got %.2f", expectedRating, updated.Rating)
	}
}

func TestLocalAgentRegistry_ResolveDependencies(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalAgentRegistry(filepath.Join(tmpDir, "agents"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// Create dependency chain: agent-c depends on agent-b, agent-b depends on agent-a
	agentA := &AgentMetadata{
		ID:           "agent-a",
		Name:         "Agent A",
		Version:      "1.0.0",
		Author:       "Test",
		Description:  "Base agent with no dependencies",
		Category:     CategoryGeneral,
		Provider:     "claude",
		Model:        "test",
		Prompt:       PromptTemplate{System: "Test"},
		Dependencies: []string{},
		License:      "MIT",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	agentB := &AgentMetadata{
		ID:           "agent-b",
		Name:         "Agent B",
		Version:      "1.0.0",
		Author:       "Test",
		Description:  "Agent depending on agent-a",
		Category:     CategoryGeneral,
		Provider:     "claude",
		Model:        "test",
		Prompt:       PromptTemplate{System: "Test"},
		Dependencies: []string{"agent-a@1.0.0"},
		License:      "MIT",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	agentC := &AgentMetadata{
		ID:           "agent-c",
		Name:         "Agent C",
		Version:      "1.0.0",
		Author:       "Test",
		Description:  "Agent depending on agent-b",
		Category:     CategoryGeneral,
		Provider:     "claude",
		Model:        "test",
		Prompt:       PromptTemplate{System: "Test"},
		Dependencies: []string{"agent-b@1.0.0"},
		License:      "MIT",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Publish all agents
	for _, agent := range []*AgentMetadata{agentA, agentB, agentC} {
		if err := registry.Publish(ctx, agent); err != nil {
			t.Fatalf("failed to publish agent %s: %v", agent.ID, err)
		}
	}

	// Resolve dependencies for agent-c
	resolved, err := registry.ResolveDependencies(ctx, "agent-c", "1.0.0")
	if err != nil {
		t.Fatalf("failed to resolve dependencies: %v", err)
	}

	// Should return: [agent-a, agent-b, agent-c] in dependency order
	if len(resolved) != 3 {
		t.Fatalf("expected 3 resolved dependencies, got %d", len(resolved))
	}

	if resolved[0].ID != "agent-a" {
		t.Errorf("expected first resolved to be agent-a, got %s", resolved[0].ID)
	}
	if resolved[1].ID != "agent-b" {
		t.Errorf("expected second resolved to be agent-b, got %s", resolved[1].ID)
	}
	if resolved[2].ID != "agent-c" {
		t.Errorf("expected third resolved to be agent-c, got %s", resolved[2].ID)
	}
}

func TestLocalWorkflowRegistry_Basic(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()

	registry, err := NewLocalWorkflowRegistry(filepath.Join(tmpDir, "workflows"))
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	template := &WorkflowTemplate{
		ID:          "test-workflow",
		Name:        "Test Workflow",
		Version:     "1.0.0",
		Author:      "Test Author",
		Description: "A test workflow template",
		Category:    WorkflowCategoryGeneral,
		Tags:        []string{"test", "demo"},
		Steps: []StepTemplate{
			{
				Name:      "step1",
				Type:      "sequential",
				AgentRef:  "agent-1@1.0.0",
				OutputKey: "result1",
			},
		},
		License:   "MIT",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Test Publish
	err = registry.Publish(ctx, template)
	if err != nil {
		t.Fatalf("failed to publish workflow: %v", err)
	}

	// Test Get
	retrieved, err := registry.Get(ctx, "test-workflow", "1.0.0")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}

	if retrieved.ID != template.ID {
		t.Errorf("expected ID %s, got %s", template.ID, retrieved.ID)
	}
	if len(retrieved.Steps) != len(template.Steps) {
		t.Errorf("expected %d steps, got %d", len(template.Steps), len(retrieved.Steps))
	}
}

func TestLocalRegistryRatingReportsPartialUpdate(t *testing.T) {
	for _, kind := range []string{"agent", "workflow"} {
		t.Run(kind, func(t *testing.T) {
			const id = "example"
			ctx := t.Context()
			root := t.TempDir()
			var rate func(*Rating) error
			var filename string
			versions := []string{"1.0.0", "2.0.0"}
			if kind == "agent" {
				registry, err := NewLocalAgentRegistry(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, version := range versions {
					if err := registry.Publish(ctx, &AgentMetadata{ID: id, Version: version}); err != nil {
						t.Fatal(err)
					}
				}
				rate = func(rating *Rating) error { return registry.Rate(ctx, rating) }
				filename = "metadata.json"
			} else {
				registry, err := NewLocalWorkflowRegistry(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, version := range versions {
					if err := registry.Publish(ctx, &WorkflowTemplate{ID: id, Version: version}); err != nil {
						t.Fatal(err)
					}
				}
				rate = func(rating *Rating) error { return registry.Rate(ctx, rating) }
				filename = "template.json"
			}

			path := func(version string) string { return filepath.Join(root, id, version, filename) }
			checkRating := func(version string, want float64) {
				t.Helper()
				data, err := os.ReadFile(path(version))
				if err != nil {
					t.Fatal(err)
				}
				var stored struct{ Rating float64 }
				if err := json.Unmarshal(data, &stored); err != nil {
					t.Fatal(err)
				}
				if stored.Rating != want {
					t.Errorf("version %s rating = %v, want %v", version, stored.Rating, want)
				}
			}

			rating := &Rating{TargetID: id, UserID: "reviewer", Score: 2}
			if err := rate(rating); err != nil {
				t.Fatal(err)
			}
			checkRating("1.0.0", 2)
			checkRating("2.0.0", 2)

			badPath := path("1.0.0")
			original, err := os.ReadFile(badPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(badPath, []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
			rating.Score = 4
			var syntaxErr *json.SyntaxError
			if err := rate(rating); !errors.As(err, &syntaxErr) || !strings.Contains(err.Error(), id+"@1.0.0") {
				t.Errorf("Rate with corrupt version = %v, want versioned JSON syntax error", err)
			}
			checkRating("2.0.0", 4)
			if err := os.WriteFile(badPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := rate(rating); err != nil {
				t.Fatal(err)
			}
			checkRating("1.0.0", 4)
			checkRating("2.0.0", 4)

			t.Run("read-only version", func(t *testing.T) {
				if err := os.Chmod(badPath, 0400); err != nil {
					t.Fatal(err)
				}
				writable, err := os.OpenFile(badPath, os.O_WRONLY, 0)
				if err == nil {
					_ = writable.Close()
					t.Skip("filesystem permissions allow writes to the read-only fixture")
				}
				if !errors.Is(err, fs.ErrPermission) {
					t.Fatal(err)
				}
				rating.Score = 5
				if err := rate(rating); !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), id+"@1.0.0") {
					t.Errorf("Rate with read-only version = %v, want versioned write error", err)
				}
				checkRating("1.0.0", 4)
				checkRating("2.0.0", 5)
			})
		})
	}
}

func TestRegistrySearchAndMutationComplete(t *testing.T) {
	for _, kind := range []string{"agent", "workflow"} {
		t.Run(kind, func(t *testing.T) {
			manager := newTestManager(t)
			ctx := t.Context()
			var search, publish, remove func() error
			if kind == "agent" {
				const id = "concurrent-agent"
				if err := manager.PublishAgent(ctx, testAgentMetadata(id, "1.0.0")); err != nil {
					t.Fatal(err)
				}
				search = func() error {
					result, err := manager.SearchAgents(ctx, SearchQuery{})
					if err != nil {
						return err
					}
					if len(result) != 1 || result[0].ID != id {
						return fmt.Errorf("search result = %v, want one agent", result)
					}
					return nil
				}
				publish = func() error { return manager.PublishAgent(ctx, testAgentMetadata(id, "2.0.0")) }
				remove = func() error { return manager.DeleteAgent(ctx, id, "2.0.0") }
			} else {
				first, second := workflowVersionFixture("1.0.0"), workflowVersionFixture("2.0.0")
				if err := manager.PublishWorkflow(ctx, first); err != nil {
					t.Fatal(err)
				}
				search = func() error {
					result, err := manager.SearchWorkflows(ctx, SearchQuery{})
					if err != nil {
						return err
					}
					if len(result) != 1 || result[0].ID != first.ID {
						return fmt.Errorf("search result = %v, want one workflow", result)
					}
					return nil
				}
				publish = func() error { return manager.PublishWorkflow(ctx, second) }
				remove = func() error { return manager.DeleteWorkflow(ctx, second.ID, second.Version) }
			}
			mutate := func() error {
				if err := publish(); err != nil {
					return err
				}
				return remove()
			}
			start := make(chan struct{})
			done := make(chan error, 2)
			for _, operation := range []func() error{search, mutate} {
				go func() {
					<-start
					for range 25 {
						if err := operation(); err != nil {
							done <- err
							return
						}
					}
					done <- nil
				}()
			}
			close(start)
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for range 2 {
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-deadline.C:
					t.Fatal("concurrent search and mutation did not complete")
				}
			}
		})
	}
}
