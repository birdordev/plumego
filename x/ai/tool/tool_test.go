package tool

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
)

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	tool := NewEchoTool()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Try to register same tool again
	if err := registry.Register(tool); err == nil {
		t.Error("Register() should error when registering duplicate tool")
	}
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry()
	echoTool := NewEchoTool()

	registry.Register(echoTool)

	tool, err := registry.Get("echo")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if tool.Name() != "echo" {
		t.Errorf("Tool name = %v, want echo", tool.Name())
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.Get("nonexistent")
	if err == nil {
		t.Error("Get() should error for nonexistent tool")
	}
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()

	registry.Register(NewEchoTool())
	registry.Register(NewCalculatorTool())

	tools := registry.List()
	if len(tools) != 2 {
		t.Errorf("List() count = %v, want 2", len(tools))
	}
}

func TestRegistry_Execute(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewEchoTool())

	input := map[string]any{
		"message": "Hello, World!",
	}

	result, err := registry.Execute(t.Context(), "echo", input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.IsError {
		t.Error("Result should not be an error")
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatal("Output should be a map")
	}

	if echoed := output["echoed"]; echoed != "Hello, World!" {
		t.Errorf("Echoed message = %v, want 'Hello, World!'", echoed)
	}
}

func TestRegistry_ExecuteToolUse(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewCalculatorTool())

	toolUse := &provider.ToolUse{
		ID:   "tool-1",
		Name: "calculator",
		Input: map[string]any{
			"operation": "add",
			"a":         float64(5),
			"b":         float64(3),
		},
	}

	result, err := registry.ExecuteToolUse(t.Context(), toolUse)
	if err != nil {
		t.Fatalf("ExecuteToolUse() error = %v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatal("Output should be a map")
	}

	if result := output["result"]; result != float64(8) {
		t.Errorf("Result = %v, want 8", result)
	}
}

func TestRegistry_ToProviderTools(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewEchoTool())
	registry.Register(NewCalculatorTool())

	tools := registry.ToProviderTools(t.Context())

	if len(tools) != 2 {
		t.Errorf("Tool count = %v, want 2", len(tools))
	}

	for _, tool := range tools {
		if tool.Type != "function" {
			t.Errorf("Tool type = %v, want function", tool.Type)
		}

		if tool.Function.Name == "" {
			t.Error("Tool name should not be empty")
		}

		if tool.Function.Description == "" {
			t.Error("Tool description should not be empty")
		}
	}
}

func TestRegistry_ListForContext(t *testing.T) {
	policy := NewAllowListPolicy([]string{"echo"})
	registry := NewRegistry(WithPolicy(policy))
	registry.Register(NewEchoTool())
	registry.Register(NewCalculatorTool())

	tools := registry.ListForContext(t.Context())
	if len(tools) != 1 {
		t.Fatalf("ListForContext() count = %d, want 1", len(tools))
	}
	if tools[0].Name() != "echo" {
		t.Fatalf("ListForContext() tool = %q, want echo", tools[0].Name())
	}
}

func TestAllowListPolicy(t *testing.T) {
	policy := NewAllowListPolicy([]string{"echo", "calculator"})

	if !policy.CanExecute(t.Context(), "echo") {
		t.Error("CanExecute() should allow 'echo'")
	}

	if policy.CanExecute(t.Context(), "bash") {
		t.Error("CanExecute() should not allow 'bash'")
	}

	// Add tool
	policy.Add("bash")
	if !policy.CanExecute(t.Context(), "bash") {
		t.Error("CanExecute() should allow 'bash' after Add()")
	}

	// Remove tool
	policy.Remove("bash")
	if policy.CanExecute(t.Context(), "bash") {
		t.Error("CanExecute() should not allow 'bash' after Remove()")
	}
}

func TestRegistry_WithPolicy(t *testing.T) {
	policy := NewAllowListPolicy([]string{"echo"})
	registry := NewRegistry(WithPolicy(policy))

	registry.Register(NewEchoTool())
	registry.Register(NewCalculatorTool())

	// Echo should work
	_, err := registry.Execute(t.Context(), "echo", map[string]any{
		"message": "test",
	})
	if err != nil {
		t.Errorf("Execute(echo) error = %v", err)
	}

	// Calculator should be blocked
	_, err = registry.Execute(t.Context(), "calculator", map[string]any{
		"operation": "add",
		"a":         1.0,
		"b":         2.0,
	})
	if err == nil {
		t.Error("Execute(calculator) should be blocked by policy")
	}
}

func TestRegistry_Execute_ErrorResultIncludesMetrics(t *testing.T) {
	registry := NewRegistry()
	errTool := NewFuncTool("fail", "always fails", map[string]any{"type": "object"}, func(ctx context.Context, input map[string]any) (any, error) {
		return nil, fmt.Errorf("boom")
	})
	if err := registry.Register(errTool); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	result, err := registry.Execute(t.Context(), "fail", map[string]any{})
	if err == nil {
		t.Fatal("Execute() should return tool error")
	}
	if result == nil || !result.IsError {
		t.Fatal("Execute() should return an error result")
	}
	if result.Error == nil || result.Error.Error() != "boom" {
		t.Fatalf("result.Error = %v, want boom", result.Error)
	}
	if result.Metrics.Timestamp.IsZero() {
		t.Fatal("Metrics.Timestamp should be set")
	}
	if result.Metrics.Duration < 0 || result.Metrics.Duration > time.Second {
		t.Fatalf("Metrics.Duration = %v, want bounded non-negative duration", result.Metrics.Duration)
	}
}

func TestEchoTool(t *testing.T) {
	tool := NewEchoTool()

	if tool.Name() != "echo" {
		t.Errorf("Name() = %v, want echo", tool.Name())
	}

	result, err := tool.Execute(t.Context(), map[string]any{
		"message": "test",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.IsError {
		t.Error("Result should not be an error")
	}
}

func TestCalculatorTool(t *testing.T) {
	tool := NewCalculatorTool()

	tests := []struct {
		name      string
		input     map[string]any
		expected  float64
		wantError bool
	}{
		{
			name: "add",
			input: map[string]any{
				"operation": "add",
				"a":         float64(5),
				"b":         float64(3),
			},
			expected: 8,
		},
		{
			name: "subtract",
			input: map[string]any{
				"operation": "subtract",
				"a":         float64(10),
				"b":         float64(4),
			},
			expected: 6,
		},
		{
			name: "multiply",
			input: map[string]any{
				"operation": "multiply",
				"a":         float64(4),
				"b":         float64(5),
			},
			expected: 20,
		},
		{
			name: "divide",
			input: map[string]any{
				"operation": "divide",
				"a":         float64(10),
				"b":         float64(2),
			},
			expected: 5,
		},
		{
			name: "divide by zero",
			input: map[string]any{
				"operation": "divide",
				"a":         float64(10),
				"b":         float64(0),
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(t.Context(), tt.input)

			if tt.wantError {
				if err == nil {
					t.Error("Execute() should return error")
				}
				return
			}

			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			output, ok := result.Output.(map[string]any)
			if !ok {
				t.Fatal("Output should be a map")
			}

			if result := output["result"]; result != tt.expected {
				t.Errorf("Result = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestTimestampTool(t *testing.T) {
	tool := NewTimestampTool()

	result, err := tool.Execute(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatal("Output should be a map")
	}

	if _, ok := output["timestamp"]; !ok {
		t.Error("Output should contain timestamp")
	}

	if _, ok := output["iso8601"]; !ok {
		t.Error("Output should contain iso8601")
	}

	if _, ok := output["human"]; !ok {
		t.Error("Output should contain human")
	}
}

func TestFormatResultAsString(t *testing.T) {
	tests := []struct {
		name     string
		result   *Result
		contains string
	}{
		{
			name: "success",
			result: &Result{
				Output: map[string]any{"result": 42},
			},
			contains: "result",
		},
		{
			name: "error",
			result: &Result{
				Error:   fmt.Errorf("test error"),
				IsError: true,
			},
			contains: "Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := FormatResultAsString(tt.result)
			if !contains(str, tt.contains) {
				t.Errorf("FormatResultAsString() = %v, should contain %v", str, tt.contains)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- NewReadFileTool ---

func TestReadFileTool_Success(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "read_file_test")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	_, _ = f.WriteString("hello file")
	_ = f.Close()

	tool := NewReadFileTool()
	result, err := tool.Execute(t.Context(), map[string]any{"path": f.Name()})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	out := result.Output.(map[string]any)
	if out["content"] != "hello file" {
		t.Errorf("content = %v, want 'hello file'", out["content"])
	}
	if out["size"] != 10 {
		t.Errorf("size = %v, want 10", out["size"])
	}
}

func TestReadFileTool_EmptyPath(t *testing.T) {
	tool := NewReadFileTool()
	_, err := tool.Execute(t.Context(), map[string]any{"path": ""})
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestReadFileTool_NotFound(t *testing.T) {
	tool := NewReadFileTool()
	_, err := tool.Execute(t.Context(), map[string]any{"path": "/nonexistent/path/file.txt"})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// --- NewWriteFileTool ---

func TestWriteFileTool_Success(t *testing.T) {
	path := t.TempDir() + "/write_test.txt"
	tool := NewWriteFileTool()
	result, err := tool.Execute(t.Context(), map[string]any{"path": path, "content": "written"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	out := result.Output.(map[string]any)
	if out["success"] != true {
		t.Errorf("success = %v, want true", out["success"])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "written" {
		t.Errorf("file content = %q, want 'written'", string(data))
	}
}

func TestWriteFileTool_EmptyPath(t *testing.T) {
	tool := NewWriteFileTool()
	_, err := tool.Execute(t.Context(), map[string]any{"path": "", "content": "x"})
	if err == nil {
		t.Error("expected error for empty path")
	}
}

// --- NewBashTool ---

func TestBashTool_Echo(t *testing.T) {
	tool := NewBashTool(0) // default timeout
	result, err := tool.Execute(t.Context(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	out := result.Output.(map[string]any)
	if out["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", out["exit_code"])
	}
}

func TestBashTool_EmptyCommand(t *testing.T) {
	tool := NewBashTool(0)
	_, err := tool.Execute(t.Context(), map[string]any{"command": ""})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

// --- NewCalculatorTool edge cases ---

func TestCalculatorTool_UnknownOperation(t *testing.T) {
	tool := NewCalculatorTool()
	_, err := tool.Execute(t.Context(), map[string]any{
		"operation": "modulo",
		"a":         float64(5),
		"b":         float64(3),
	})
	if err == nil {
		t.Error("expected error for unknown operation")
	}
}

func TestCalculatorTool_IntegerInputs(t *testing.T) {
	tool := NewCalculatorTool()
	result, err := tool.Execute(t.Context(), map[string]any{
		"operation": "add",
		"a":         int(3),
		"b":         int(4),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	out := result.Output.(map[string]any)
	if out["result"] != float64(7) {
		t.Errorf("result = %v, want 7", out["result"])
	}
}
