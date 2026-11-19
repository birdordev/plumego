package session

import (
	"testing"

	"github.com/birdordev/plumego/x/ai/provider"
)

func TestMemoryStorageOwnsSessionSnapshot(t *testing.T) {
	s := NewMemoryStorage()
	input := &Session{
		ID:       "sample",
		Metadata: map[string]string{"region": "original"},
		Context:  map[string]any{"nested": []any{map[string]any{"key": true}}},
		Messages: []provider.Message{{Content: []provider.ContentBlock{{
			ToolUse:    &provider.ToolUse{Input: map[string]any{"count": []any{1}}},
			ToolResult: &provider.ToolResult{Content: "original"},
			Image:      &provider.Image{Data: "original"},
		}}}},
	}
	input.Context["self"] = input.Context
	if err := s.Save(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	mutate := func(session *Session) {
		session.Metadata["region"] = "edited"
		session.Context["self"].(map[string]any)["nested"].([]any)[0].(map[string]any)["key"] = false
		block := session.Messages[0].Content.([]provider.ContentBlock)[0]
		block.ToolUse.Input["count"].([]any)[0] = 2
		block.ToolResult.Content = "edited"
		block.Image.Data = "edited"
	}
	load := func() *Session {
		t.Helper()
		got, err := s.Load(t.Context(), input.ID)
		if err != nil {
			t.Fatal(err)
		}
		block := got.Messages[0].Content.([]provider.ContentBlock)[0]
		if got.Metadata["region"] != "original" ||
			got.Context["nested"].([]any)[0].(map[string]any)["key"] != true ||
			block.ToolUse.Input["count"].([]any)[0] != 1 ||
			block.ToolResult.Content != "original" || block.Image.Data != "original" {
			t.Fatal("stored session was mutated through a snapshot")
		}
		return got
	}
	mutate(input)
	mutate(load())
	load()
}
