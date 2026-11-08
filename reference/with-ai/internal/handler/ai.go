// Package handler contains HTTP handlers for the with-ai reference app.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/session"
	"github.com/birdordev/plumego/x/ai/streaming"
	"github.com/birdordev/plumego/x/ai/tool"
)

// AIHandler demonstrates the four x/ai subpackages: provider, session,
// streaming, and tool. All dependencies are wired via constructor injection.
type AIHandler struct {
	Provider *provider.MockProvider
	Sessions *session.Manager
	Streams  *streaming.StreamManager
	Tools    *tool.Registry
	Logger   plumelog.StructuredLogger
}

// NewAIHandler constructs an AIHandler with offline mock implementations of
// every x/ai extension. Replace the mock provider with a real one (e.g. OpenAI)
// by swapping the constructor argument — no handler changes required.
func NewAIHandler(logger plumelog.StructuredLogger) *AIHandler {
	registry := tool.NewRegistry(tool.WithPolicy(tool.NewAllowListPolicy([]string{"echo"})))
	_ = registry.Register(tool.NewEchoTool())
	_ = registry.Register(tool.NewCalculatorTool())

	return &AIHandler{
		Provider: provider.NewMockProvider("offline-mock"),
		Sessions: session.NewManager(session.NewMemoryStorage()),
		Streams:  streaming.NewStreamManager(),
		Tools:    registry,
		Logger:   logger,
	}
}

type chatRequest struct {
	Message string `json:"message"`
}

type chatResponse struct {
	SessionID   string         `json:"session_id"`
	Reply       string         `json:"reply"`
	ToolOutput  map[string]any `json:"tool_output"`
	StreamCount int            `json:"stream_count"`
}

// Chat handles a single chat turn: creates a session, calls the provider,
// executes the echo tool, and returns a combined response.
func (h *AIHandler) Chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build()))
		return
	}
	if req.Message == "" {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Detail("field", "message").
			Message("message is required").
			Build()))
		return
	}

	ctx := r.Context()
	s, err := h.Sessions.Create(ctx, session.CreateOptions{
		TenantID: "demo-tenant",
		UserID:   "demo-user",
		Model:    "mock-model",
	})
	if err != nil {
		h.writeInternal(w, r)
		return
	}
	_ = h.Sessions.AppendMessage(ctx, s.ID, provider.NewTextMessage(provider.RoleUser, req.Message))

	h.Provider.QueueResponse(&provider.CompletionResponse{
		Model: "mock-model",
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: "offline reply: " + req.Message},
		},
	})
	resp, err := h.Provider.Complete(ctx, &provider.CompletionRequest{
		Model:    "mock-model",
		Messages: []provider.Message{provider.NewTextMessage(provider.RoleUser, req.Message)},
	})
	if err != nil {
		h.writeInternal(w, r)
		return
	}

	toolResult, err := h.Tools.Execute(ctx, "echo", map[string]any{"message": req.Message})
	if err != nil {
		h.writeInternal(w, r)
		return
	}
	toolOutput, _ := toolResult.Output.(map[string]any)

	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, chatResponse{
		SessionID:   s.ID,
		Reply:       resp.GetText(),
		ToolOutput:  toolOutput,
		StreamCount: h.Streams.Count(),
	}, nil))
}

// Status reports the current state of all x/ai subpackages.
func (h *AIHandler) Status(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, map[string]any{
		"provider":     h.Provider.Name(),
		"sessions":     "memory",
		"tools":        len(h.Tools.ToProviderTools(r.Context())),
		"stream_count": h.Streams.Count(),
		"live_network": false,
	}, nil))
}

func (h *AIHandler) writeInternal(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Message("internal error").
		Build()))
}
