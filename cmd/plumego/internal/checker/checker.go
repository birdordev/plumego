package checker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/configmgr"
	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
)

// CheckResult represents the overall health check result
type CheckResult struct {
	Status string                 `json:"status" yaml:"status"` // healthy, degraded, unhealthy
	Checks map[string]CheckDetail `json:"checks" yaml:"checks"`
}

// CheckDetail represents a single check result
type CheckDetail struct {
	Status   string       `json:"status" yaml:"status"` // passed, warning, failed
	Issues   []CheckIssue `json:"issues" yaml:"issues"`
	Outdated []string     `json:"outdated,omitempty" yaml:"outdated,omitempty"`
}

// CheckIssue represents a specific issue found
type CheckIssue struct {
	Severity string `json:"severity" yaml:"severity"` // low, medium, high, critical
	Message  string `json:"message" yaml:"message"`
	Fix      string `json:"fix,omitempty" yaml:"fix,omitempty"`
}

// DependencyOptions controls dependency diagnostics.
type DependencyOptions struct {
	CheckUpdates bool
}

// CheckConfig validates configuration files and environment
func CheckConfig(dir, envFile string) CheckDetail {
	detail := CheckDetail{
		Status: "passed",
		Issues: []CheckIssue{},
	}

	// Check if go.mod exists
	goModPath := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		detail.Status = "failed"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "high",
			Message:  "go.mod not found",
			Fix:      "Run 'go mod init <module-path>' to initialize Go module",
		})
	}

	// Check if env.example exists
	envExamplePath := filepath.Join(dir, "env.example")
	if _, err := os.Stat(envExamplePath); os.IsNotExist(err) {
		detail.Status = "warning"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "low",
			Message:  "env.example not found",
			Fix:      "Create env.example file to document environment variables",
		})
	}

	// Check if .env exists (optional)
	envPath := filepath.Join(dir, envFile)
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "low",
			Message:  fmt.Sprintf("%s not found (optional)", envFile),
			Fix:      fmt.Sprintf("Copy env.example to %s and configure", envFile),
		})
	}

	return detail
}

// CheckDependencies validates Go dependencies.
func CheckDependencies(dir string, options ...DependencyOptions) CheckDetail {
	opts := DependencyOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	return checkDependencies(dir, opts, executil.Run)
}

type dependencyCommandRunner func(context.Context, executil.Options) (executil.Result, error)

func checkDependencies(dir string, opts DependencyOptions, run dependencyCommandRunner) CheckDetail {
	detail := CheckDetail{
		Status: "passed",
		Issues: []CheckIssue{},
	}

	// Check if go.mod exists
	goModPath := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		detail.Status = "failed"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "high",
			Message:  "go.mod not found",
			Fix:      "Run 'go mod init <module-path>' to initialize Go module",
		})
		return detail
	}

	// Run go mod verify
	result, err := run(context.Background(), executil.Options{
		Name:    "go",
		Args:    []string{"mod", "verify"},
		Dir:     dir,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		detail.Status = "failed"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "high",
			Message:  fmt.Sprintf("go mod verify failed: %s", strings.TrimSpace(result.CombinedOutput())),
			Fix:      "Run 'go mod tidy' to fix dependencies",
		})
	}

	if !opts.CheckUpdates {
		return detail
	}

	// Check for outdated dependencies (list packages that could be updated)
	result, err = run(context.Background(), executil.Options{
		Name:    "go",
		Args:    []string{"list", "-u", "-m", "all"},
		Dir:     dir,
		Timeout: 30 * time.Second,
	})
	if err == nil {
		lines := strings.Split(result.CombinedOutput(), "\n")
		outdated := []string{}
		for _, line := range lines {
			if strings.Contains(line, "[") && strings.Contains(line, "]") {
				// Format: module version [available]
				outdated = append(outdated, strings.TrimSpace(line))
			}
		}
		if len(outdated) > 0 {
			detail.Status = "warning"
			detail.Outdated = outdated
			detail.Issues = append(detail.Issues, CheckIssue{
				Severity: "low",
				Message:  fmt.Sprintf("%d dependencies have updates available", len(outdated)),
				Fix:      "Run 'go get -u <module>' to update specific modules",
			})
		}
	}

	return detail
}

// CheckSecurity runs security checks
func CheckSecurity(dir, envFile string) CheckDetail {
	detail := CheckDetail{
		Status: "passed",
		Issues: []CheckIssue{},
	}

	envPath := filepath.Join(dir, envFile)
	envVars, err := configmgr.ParseEnvFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		detail.Status = "failed"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "high",
			Message:  fmt.Sprintf("failed to parse %s: %v", envFile, err),
			Fix:      fmt.Sprintf("Fix %s syntax or remove invalid lines", envFile),
		})
		return detail
	}
	if envVars == nil {
		envVars = make(map[string]string)
	}

	// Check environment variables
	for _, secret := range configmgr.RequiredSecrets() {
		_, hasEnvVar := envVars[secret]
		hasSystemEnv := os.Getenv(secret) != ""

		if !hasEnvVar && !hasSystemEnv {
			detail.Status = "warning"
			detail.Issues = append(detail.Issues, CheckIssue{
				Severity: "medium",
				Message:  fmt.Sprintf("%s not set in environment or %s", secret, envFile),
				Fix:      fmt.Sprintf("Set %s to a secure random string (32+ bytes)", secret),
			})
		}
	}

	// Check for .env in git
	gitignorePath := filepath.Join(dir, ".gitignore")
	if file, err := os.Open(gitignorePath); err == nil {
		defer file.Close()
		hasEnvIgnore := false
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == ".env" || line == ".env*" || strings.HasPrefix(line, ".env") {
				hasEnvIgnore = true
				break
			}
		}

		if !hasEnvIgnore {
			detail.Status = "warning"
			detail.Issues = append(detail.Issues, CheckIssue{
				Severity: "high",
				Message:  ".env files not in .gitignore",
				Fix:      "Add '.env' to .gitignore to prevent committing secrets",
			})
		}
	}

	// Check for common security issues in code
	mainGoPath := filepath.Join(dir, "main.go")
	if file, err := os.Open(mainGoPath); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			// Check for hardcoded secrets (basic check)
			if strings.Contains(line, `secret := "`) ||
				strings.Contains(line, `password := "`) ||
				strings.Contains(line, `key := "`) {
				detail.Status = "warning"
				detail.Issues = append(detail.Issues, CheckIssue{
					Severity: "high",
					Message:  fmt.Sprintf("Possible hardcoded secret in main.go:%d", lineNum),
					Fix:      "Use environment variables for secrets",
				})
			}
		}
	}

	return detail
}

// CheckStructure validates project structure
func CheckStructure(dir string) CheckDetail {
	detail := CheckDetail{
		Status: "passed",
		Issues: []CheckIssue{},
	}

	// Check for a canonical application entrypoint.
	rootMainPath := filepath.Join(dir, "main.go")
	cmdAppMainPath := filepath.Join(dir, "cmd", "app", "main.go")
	if !fileExists(rootMainPath) && !fileExists(cmdAppMainPath) {
		detail.Status = "warning"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "medium",
			Message:  "application entrypoint not found",
			Fix:      "Create main.go or cmd/app/main.go",
		})
	}

	// Check for README
	readmePath := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "low",
			Message:  "README.md not found",
			Fix:      "Create README.md to document the project",
		})
	}

	// Check for .gitignore
	gitignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		detail.Status = "warning"
		detail.Issues = append(detail.Issues, CheckIssue{
			Severity: "medium",
			Message:  ".gitignore not found",
			Fix:      "Create .gitignore to exclude build artifacts and secrets",
		})
	}

	return detail
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
