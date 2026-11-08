package marketplace

import (
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
)

// AgentCategory represents the category of an agent.
type AgentCategory string

const (
	CategoryDataAnalysis    AgentCategory = "data_analysis"
	CategoryCodeGeneration  AgentCategory = "code_generation"
	CategoryContentWriting  AgentCategory = "content_writing"
	CategoryResearch        AgentCategory = "research"
	CategoryCustomerService AgentCategory = "customer_service"
	CategoryDevOps          AgentCategory = "devops"
	CategoryGeneral         AgentCategory = "general"
)

// AgentMetadata describes a reusable agent.
type AgentMetadata struct {
	ID           string          `json:"id" yaml:"id"`
	Name         string          `json:"name" yaml:"name"`
	Version      string          `json:"version" yaml:"version"`
	Author       string          `json:"author" yaml:"author"`
	Description  string          `json:"description" yaml:"description"`
	Category     AgentCategory   `json:"category" yaml:"category"`
	Tags         []string        `json:"tags" yaml:"tags"`
	Provider     string          `json:"provider" yaml:"provider"` // claude, openai, etc.
	Model        string          `json:"model" yaml:"model"`
	Prompt       PromptTemplate  `json:"prompt" yaml:"prompt"`
	Tools        []ToolReference `json:"tools" yaml:"tools"`
	Config       AgentConfig     `json:"config" yaml:"config"`
	Dependencies []string        `json:"dependencies" yaml:"dependencies"`
	License      string          `json:"license" yaml:"license"`
	Downloads    int64           `json:"downloads" yaml:"downloads"`
	Rating       float64         `json:"rating" yaml:"rating"`
	CreatedAt    time.Time       `json:"created_at" yaml:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" yaml:"updated_at"`
}

// PromptTemplate defines the prompt structure for an agent.
type PromptTemplate struct {
	System    string               `json:"system" yaml:"system"`
	Variables []VariableDefinition `json:"variables" yaml:"variables"`
	Examples  []PromptExample      `json:"examples" yaml:"examples"`
}

// VariableDefinition defines a variable in the prompt template.
type VariableDefinition struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"` // string, number, boolean, array, object
	Required    bool   `json:"required" yaml:"required"`
	Default     any    `json:"default,omitempty" yaml:"default,omitempty"`
	Description string `json:"description" yaml:"description"`
}

// PromptExample provides example usage.
type PromptExample struct {
	Input       map[string]any `json:"input" yaml:"input"`
	Output      string         `json:"output" yaml:"output"`
	Description string         `json:"description" yaml:"description"`
}

// ToolReference references a tool.
type ToolReference struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

// AgentConfig contains agent configuration.
type AgentConfig struct {
	Temperature float64             `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	TopP        float64             `json:"top_p,omitempty" yaml:"top_p,omitempty"`
	TopK        int                 `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Streaming   bool                `json:"streaming,omitempty" yaml:"streaming,omitempty"`
	Metadata    map[string]string   `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	ToolChoice  provider.ToolChoice `json:"tool_choice,omitempty" yaml:"tool_choice,omitempty"`
}

// WorkflowCategory represents workflow category.
type WorkflowCategory string

const (
	WorkflowCategoryDataPipeline WorkflowCategory = "data_pipeline"
	WorkflowCategoryAnalysis     WorkflowCategory = "analysis"
	WorkflowCategoryAutomation   WorkflowCategory = "automation"
	WorkflowCategoryIntegration  WorkflowCategory = "integration"
	WorkflowCategoryGeneral      WorkflowCategory = "general"
)

// WorkflowTemplate describes a reusable workflow.
type WorkflowTemplate struct {
	ID          string               `json:"id" yaml:"id"`
	Name        string               `json:"name" yaml:"name"`
	Version     string               `json:"version" yaml:"version"`
	Author      string               `json:"author" yaml:"author"`
	Description string               `json:"description" yaml:"description"`
	Category    WorkflowCategory     `json:"category" yaml:"category"`
	Tags        []string             `json:"tags" yaml:"tags"`
	Steps       []StepTemplate       `json:"steps" yaml:"steps"`
	Variables   []VariableDefinition `json:"variables" yaml:"variables"`
	Outputs     []OutputDefinition   `json:"outputs" yaml:"outputs"`
	Example     WorkflowExample      `json:"example" yaml:"example"`
	License     string               `json:"license" yaml:"license"`
	Downloads   int64                `json:"downloads" yaml:"downloads"`
	Rating      float64              `json:"rating" yaml:"rating"`
	CreatedAt   time.Time            `json:"created_at" yaml:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at" yaml:"updated_at"`
}

// StepTemplate defines a step in workflow template.
type StepTemplate struct {
	Name      string         `json:"name" yaml:"name"`
	Type      string         `json:"type" yaml:"type"` // sequential, parallel, conditional
	AgentID   string         `json:"agent_id,omitempty" yaml:"agent_id,omitempty"`
	AgentRef  string         `json:"agent_ref,omitempty" yaml:"agent_ref,omitempty"` // agent-id@version
	InputMap  map[string]any `json:"input_map,omitempty" yaml:"input_map,omitempty"`
	OutputKey string         `json:"output_key,omitempty" yaml:"output_key,omitempty"`
}

// OutputDefinition defines workflow output.
type OutputDefinition struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Type        string `json:"type" yaml:"type"`
}

// WorkflowExample provides example usage.
type WorkflowExample struct {
	Input          map[string]any `json:"input" yaml:"input"`
	ExpectedOutput map[string]any `json:"expected_output" yaml:"expected_output"`
	Description    string         `json:"description" yaml:"description"`
}

// SearchQuery defines search parameters.
type SearchQuery struct {
	Text     string        `json:"text"`
	Category AgentCategory `json:"category,omitempty"`
	Tags     []string      `json:"tags,omitempty"`
	Provider string        `json:"provider,omitempty"`
	SortBy   SortCriteria  `json:"sort_by"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

// SortCriteria defines sort order.
type SortCriteria string

const (
	SortByDownloads SortCriteria = "downloads"
	SortByRating    SortCriteria = "rating"
	SortByUpdated   SortCriteria = "updated"
	SortByName      SortCriteria = "name"
)

// Rating represents agent/workflow rating.
type Rating struct {
	TargetID  string    `json:"target_id"` // agent or workflow ID
	UserID    string    `json:"user_id"`
	Score     float64   `json:"score"` // 1.0 - 5.0
	Comment   string    `json:"comment,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// InstallationRecord tracks installed agents and workflows.
type InstallationRecord struct {
	Type        string    `json:"type"` // "agent" or "workflow"
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installed_at"`
	Metadata    any       `json:"metadata"` // *AgentMetadata or *WorkflowTemplate
}
