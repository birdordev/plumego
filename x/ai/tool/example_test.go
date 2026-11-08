package tool_test

import (
	"context"
	"fmt"

	"github.com/birdordev/plumego/x/ai/tool"
)

func ExampleRegistry() {
	ctx := context.Background()

	registry := tool.NewRegistry(tool.WithPolicy(tool.NewAllowListPolicy([]string{"echo"})))
	_ = registry.Register(tool.NewEchoTool())
	_ = registry.Register(tool.NewCalculatorTool())

	result, _ := registry.Execute(ctx, "echo", map[string]any{
		"message": "hello tool",
	})

	output := result.Output.(map[string]any)
	fmt.Println(len(registry.ToProviderTools(ctx)))
	fmt.Println(output["echoed"])

	// Output:
	// 1
	// hello tool
}
