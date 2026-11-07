package marketplace

import (
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManagerWithPath(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithPath: %v", err)
	}
	return mgr
}

func testAgentMetadata(id, version string) *AgentMetadata {
	return &AgentMetadata{
		ID:          id,
		Name:        "Test Agent",
		Version:     version,
		Author:      "tester",
		Description: "a test agent for unit testing the marketplace manager",
		Category:    CategoryGeneral,
		Provider:    "claude",
		Model:       "claude-3",
		License:     "MIT",
		Prompt:      PromptTemplate{System: "You are a helpful assistant used in unit tests."},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func TestNewManagerWithPath(t *testing.T) {
	mgr := newTestManager(t)
	if mgr == nil {
		t.Fatal("expected non-nil Manager")
	}
}

func TestManager_PublishAndGetAgent(t *testing.T) {
	mgr := newTestManager(t)
	meta := testAgentMetadata("agent-1", "1.0.0")

	if err := mgr.PublishAgent(t.Context(), meta); err != nil {
		t.Fatalf("PublishAgent: %v", err)
	}

	got, err := mgr.GetAgent(t.Context(), "agent-1", "1.0.0")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.ID != "agent-1" {
		t.Errorf("ID = %q, want agent-1", got.ID)
	}
}

func TestManager_SearchAgents(t *testing.T) {
	mgr := newTestManager(t)
	_ = mgr.PublishAgent(t.Context(), testAgentMetadata("search-1", "1.0.0"))
	_ = mgr.PublishAgent(t.Context(), testAgentMetadata("search-2", "1.0.0"))

	results, err := mgr.SearchAgents(t.Context(), SearchQuery{})
	if err != nil {
		t.Fatalf("SearchAgents: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected >= 2 results, got %d", len(results))
	}
}

func TestManager_ListAgentVersions(t *testing.T) {
	mgr := newTestManager(t)
	_ = mgr.PublishAgent(t.Context(), testAgentMetadata("versioned", "1.0.0"))
	_ = mgr.PublishAgent(t.Context(), testAgentMetadata("versioned", "2.0.0"))

	versions, err := mgr.ListAgentVersions(t.Context(), "versioned")
	if err != nil {
		t.Fatalf("ListAgentVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("expected 2 versions, got %d: %v", len(versions), versions)
	}
}

func TestManager_IsAgentInstalled_NotInstalled(t *testing.T) {
	mgr := newTestManager(t)
	if mgr.IsAgentInstalled("nonexistent", "1.0.0") {
		t.Error("IsAgentInstalled should return false for unpublished agent")
	}
}

func TestManager_ListInstalledAgents_Empty(t *testing.T) {
	mgr := newTestManager(t)
	list, err := mgr.ListInstalledAgents()
	if err != nil {
		t.Fatalf("ListInstalledAgents: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestManager_RateAgent(t *testing.T) {
	mgr := newTestManager(t)
	_ = mgr.PublishAgent(t.Context(), testAgentMetadata("ratable", "1.0.0"))

	rating := &Rating{
		TargetID: "ratable",
		UserID:   "user-1",
		Score:    4.5,
		Comment:  "great agent",
	}
	if err := mgr.RateAgent(t.Context(), rating); err != nil {
		t.Fatalf("RateAgent: %v", err)
	}
}

func TestManager_InstallAgent_NotPublished(t *testing.T) {
	mgr := newTestManager(t)
	err := mgr.InstallAgent(t.Context(), "nonexistent", "1.0.0")
	if err == nil {
		t.Error("expected error installing non-published agent")
	}
}

func TestManager_InstallAndListInstalledAgents(t *testing.T) {
	mgr := newTestManager(t)
	meta := testAgentMetadata("installable", "1.0.0")
	if err := mgr.PublishAgent(t.Context(), meta); err != nil {
		t.Fatalf("PublishAgent: %v", err)
	}

	if err := mgr.InstallAgent(t.Context(), "installable", "1.0.0"); err != nil {
		t.Fatalf("InstallAgent: %v", err)
	}

	if !mgr.IsAgentInstalled("installable", "1.0.0") {
		t.Error("IsAgentInstalled should return true after install")
	}

	list, err := mgr.ListInstalledAgents()
	if err != nil {
		t.Fatalf("ListInstalledAgents: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 installed agent, got %d", len(list))
	}
}

func TestManager_UninstallAgent(t *testing.T) {
	mgr := newTestManager(t)
	meta := testAgentMetadata("removable", "1.0.0")
	if err := mgr.PublishAgent(t.Context(), meta); err != nil {
		t.Fatalf("PublishAgent: %v", err)
	}
	if err := mgr.InstallAgent(t.Context(), "removable", "1.0.0"); err != nil {
		t.Fatalf("InstallAgent: %v", err)
	}

	if err := mgr.UninstallAgent(t.Context(), "removable", "1.0.0"); err != nil {
		t.Fatalf("UninstallAgent: %v", err)
	}

	if mgr.IsAgentInstalled("removable", "1.0.0") {
		t.Error("IsAgentInstalled should return false after uninstall")
	}
}
