package session_test

import (
	"context"
	"fmt"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/session"
)

func ExampleManager() {
	ctx := context.Background()

	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage)

	s, _ := manager.Create(ctx, session.CreateOptions{
		TenantID: "tenant-a",
		UserID:   "user-1",
		Model:    "mock-model",
	})

	_ = manager.AppendMessage(ctx, s.ID, provider.NewTextMessage(provider.RoleUser, "hello session"))

	active, _ := manager.GetActiveContext(ctx, s.ID, 64)

	fmt.Println(storage.Count())
	fmt.Println(active[0].GetText())

	// Output:
	// 1
	// hello session
}
