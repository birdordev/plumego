package marketplace

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/birdordev/plumego/internal/semver"
)

// Validator validates agent metadata and workflow templates.
type Validator struct {
	// Configuration
	allowedProviders          map[string]bool
	allowedCategories         map[AgentCategory]bool
	allowedWorkflowCategories map[WorkflowCategory]bool
}

// NewValidator creates a new validator.
func NewValidator() *Validator {
	return &Validator{
		allowedProviders: map[string]bool{
			"claude":    true,
			"openai":    true,
			"anthropic": true,
		},
		allowedCategories: map[AgentCategory]bool{
			CategoryDataAnalysis:    true,
			CategoryCodeGeneration:  true,
			CategoryContentWriting:  true,
			CategoryResearch:        true,
			CategoryCustomerService: true,
			CategoryDevOps:          true,
			CategoryGeneral:         true,
		},
		allowedWorkflowCategories: map[WorkflowCategory]bool{
			WorkflowCategoryDataPipeline: true,
			WorkflowCategoryAnalysis:     true,
			WorkflowCategoryAutomation:   true,
			WorkflowCategoryIntegration:  true,
			WorkflowCategoryGeneral:      true,
		},
	}
}

// validationError represents a single field validation error.
type validationError struct {
	field   string
	message string
}

func (e *validationError) Error() string {
	return fmt.Sprintf("validation error in field '%s': %s", e.field, e.message)
}

// ValidationResult contains validation errors.
type ValidationResult struct {
	Errors []validationError
}

// IsValid returns true if there are no errors.
func (r *ValidationResult) IsValid() bool {
	return len(r.Errors) == 0
}

// AddError adds a validation error.
func (r *ValidationResult) AddError(field, message string) {
	r.Errors = append(r.Errors, validationError{
		field:   field,
		message: message,
	})
}

// ValidateAgent validates agent metadata.
func (v *Validator) ValidateAgent(metadata *AgentMetadata) *ValidationResult {
	result := &ValidationResult{}

	// Validate ID
	if metadata.ID == "" {
		result.AddError("id", "ID is required")
	} else if !isValidID(metadata.ID) {
		result.AddError("id", "ID must be lowercase alphanumeric with hyphens (e.g., my-agent)")
	}

	// Validate Name
	if metadata.Name == "" {
		result.AddError("name", "Name is required")
	} else if len(metadata.Name) > 100 {
		result.AddError("name", "Name must be 100 characters or less")
	}

	// Validate Version
	if metadata.Version == "" {
		result.AddError("version", "Version is required")
	} else {
		if _, err := semver.Parse(metadata.Version); err != nil {
			result.AddError("version", fmt.Sprintf("Invalid semantic version: %v", err))
		}
	}

	// Validate Author
	if metadata.Author == "" {
		result.AddError("author", "Author is required")
	}

	// Validate Description
	if metadata.Description == "" {
		result.AddError("description", "Description is required")
	} else if len(metadata.Description) < 20 {
		result.AddError("description", "Description must be at least 20 characters")
	} else if len(metadata.Description) > 500 {
		result.AddError("description", "Description must be 500 characters or less")
	}

	// Validate Category
	if metadata.Category == "" {
		result.AddError("category", "Category is required")
	} else if !v.allowedCategories[metadata.Category] {
		result.AddError("category", fmt.Sprintf("Invalid category: %s", metadata.Category))
	}

	// Validate Provider
	if metadata.Provider == "" {
		result.AddError("provider", "Provider is required")
	} else if !v.allowedProviders[metadata.Provider] {
		result.AddError("provider", fmt.Sprintf("Invalid provider: %s", metadata.Provider))
	}

	// Validate Model
	if metadata.Model == "" {
		result.AddError("model", "Model is required")
	}

	// Validate Prompt
	if metadata.Prompt.System == "" {
		result.AddError("prompt.system", "System prompt is required")
	} else if len(metadata.Prompt.System) > 10000 {
		result.AddError("prompt.system", "System prompt must be 10000 characters or less")
	}

	// Validate Dependencies
	for i, dep := range metadata.Dependencies {
		if !isValidDependency(dep) {
			result.AddError(fmt.Sprintf("dependencies[%d]", i),
				"Dependency must be in format 'agent-id@version'")
		}
	}

	// Validate Config
	if metadata.Config.Temperature < 0 || metadata.Config.Temperature > 2 {
		result.AddError("config.temperature", "Temperature must be between 0 and 2")
	}
	if metadata.Config.MaxTokens < 0 || metadata.Config.MaxTokens > 100000 {
		result.AddError("config.max_tokens", "MaxTokens must be between 0 and 100000")
	}
	if metadata.Config.TopP < 0 || metadata.Config.TopP > 1 {
		result.AddError("config.top_p", "TopP must be between 0 and 1")
	}

	// Validate License
	if metadata.License == "" {
		result.AddError("license", "License is required (e.g., MIT, Apache-2.0)")
	}

	return result
}

// ValidateWorkflow validates workflow template.
func (v *Validator) ValidateWorkflow(template *WorkflowTemplate) *ValidationResult {
	result := &ValidationResult{}

	// Validate ID
	if template.ID == "" {
		result.AddError("id", "ID is required")
	} else if !isValidID(template.ID) {
		result.AddError("id", "ID must be lowercase alphanumeric with hyphens (e.g., my-workflow)")
	}

	// Validate Name
	if template.Name == "" {
		result.AddError("name", "Name is required")
	} else if len(template.Name) > 100 {
		result.AddError("name", "Name must be 100 characters or less")
	}

	// Validate Version
	if template.Version == "" {
		result.AddError("version", "Version is required")
	} else {
		if _, err := semver.Parse(template.Version); err != nil {
			result.AddError("version", fmt.Sprintf("Invalid semantic version: %v", err))
		}
	}

	// Validate Author
	if template.Author == "" {
		result.AddError("author", "Author is required")
	}

	// Validate Description
	if template.Description == "" {
		result.AddError("description", "Description is required")
	} else if len(template.Description) < 20 {
		result.AddError("description", "Description must be at least 20 characters")
	} else if len(template.Description) > 500 {
		result.AddError("description", "Description must be 500 characters or less")
	}

	// Validate Category
	if template.Category == "" {
		result.AddError("category", "Category is required")
	} else if !v.allowedWorkflowCategories[template.Category] {
		result.AddError("category", fmt.Sprintf("Invalid category: %s", template.Category))
	}

	// Validate Steps
	if len(template.Steps) == 0 {
		result.AddError("steps", "At least one step is required")
	}

	for i, step := range template.Steps {
		if step.Name == "" {
			result.AddError(fmt.Sprintf("steps[%d].name", i), "Step name is required")
		}
		if step.Type == "" {
			result.AddError(fmt.Sprintf("steps[%d].type", i), "Step type is required")
		} else if step.Type != "sequential" && step.Type != "parallel" && step.Type != "conditional" {
			result.AddError(fmt.Sprintf("steps[%d].type", i),
				"Step type must be 'sequential', 'parallel', or 'conditional'")
		}

		// For agent steps, validate agent reference
		if step.Type == "sequential" || step.Type == "conditional" {
			if step.AgentRef == "" && step.AgentID == "" {
				result.AddError(fmt.Sprintf("steps[%d].agent_ref", i),
					"Agent reference is required for sequential/conditional steps")
			}
			if step.AgentRef != "" && !isValidDependency(step.AgentRef) {
				result.AddError(fmt.Sprintf("steps[%d].agent_ref", i),
					"Agent reference must be in format 'agent-id@version'")
			}
		}
	}

	// Validate Variables
	for i, variable := range template.Variables {
		if variable.Name == "" {
			result.AddError(fmt.Sprintf("variables[%d].name", i), "Variable name is required")
		}
		if variable.Type == "" {
			result.AddError(fmt.Sprintf("variables[%d].type", i), "Variable type is required")
		}
	}

	// Validate Outputs
	for i, output := range template.Outputs {
		if output.Name == "" {
			result.AddError(fmt.Sprintf("outputs[%d].name", i), "Output name is required")
		}
		if output.Type == "" {
			result.AddError(fmt.Sprintf("outputs[%d].type", i), "Output type is required")
		}
	}

	// Validate License
	if template.License == "" {
		result.AddError("license", "License is required (e.g., MIT, Apache-2.0)")
	}

	return result
}

// ValidateRating validates a rating.
func (v *Validator) ValidateRating(rating *Rating) *ValidationResult {
	result := &ValidationResult{}

	if rating.TargetID == "" {
		result.AddError("target_id", "Target ID is required")
	}
	if rating.UserID == "" {
		result.AddError("user_id", "User ID is required")
	}
	if rating.Score < 1.0 || rating.Score > 5.0 {
		result.AddError("score", "Score must be between 1.0 and 5.0")
	}
	if len(rating.Comment) > 1000 {
		result.AddError("comment", "Comment must be 1000 characters or less")
	}

	return result
}

// isValidID checks if an ID is valid (lowercase alphanumeric with hyphens).
func isValidID(id string) bool {
	// Must be lowercase alphanumeric with hyphens, start with letter
	pattern := `^[a-z][a-z0-9-]*$`
	matched, _ := regexp.MatchString(pattern, id)
	return matched && len(id) >= 3 && len(id) <= 50
}

// isValidDependency checks if a dependency string is valid.
func isValidDependency(dep string) bool {
	parts := strings.Split(dep, "@")
	if len(parts) != 2 {
		return false
	}

	id, ver := parts[0], parts[1]
	if !isValidID(id) {
		return false
	}

	_, err := semver.Parse(ver)
	return err == nil
}

// QuickValidate performs quick validation and returns error if invalid.
func (v *Validator) QuickValidate(data any) error {
	var result *ValidationResult

	switch d := data.(type) {
	case *AgentMetadata:
		result = v.ValidateAgent(d)
	case *WorkflowTemplate:
		result = v.ValidateWorkflow(d)
	case *Rating:
		result = v.ValidateRating(d)
	default:
		return fmt.Errorf("unsupported validation type")
	}

	if !result.IsValid() {
		// Return first error
		return &result.Errors[0]
	}

	return nil
}
