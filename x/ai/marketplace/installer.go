package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/provider"
)

// Installer handles agent and workflow installation.
type Installer struct {
	agentRegistry    AgentRegistry
	workflowRegistry WorkflowRegistry
	installDir       string
}

// NewInstaller creates a new installer.
func NewInstaller(
	agentRegistry AgentRegistry,
	workflowRegistry WorkflowRegistry,
	installDir string,
) (*Installer, error) {
	if installDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		installDir = filepath.Join(homeDir, ".plumego", "installed")
	}

	if err := os.MkdirAll(installDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create install directory: %w", err)
	}

	return &Installer{
		agentRegistry:    agentRegistry,
		workflowRegistry: workflowRegistry,
		installDir:       installDir,
	}, nil
}

// InstallAgent installs an agent and its dependencies.
func (i *Installer) InstallAgent(ctx context.Context, id string, version string) error {
	// Check if already installed
	if i.IsAgentInstalled(id, version) {
		return fmt.Errorf("agent %s@%s is already installed", id, version)
	}

	// Resolve dependencies
	dependencies, err := i.agentRegistry.ResolveDependencies(ctx, id, version)
	if err != nil {
		return fmt.Errorf("failed to resolve dependencies: %w", err)
	}

	// Install dependencies first
	for _, dep := range dependencies {
		// Skip if it's the target agent (it's at the end of the list)
		if dep.ID == id && dep.Version == version {
			continue
		}

		if !i.IsAgentInstalled(dep.ID, dep.Version) {
			if err := i.installAgentMetadata(ctx, dep); err != nil {
				return fmt.Errorf("failed to install dependency %s@%s: %w", dep.ID, dep.Version, err)
			}
		}
	}

	// Install target agent
	metadata, err := i.agentRegistry.Get(ctx, id, version)
	if err != nil {
		return fmt.Errorf("failed to get agent metadata: %w", err)
	}

	if err := i.installAgentMetadata(ctx, metadata); err != nil {
		return fmt.Errorf("failed to install agent: %w", err)
	}

	// Update download count
	_ = i.agentRegistry.UpdateDownloadCount(ctx, id)

	return nil
}

// installAgentMetadata installs agent metadata to local installation directory.
func (i *Installer) installAgentMetadata(ctx context.Context, metadata *AgentMetadata) error {
	agentDir := filepath.Join(i.installDir, "agents", metadata.ID, metadata.Version)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}

	// Create installation record
	record := InstallationRecord{
		Type:        "agent",
		ID:          metadata.ID,
		Version:     metadata.Version,
		InstalledAt: time.Now(),
		Metadata:    metadata,
	}

	return i.saveInstallationRecord(agentDir, &record)
}

// UninstallAgent uninstalls an agent.
func (i *Installer) UninstallAgent(ctx context.Context, id string, version string) error {
	if !i.IsAgentInstalled(id, version) {
		return fmt.Errorf("agent %s@%s is not installed", id, version)
	}

	agentDir := filepath.Join(i.installDir, "agents", id, version)
	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("failed to uninstall agent: %w", err)
	}

	// Clean up empty parent directory
	parentDir := filepath.Join(i.installDir, "agents", id)
	entries, err := os.ReadDir(parentDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(parentDir)
	}

	return nil
}

// IsAgentInstalled checks if an agent is installed.
func (i *Installer) IsAgentInstalled(id string, version string) bool {
	agentDir := filepath.Join(i.installDir, "agents", id, version)
	_, err := os.Stat(filepath.Join(agentDir, "install.json"))
	return err == nil
}

// ListInstalledAgents lists all installed agents.
func (i *Installer) ListInstalledAgents() ([]*AgentMetadata, error) {
	agentsDir := filepath.Join(i.installDir, "agents")
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) {
		return []*AgentMetadata{}, nil
	}

	var agents []*AgentMetadata

	// Walk through agent directories
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read agents directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		agentID := entry.Name()
		versionEntries, err := os.ReadDir(filepath.Join(agentsDir, agentID))
		if err != nil {
			continue
		}

		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() {
				continue
			}

			record, err := i.loadInstallationRecord(filepath.Join(agentsDir, agentID, versionEntry.Name()))
			if err != nil {
				continue
			}

			if metadata, ok := record.Metadata.(*AgentMetadata); ok {
				agents = append(agents, metadata)
			}
		}
	}

	return agents, nil
}

// InstallWorkflow installs a workflow template.
func (i *Installer) InstallWorkflow(ctx context.Context, id string, version string) error {
	// Check if already installed
	if i.IsWorkflowInstalled(id, version) {
		return fmt.Errorf("workflow %s@%s is already installed", id, version)
	}

	// Get workflow template
	template, err := i.workflowRegistry.Get(ctx, id, version)
	if err != nil {
		return fmt.Errorf("failed to get workflow template: %w", err)
	}

	// Install workflow
	workflowDir := filepath.Join(i.installDir, "workflows", template.ID, template.Version)
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		return fmt.Errorf("failed to create workflow directory: %w", err)
	}

	// Create installation record
	record := InstallationRecord{
		Type:        "workflow",
		ID:          template.ID,
		Version:     template.Version,
		InstalledAt: time.Now(),
		Metadata:    template,
	}

	return i.saveInstallationRecord(workflowDir, &record)
}

// UninstallWorkflow uninstalls a workflow.
func (i *Installer) UninstallWorkflow(ctx context.Context, id string, version string) error {
	if !i.IsWorkflowInstalled(id, version) {
		return fmt.Errorf("workflow %s@%s is not installed", id, version)
	}

	workflowDir := filepath.Join(i.installDir, "workflows", id, version)
	if err := os.RemoveAll(workflowDir); err != nil {
		return fmt.Errorf("failed to uninstall workflow: %w", err)
	}

	// Clean up empty parent directory
	parentDir := filepath.Join(i.installDir, "workflows", id)
	entries, err := os.ReadDir(parentDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(parentDir)
	}

	return nil
}

// IsWorkflowInstalled checks if a workflow is installed.
func (i *Installer) IsWorkflowInstalled(id string, version string) bool {
	workflowDir := filepath.Join(i.installDir, "workflows", id, version)
	_, err := os.Stat(filepath.Join(workflowDir, "install.json"))
	return err == nil
}

// ListInstalledWorkflows lists all installed workflows.
func (i *Installer) ListInstalledWorkflows() ([]*WorkflowTemplate, error) {
	workflowsDir := filepath.Join(i.installDir, "workflows")
	if _, err := os.Stat(workflowsDir); os.IsNotExist(err) {
		return []*WorkflowTemplate{}, nil
	}

	var workflows []*WorkflowTemplate

	// Walk through workflow directories
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflows directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workflowID := entry.Name()
		versionEntries, err := os.ReadDir(filepath.Join(workflowsDir, workflowID))
		if err != nil {
			continue
		}

		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() {
				continue
			}

			record, err := i.loadInstallationRecord(filepath.Join(workflowsDir, workflowID, versionEntry.Name()))
			if err != nil {
				continue
			}

			if template, ok := record.Metadata.(*WorkflowTemplate); ok {
				workflows = append(workflows, template)
			}
		}
	}

	return workflows, nil
}

// CreateAgentFromInstalled creates an agent instance from installed metadata.
// The caller needs to provide the actual provider implementation.
func (i *Installer) CreateAgentFromInstalled(
	id string,
	version string,
	prov provider.Provider,
) (*orchestration.Agent, error) {
	if !i.IsAgentInstalled(id, version) {
		return nil, fmt.Errorf("agent %s@%s is not installed", id, version)
	}

	agentDir := filepath.Join(i.installDir, "agents", id, version)
	record, err := i.loadInstallationRecord(agentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load installation record: %w", err)
	}

	metadata, ok := record.Metadata.(*AgentMetadata)
	if !ok {
		return nil, fmt.Errorf("invalid metadata type")
	}

	// Create agent with provided provider
	agentInstance := &orchestration.Agent{
		ID:           metadata.ID,
		Name:         metadata.Name,
		Description:  metadata.Description,
		Provider:     prov,
		Model:        metadata.Model,
		SystemPrompt: metadata.Prompt.System,
		Temperature:  metadata.Config.Temperature,
		MaxTokens:    metadata.Config.MaxTokens,
		Tools:        []provider.Tool{}, // Tools can be added separately
	}

	return agentInstance, nil
}

// CreateWorkflowFromInstalled creates a workflow from installed template.
func (i *Installer) CreateWorkflowFromInstalled(
	id string,
	version string,
	agentMap map[string]*orchestration.Agent,
) (*orchestration.Workflow, error) {
	if !i.IsWorkflowInstalled(id, version) {
		return nil, fmt.Errorf("workflow %s@%s is not installed", id, version)
	}

	workflowDir := filepath.Join(i.installDir, "workflows", id, version)
	record, err := i.loadInstallationRecord(workflowDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load installation record: %w", err)
	}

	template, ok := record.Metadata.(*WorkflowTemplate)
	if !ok {
		return nil, fmt.Errorf("invalid metadata type")
	}

	// Create workflow
	workflow := orchestration.NewWorkflow(template.ID, template.Name, template.Description)

	// Add steps from template
	for _, stepTemplate := range template.Steps {
		agentInstance, ok := agentMap[stepTemplate.AgentID]
		if !ok {
			return nil, fmt.Errorf("agent %s not found in agent map", stepTemplate.AgentID)
		}

		switch stepTemplate.Type {
		case "sequential":
			step := &orchestration.SequentialStep{
				StepName: stepTemplate.Name,
				Agent:    agentInstance,
				InputFn: func(state map[string]any) string {
					// Default input function - can be overridden
					return fmt.Sprintf("%v", state)
				},
				OutputKey: stepTemplate.OutputKey,
			}
			workflow.AddStep(step)

		case "parallel":
			// For parallel steps, would need multiple agents
			// This is a simplified implementation
			continue

		case "conditional":
			// Conditional steps would need condition function
			continue
		}
	}

	return workflow, nil
}

// UpdateAgent updates an agent to a new version.
func (i *Installer) UpdateAgent(ctx context.Context, id string, newVersion string) error {
	// Get current versions
	agents, err := i.ListInstalledAgents()
	if err != nil {
		return fmt.Errorf("failed to list installed agents: %w", err)
	}

	var currentVersion string
	for _, a := range agents {
		if a.ID == id {
			currentVersion = a.Version
			break
		}
	}

	if currentVersion == "" {
		return fmt.Errorf("agent %s is not installed", id)
	}

	// Install new version
	if err := i.InstallAgent(ctx, id, newVersion); err != nil {
		return fmt.Errorf("failed to install new version: %w", err)
	}

	// Optionally uninstall old version
	// For now, keep both versions installed
	return nil
}

// UpdateWorkflow updates a workflow to a new version.
func (i *Installer) UpdateWorkflow(ctx context.Context, id string, newVersion string) error {
	// Get current versions
	workflows, err := i.ListInstalledWorkflows()
	if err != nil {
		return fmt.Errorf("failed to list installed workflows: %w", err)
	}

	var currentVersion string
	for _, w := range workflows {
		if w.ID == id {
			currentVersion = w.Version
			break
		}
	}

	if currentVersion == "" {
		return fmt.Errorf("workflow %s is not installed", id)
	}

	// Install new version
	if err := i.InstallWorkflow(ctx, id, newVersion); err != nil {
		return fmt.Errorf("failed to install new version: %w", err)
	}

	// Optionally uninstall old version
	// For now, keep both versions installed
	return nil
}

// saveInstallationRecord saves an installation record.
func (i *Installer) saveInstallationRecord(dir string, record *InstallationRecord) error {
	recordPath := filepath.Join(dir, "install.json")
	data, err := os.ReadFile(recordPath)
	if err == nil {
		// Record exists, update it
		var existing InstallationRecord
		if err := json.Unmarshal(data, &existing); err == nil {
			record.InstalledAt = existing.InstalledAt
		}
	}

	data, err = json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	if err := os.WriteFile(recordPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write record: %w", err)
	}

	return nil
}

// loadInstallationRecord loads an installation record.
func (i *Installer) loadInstallationRecord(dir string) (*InstallationRecord, error) {
	recordPath := filepath.Join(dir, "install.json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read record: %w", err)
	}

	// Two-pass decode: first pass captures Metadata as raw JSON, second pass
	// decodes it into the concrete type based on the Type field.
	var raw struct {
		Type        string          `json:"type"`
		ID          string          `json:"id"`
		Version     string          `json:"version"`
		InstalledAt time.Time       `json:"installed_at"`
		Metadata    json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal record: %w", err)
	}

	record := &InstallationRecord{
		Type:        raw.Type,
		ID:          raw.ID,
		Version:     raw.Version,
		InstalledAt: raw.InstalledAt,
	}

	switch raw.Type {
	case "agent":
		var m AgentMetadata
		if err := json.Unmarshal(raw.Metadata, &m); err != nil {
			return nil, fmt.Errorf("failed to unmarshal agent metadata: %w", err)
		}
		record.Metadata = &m
	case "workflow":
		var wf WorkflowTemplate
		if err := json.Unmarshal(raw.Metadata, &wf); err != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow metadata: %w", err)
		}
		record.Metadata = &wf
	default:
		return nil, fmt.Errorf("unknown installation record type %q", raw.Type)
	}

	return record, nil
}
