package prompt

import (
	"context"
	"fmt"
)

var (
	// codeAssistTemplate provides a general-purpose coding assistant prompt.
	codeAssistTemplate = &Template{
		Name:    "code-assistant",
		Version: "v1.0.0",
		Content: `You are a helpful coding assistant.
User: {{.UserName | default "User"}}
Language: {{.Language | default "Go"}}
Task: {{.Task}}

Please provide clear, concise, and well-documented code.`,
		Variables: []Variable{
			{Name: "UserName", Type: "string", Required: false},
			{Name: "Language", Type: "string", Required: false, Default: "Go"},
			{Name: "Task", Type: "string", Required: true},
		},
		Model:       "claude-3-sonnet-20240229",
		Temperature: 0.7,
		MaxTokens:   4096,
		Tags:        []string{"coding", "assistant"},
	}

	// codeReviewTemplate performs structured code review.
	codeReviewTemplate = &Template{
		Name:    "code-review",
		Version: "v1.0.0",
		Content: `You are an expert code reviewer.

Review the following {{.Language}} code and provide:
1. Potential bugs or issues
2. Performance improvements
3. Best practice suggestions
4. Security concerns

Code:
{{.Code}}`,
		Variables: []Variable{
			{Name: "Language", Type: "string", Required: true},
			{Name: "Code", Type: "string", Required: true},
		},
		Model:       "claude-3-opus-20240229",
		Temperature: 0.3,
		MaxTokens:   2048,
		Tags:        []string{"code-review", "analysis"},
	}

	// summarizerTemplate summarizes text to a configurable word limit.
	summarizerTemplate = &Template{
		Name:    "summarizer",
		Version: "v1.0.0",
		Content: `Summarize the following text in {{.MaxWords | default "100"}} words or less:

{{.Text}}

Summary:`,
		Variables: []Variable{
			{Name: "Text", Type: "string", Required: true},
			{Name: "MaxWords", Type: "int", Required: false, Default: 100},
		},
		Model:       "claude-3-haiku-20240307",
		Temperature: 0.5,
		MaxTokens:   500,
		Tags:        []string{"summarization", "text-processing"},
	}

	// translatorTemplate translates text between two languages.
	translatorTemplate = &Template{
		Name:    "translator",
		Version: "v1.0.0",
		Content: `Translate the following text from {{.SourceLang}} to {{.TargetLang}}:

{{.Text}}

Translation:`,
		Variables: []Variable{
			{Name: "SourceLang", Type: "string", Required: true},
			{Name: "TargetLang", Type: "string", Required: true},
			{Name: "Text", Type: "string", Required: true},
		},
		Model:       "claude-3-sonnet-20240229",
		Temperature: 0.3,
		MaxTokens:   2048,
		Tags:        []string{"translation", "language"},
	}

	// bugReporterTemplate generates a structured bug report.
	bugReporterTemplate = &Template{
		Name:    "bug-reporter",
		Version: "v1.0.0",
		Content: `Generate a detailed bug report for the following issue:

Application: {{.AppName}}
Environment: {{.Environment | default "production"}}
Error Message: {{.ErrorMessage}}
Steps to Reproduce: {{.Steps}}

Please include:
- Issue description
- Expected behavior
- Actual behavior
- Potential causes
- Suggested fixes`,
		Variables: []Variable{
			{Name: "AppName", Type: "string", Required: true},
			{Name: "Environment", Type: "string", Required: false, Default: "production"},
			{Name: "ErrorMessage", Type: "string", Required: true},
			{Name: "Steps", Type: "string", Required: true},
		},
		Model:       "claude-3-sonnet-20240229",
		Temperature: 0.5,
		MaxTokens:   2048,
		Tags:        []string{"debugging", "reporting"},
	}

	// apiDocumenterTemplate generates API documentation from code.
	apiDocumenterTemplate = &Template{
		Name:    "api-documenter",
		Version: "v1.0.0",
		Content: `Generate API documentation for the following {{.Language}} function:

{{.Code}}

Include:
- Function description
- Parameters
- Return value
- Example usage
- Error cases`,
		Variables: []Variable{
			{Name: "Language", Type: "string", Required: true},
			{Name: "Code", Type: "string", Required: true},
		},
		Model:       "claude-3-sonnet-20240229",
		Temperature: 0.4,
		MaxTokens:   1500,
		Tags:        []string{"documentation", "api"},
	}

	// testGeneratorTemplate generates unit tests for source code.
	testGeneratorTemplate = &Template{
		Name:    "test-generator",
		Version: "v1.0.0",
		Content: `Generate unit tests for the following {{.Language}} code:

{{.Code}}

Generate tests that cover:
- Normal cases
- Edge cases
- Error cases
- Boundary conditions

Use {{.TestFramework | default "standard library"}} testing framework.`,
		Variables: []Variable{
			{Name: "Language", Type: "string", Required: true},
			{Name: "Code", Type: "string", Required: true},
			{Name: "TestFramework", Type: "string", Required: false},
		},
		Model:       "claude-3-sonnet-20240229",
		Temperature: 0.6,
		MaxTokens:   3000,
		Tags:        []string{"testing", "code-generation"},
	}
)

// BuiltinTemplates returns commonly used prompt templates.
func BuiltinTemplates() []*Template {
	return []*Template{
		codeAssistTemplate,
		codeReviewTemplate,
		summarizerTemplate,
		translatorTemplate,
		bugReporterTemplate,
		apiDocumenterTemplate,
		testGeneratorTemplate,
	}
}

// LoadBuiltinTemplates loads builtin templates into an engine.
//
// The context controls template registration; cancel it to abort a long load.
func LoadBuiltinTemplates(ctx context.Context, engine *Engine) error {
	templates := BuiltinTemplates()

	for _, tmpl := range templates {
		if err := engine.Register(ctx, tmpl); err != nil {
			return fmt.Errorf("load template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}
