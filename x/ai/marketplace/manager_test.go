package marketplace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/provider"
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

func TestInstalledListsReportPartialReadFailures(t *testing.T) {
	dir := t.TempDir()
	installer, err := NewInstaller(nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"agent", "workflow"} {
		for id, body := range map[string]string{"good": `{"type":"` + kind + `","id":"good","version":"v1","metadata":{"id":"good","version":"v1"}}`, "bad": "{"} {
			record := filepath.Join(dir, kind+"s", id, "v1")
			if err := os.MkdirAll(record, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(record, "install.json"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	agents, agentErr := installer.ListInstalledAgents()
	workflows, workflowErr := installer.ListInstalledWorkflows()
	var syntax *json.SyntaxError
	if len(agents) != 1 || agents[0].ID != "good" || !errors.As(agentErr, &syntax) {
		t.Errorf("agent list=%v, error=%v", agents, agentErr)
	}
	if len(workflows) != 1 || workflows[0].ID != "good" || !errors.As(workflowErr, &syntax) {
		t.Errorf("workflow list=%v, error=%v", workflows, workflowErr)
	}
	if err := installer.UpdateAgent(t.Context(), "good", "v2"); !errors.As(err, &syntax) {
		t.Errorf("update agent = %v", err)
	}
	if err := installer.UpdateWorkflow(t.Context(), "good", "v2"); !errors.As(err, &syntax) {
		t.Errorf("update workflow = %v", err)
	}
}

func TestManagerListInstalledWorkflowsPreservesPartialResults(t *testing.T) {
	manager := newTestManager(t)
	sources := map[string]string{
		"valid":  `{"type":"workflow","id":"valid","version":"v1","metadata":{"id":"valid","version":"v1"}}`,
		"broken": "{",
	}
	for id, source := range sources {
		dir := filepath.Join(manager.installer.installDir, "workflows", id, "v1")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "install.json"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := manager.ListInstalledWorkflows()
	var syntax *json.SyntaxError
	if len(got) != 1 || got[0].ID != "valid" || !errors.As(err, &syntax) {
		t.Errorf("workflows=%v, %v", got, err)
	}
	for id, source := range sources {
		path := filepath.Join(manager.installer.installDir, "workflows", id, "v1", "install.json")
		if data, err := os.ReadFile(path); err != nil || string(data) != source {
			t.Errorf("install record %q changed: %q, %v", id, data, err)
		}
	}
}

func TestManagerWorkflowInstallLifecycle(t *testing.T) {
	m := newTestManager(t)
	ctx := t.Context()
	template := &WorkflowTemplate{ID: "workflow-fixture", Name: "Fixture", Version: "1.0.0", Author: "tester",
		Description: "Fixture workflow for the install lifecycle", Category: WorkflowCategoryGeneral, License: "MIT",
		Steps: []StepTemplate{{Name: "first", Type: "sequential", AgentID: "test-agent"}},
	}
	if err := m.PublishWorkflow(ctx, template); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallWorkflow(ctx, template.ID, template.Version); err != nil {
		t.Fatal(err)
	}
	if !m.IsWorkflowInstalled(template.ID, template.Version) {
		t.Fatal("workflow was not installed")
	}
	if got, err := m.ListInstalledWorkflows(); err != nil || len(got) != 1 || got[0].ID != template.ID || got[0].Version != template.Version {
		t.Fatalf("installed=%+v, %v", got, err)
	}
	if err := m.InstallWorkflow(ctx, template.ID, template.Version); err == nil {
		t.Error("duplicate install succeeded")
	}
	if err := m.UninstallWorkflow(ctx, template.ID, template.Version); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListInstalledWorkflows(); err != nil || len(got) != 0 || m.IsWorkflowInstalled(template.ID, template.Version) {
		t.Errorf("after uninstall=%+v, %v", got, err)
	}
	if got, err := m.GetWorkflow(ctx, template.ID, template.Version); err != nil || got == nil || got.Version != template.Version {
		t.Errorf("registry after uninstall=%+v, %v", got, err)
	}
}

func TestInstalledWorkflowRejectsUnsupportedSteps(t *testing.T) {
	for _, kind := range []string{"sequential", "conditional", "parallel", "historical"} {
		t.Run(kind, func(t *testing.T) {
			m := newTestManager(t)
			ctx := t.Context()
			template := &WorkflowTemplate{
				ID: "workflow-fixture", Name: "Fixture", Version: "1.0.0", Author: "tester",
				Description: "Fixture workflow for assembly", Category: WorkflowCategoryGeneral, License: "MIT",
				Steps: []StepTemplate{
					{Name: "first", Type: "sequential", AgentID: "first-agent", OutputKey: "one"},
					{Name: "second", Type: kind, AgentID: "second-agent", OutputKey: "two"},
				},
			}
			installer := m.Installer()
			if kind == "historical" {
				dir := filepath.Join(installer.installDir, "workflows", template.ID, template.Version)
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := installer.saveInstallationRecord(dir, &InstallationRecord{
					Type: "workflow", ID: template.ID, Version: template.Version, Metadata: template,
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := m.PublishWorkflow(ctx, template); err != nil {
					t.Fatal(err)
				}
				if err := m.InstallWorkflow(ctx, template.ID, template.Version); err != nil {
					t.Fatal(err)
				}
			}
			first, second := &orchestration.Agent{ID: "first-agent"}, &orchestration.Agent{ID: "second-agent"}
			agents := map[string]*orchestration.Agent{first.ID: first, second.ID: second}
			workflow, err := installer.CreateWorkflowFromInstalled(template.ID, template.Version, agents)
			if kind != "sequential" {
				if workflow != nil || err == nil || !strings.Contains(err.Error(), "second") || !strings.Contains(err.Error(), kind) {
					t.Fatalf("unsupported step: workflow=%v error=%v", workflow, err)
				}
				return
			}
			if err != nil || workflow == nil || len(workflow.Steps) != 2 {
				t.Fatalf("sequential assembly: workflow=%v error=%v", workflow, err)
			}
			for i, agent := range []*orchestration.Agent{first, second} {
				step, ok := workflow.Steps[i].(*orchestration.SequentialStep)
				want := template.Steps[i]
				if !ok || step.Name() != want.Name || step.Agent != agent || step.OutputKey != want.OutputKey {
					t.Errorf("step %d = %+v", i, workflow.Steps[i])
				}
			}
			agents[second.ID] = nil
			if workflow, err := installer.CreateWorkflowFromInstalled(template.ID, template.Version, agents); workflow != nil || err == nil {
				t.Errorf("nil agent binding: workflow=%v error=%v", workflow, err)
			}
		})
	}
}

func TestCreateAgentFromInstalledUsesMetadata(t *testing.T) {
	m := newTestManager(t)
	ctx := t.Context()
	meta := testAgentMetadata("agent-fixture", "1.0.0")
	meta.Config = AgentConfig{Temperature: 0.25, MaxTokens: 1234}
	if err := m.PublishAgent(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallAgent(ctx, meta.ID, meta.Version); err != nil {
		t.Fatal(err)
	}
	upstream := provider.NewMockProvider("fixture")
	agent, err := m.Installer().CreateAgentFromInstalled(meta.ID, meta.Version, upstream)
	if err != nil || agent == nil {
		t.Fatalf("CreateAgentFromInstalled = %v, %v", agent, err)
	}
	if agent.ID != meta.ID || agent.Name != meta.Name || agent.Model != meta.Model ||
		agent.SystemPrompt != meta.Prompt.System || agent.Temperature != meta.Config.Temperature ||
		agent.MaxTokens != meta.Config.MaxTokens || agent.Provider != upstream {
		t.Errorf("installed agent did not preserve metadata and provider: %+v", agent)
	}
	if agent, err := m.Installer().CreateAgentFromInstalled(meta.ID, "2.0.0", upstream); agent != nil || err == nil {
		t.Errorf("uninstalled version = %v, %v", agent, err)
	}
}
