package provider_test

import (
	"context"
	"fmt"

	"github.com/birdordev/plumego/x/ai/provider"
)

func ExampleMockProvider() {
	ctx := context.Background()

	mock := provider.NewMockProvider("mock")
	mock.QueueResponse(&provider.CompletionResponse{
		Model: "mock-model",
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: "hello from mock"},
		},
	})

	resp, _ := mock.Complete(ctx, &provider.CompletionRequest{
		Model: "mock-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "Say hello"),
		},
	})

	fmt.Println(resp.GetText())
	fmt.Println(mock.CallCount())

	// Output:
	// hello from mock
	// 1
}
