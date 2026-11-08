# Agent Marketplace

The **marketplace** package provides a complete agent and workflow marketplace system for Plumego, enabling discovery, sharing, and reuse of AI agents and workflow templates.

## Features

- 📦 **Agent Registry**: Publish and discover reusable AI agents
- 🔄 **Workflow Templates**: Share complete workflow patterns
- 🔍 **Search & Discovery**: Find agents by category, tags, and ratings
- ⬇️ **Package Management**: Install agents with automatic dependency resolution
- ⭐ **Ratings & Reviews**: Community-driven quality feedback
- ✅ **Validation**: Ensure agent and workflow quality
- 📝 **Semantic Versioning**: Version management with SemVer

## Architecture

```
┌─────────────────┐
│    Manager      │  High-level API
├─────────────────┤
│  AgentRegistry  │  Agent storage & search
│ WorkflowRegistry│  Workflow storage & search
├─────────────────┤
│   Installer     │  Package management
│   Validator     │  Quality assurance
└─────────────────┘
```

## Quick Start

### Creating a Manager

```go
import "github.com/birdordev/plumego/x/ai/marketplace"

// Create with default paths (~/.plumego)
manager, err := marketplace.NewManager()
if err != nil {
    log.Fatal(err)
}

// Or with custom path
manager, err := marketplace.NewManagerWithPath("/path/to/marketplace")
```

### Publishing an Agent

```go
agent := &marketplace.AgentMetadata{
    ID:          "data-analyzer",
    Name:        "Data Analyzer",
    Version:     "1.0.0",
    Author:      "Your Name",
    Description: "An agent that analyzes structured data and provides insights",
    Category:    marketplace.CategoryDataAnalysis,
    Tags:        []string{"data", "analysis", "insights"},
    Provider:    "claude",
    Model:       "claude-3-5-sonnet-20241022",
    Prompt: marketplace.PromptTemplate{
        System: "You are an expert data analyst...",
        Variables: []marketplace.VariableDefinition{
            {
                Name:     "data",
                Type:     "object",
                Required: true,
                Description: "The data to analyze",
            },
        },
    },
    Config: marketplace.AgentConfig{
        Temperature: 0.7,
        MaxTokens:   2000,
        TopP:        0.9,
    },
    License: "MIT",
}

err := manager.PublishAgent(context.Background(), agent)
```

### Searching for Agents

```go
// Search by category
results, err := manager.SearchAgents(ctx, marketplace.SearchQuery{
    Category: marketplace.CategoryDataAnalysis,
    Limit:    10,
})

// Search by tags
results, err := manager.SearchAgents(ctx, marketplace.SearchQuery{
    Tags: []string{"data", "analysis"},
})

// Text search
results, err := manager.SearchAgents(ctx, marketplace.SearchQuery{
    Text: "analyze data",
})

// Combined search
results, err := manager.SearchAgents(ctx, marketplace.SearchQuery{
    Text:     "code",
    Category: marketplace.CategoryCodeGeneration,
    Provider: "claude",
    Limit:    20,
})
```

### Installing Agents

```go
// Install an agent (with automatic dependency resolution)
err := manager.InstallAgent(ctx, "data-analyzer", "1.0.0")

// Check if installed
if manager.IsAgentInstalled("data-analyzer", "1.0.0") {
    fmt.Println("Agent is installed")
}

// List installed agents
installed, err := manager.ListInstalledAgents()
for _, agent := range installed {
    fmt.Printf("%s v%s\n", agent.Name, agent.Version)
}

// Uninstall
err := manager.UninstallAgent(ctx, "data-analyzer", "1.0.0")
```

### Creating Agent from Installed Metadata

```go
// Get a provider (from your provider registry)
claudeProvider := provider.NewClaudeProvider(apiKey)

// Create agent instance
agentInstance, err := manager.Installer().CreateAgentFromInstalled(
    "data-analyzer",
    "1.0.0",
    claudeProvider,
)

// Use the agent
result, err := agentInstance.Execute(ctx, "Analyze this data...")
```

### Working with Workflows

```go
// Publish a workflow template
template := &marketplace.WorkflowTemplate{
    ID:          "data-pipeline",
    Name:        "Data Analysis Pipeline",
    Version:     "1.0.0",
    Author:      "Your Name",
    Description: "A complete pipeline for data ingestion, analysis, and reporting",
    Category:    marketplace.WorkflowCategoryDataPipeline,
    Tags:        []string{"data", "pipeline", "etl"},
    Steps: []marketplace.StepTemplate{
        {
            Name:      "ingest",
            Type:      "sequential",
            AgentRef:  "data-ingestor@1.0.0",
            OutputKey: "raw_data",
        },
        {
            Name:      "analyze",
            Type:      "sequential",
            AgentRef:  "data-analyzer@1.0.0",
            OutputKey: "analysis",
        },
        {
            Name:      "report",
            Type:      "sequential",
            AgentRef:  "report-generator@1.0.0",
            OutputKey: "report",
        },
    },
    Variables: []marketplace.VariableDefinition{
        {Name: "source", Type: "string", Required: true},
    },
    Outputs: []marketplace.OutputDefinition{
        {Name: "report", Type: "string", Description: "Final report"},
    },
    License: "MIT",
}

err := manager.PublishWorkflow(ctx, template)

// Install workflow
err = manager.InstallWorkflow(ctx, "data-pipeline", "1.0.0")

// Create workflow from template
agentMap := map[string]*orchestration.Agent{
    "data-ingestor":    ingestorAgent,
    "data-analyzer":    analyzerAgent,
    "report-generator": reporterAgent,
}

workflow, err := manager.Installer().CreateWorkflowFromInstalled(
    "data-pipeline",
    "1.0.0",
    agentMap,
)
```

### Rating Agents

```go
// Add a rating
rating := &marketplace.Rating{
    TargetID:  "data-analyzer",
    UserID:    "user-123",
    Score:     4.5,
    Comment:   "Excellent agent for data analysis!",
    CreatedAt: time.Now(),
}

err := manager.RateAgent(ctx, rating)

// Get ratings
ratings, err := manager.GetAgentRatings(ctx, "data-analyzer")
for _, r := range ratings {
    fmt.Printf("User %s rated %.1f: %s\n", r.UserID, r.Score, r.Comment)
}
```

### Dependency Management

```go
// Agents can depend on other agents
dependentAgent := &marketplace.AgentMetadata{
    ID:           "advanced-analyzer",
    Name:         "Advanced Data Analyzer",
    Version:      "1.0.0",
    Dependencies: []string{
        "data-analyzer@1.0.0",
        "data-validator@1.2.3",
    },
    // ... other fields
}

// When installing, dependencies are resolved automatically
err := manager.InstallAgent(ctx, "advanced-analyzer", "1.0.0")
// This will install data-analyzer and data-validator first
```

### Version Management

```go
// List available versions
versions, err := manager.ListAgentVersions(ctx, "data-analyzer")
fmt.Println("Available versions:", versions)
// Output: [1.0.0, 1.1.0, 2.0.0]

// Get specific version
agent, err := manager.GetAgent(ctx, "data-analyzer", "1.0.0")

// Update to newer version
err = manager.UpdateAgent(ctx, "data-analyzer", "2.0.0")
```

## Agent Categories

- `CategoryDataAnalysis`: Data analysis and insights
- `CategoryCodeGeneration`: Code generation and programming
- `CategoryContentWriting`: Content creation and writing
- `CategoryResearch`: Research and information gathering
- `CategoryCustomerService`: Customer support and service
- `CategoryDevOps`: DevOps and system operations
- `CategoryGeneral`: General-purpose agents

## Workflow Categories

- `WorkflowCategoryDataPipeline`: Data pipelines and ETL
- `WorkflowCategoryAnalysis`: Analysis workflows
- `WorkflowCategoryAutomation`: Automation workflows
- `WorkflowCategoryIntegration`: Integration workflows
- `WorkflowCategoryGeneral`: General-purpose workflows

## Validation

The marketplace automatically validates agents and workflows before publishing:

```go
validator := marketplace.NewValidator()

// Validate agent
result := validator.ValidateAgent(agentMetadata)
if !result.IsValid() {
    for _, err := range result.Errors {
        fmt.Printf("Field %s: %s\n", err.Field, err.Message)
    }
}

// Validate workflow
result = validator.ValidateWorkflow(workflowTemplate)

// Validate rating
result = validator.ValidateRating(rating)
```

### Validation Rules

**Agent Metadata:**
- ID must be lowercase alphanumeric with hyphens (3-50 chars)
- Version must be valid semantic version
- Description must be 20-500 characters
- Provider must be in allowed list (claude, openai, anthropic)
- Category must be valid
- Temperature must be 0-2
- MaxTokens must be 0-100,000
- TopP must be 0-1

**Workflow Template:**
- ID must be lowercase alphanumeric with hyphens
- Version must be valid semantic version
- At least one step required
- Step types: sequential, parallel, conditional
- Agent references must be in format "agent-id@version"

**Rating:**
- Score must be 1.0-5.0
- TargetID and UserID required
- Comment max 1000 characters

## Storage Structure

The marketplace uses a file-based storage structure:

```
~/.plumego/
├── agents/                     # Agent registry
│   └── data-analyzer/
│       ├── 1.0.0/
│       │   └── metadata.json
│       ├── 1.1.0/
│       │   └── metadata.json
│       └── ratings/
│           ├── user1.json
│           └── user2.json
├── workflows/                  # Workflow registry
│   └── data-pipeline/
│       └── 1.0.0/
│           └── template.json
└── installed/                  # Installed packages
    ├── agents/
    │   └── data-analyzer/
    │       └── 1.0.0/
    │           └── install.json
    └── workflows/
        └── data-pipeline/
            └── 1.0.0/
                └── install.json
```

## Advanced Usage

### Direct Registry Access

```go
// Access registries directly for advanced operations
agentRegistry := manager.AgentRegistry()
workflowRegistry := manager.WorkflowRegistry()

// Resolve dependencies manually
deps, err := agentRegistry.ResolveDependencies(ctx, "agent-id", "1.0.0")

// Update download count
err = agentRegistry.UpdateDownloadCount(ctx, "agent-id")
```

### Custom Registries

Implement custom registries for different storage backends:

```go
type CustomAgentRegistry struct {
    // Your implementation
}

func (r *CustomAgentRegistry) Publish(ctx context.Context, metadata *AgentMetadata) error {
    // Store in database, S3, etc.
}

// Implement all AgentRegistry interface methods
```

## Best Practices

1. **Versioning**: Use semantic versioning (major.minor.patch)
2. **Dependencies**: Pin exact versions in dependencies
3. **Testing**: Test agents before publishing
4. **Documentation**: Provide clear descriptions and examples
5. **Categories**: Choose appropriate categories and tags
6. **Licensing**: Always specify a license
7. **Security**: Don't include API keys or secrets in metadata

## Error Handling

```go
import "errors"

// Check error types
if errors.Is(err, marketplace.ErrAgentNotFound) {
    // Handle not found
}

if errors.Is(err, marketplace.ErrDuplicateAgent) {
    // Handle duplicate
}

// Validation errors
if valErr, ok := err.(*marketplace.ValidationError); ok {
    fmt.Printf("Validation failed for field %s: %s\n",
        valErr.Field, valErr.Message)
}
```

## Complete Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/birdordev/plumego/x/ai/marketplace"
    "github.com/birdordev/plumego/x/ai/provider"
)

func main() {
    ctx := context.Background()

    // Create manager
    manager, err := marketplace.NewManager()
    if err != nil {
        log.Fatal(err)
    }

    // 1. Publish an agent
    agent := &marketplace.AgentMetadata{
        ID:          "code-reviewer",
        Name:        "Code Reviewer",
        Version:     "1.0.0",
        Author:      "Engineering Team",
        Description: "An AI agent that reviews code for quality, bugs, and best practices",
        Category:    marketplace.CategoryCodeGeneration,
        Tags:        []string{"code", "review", "quality"},
        Provider:    "claude",
        Model:       "claude-3-5-sonnet-20241022",
        Prompt: marketplace.PromptTemplate{
            System: "You are an expert code reviewer...",
        },
        Config: marketplace.AgentConfig{
            Temperature: 0.3,
            MaxTokens:   4000,
        },
        License:   "MIT",
        CreatedAt: time.Now(),
        UpdatedAt: time.Now(),
    }

    err = manager.PublishAgent(ctx, agent)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("✓ Agent published")

    // 2. Search for agents
    results, err := manager.SearchAgents(ctx, marketplace.SearchQuery{
        Category: marketplace.CategoryCodeGeneration,
        Limit:    10,
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Found %d code generation agents\n", len(results))

    // 3. Install the agent
    err = manager.InstallAgent(ctx, "code-reviewer", "1.0.0")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("✓ Agent installed")

    // 4. Create agent instance
    claudeProvider := provider.NewClaudeProvider("your-api-key")
    agentInstance, err := manager.Installer().CreateAgentFromInstalled(
        "code-reviewer",
        "1.0.0",
        claudeProvider,
    )
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("✓ Agent ready: %s\n", agentInstance.Name)

    // 5. Rate the agent
    rating := &marketplace.Rating{
        TargetID:  "code-reviewer",
        UserID:    "user-123",
        Score:     5.0,
        Comment:   "Excellent code reviews!",
        CreatedAt: time.Now(),
    }
    err = manager.RateAgent(ctx, rating)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("✓ Rating submitted")
}
```

## Testing

Run the marketplace tests:

```bash
go test ./x/ai/marketplace -v
```

## Future Enhancements

- Remote marketplace server (REST API)
- Package signing and verification
- Usage analytics
- Automated testing framework
- Community curation
- Marketplace web UI
