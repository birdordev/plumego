package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/mq"
)

type sendAcceptedResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type receiptListResponse struct {
	Receipts []Receipt `json:"receipts"`
	Count    int       `json:"count"`
}

type channelHealthResponse struct {
	Channels []ChannelStatus `json:"channels"`
}

const (
	CodeProviderError    = "PROVIDER_ERROR"
	CodeQuotaExceeded    = "QUOTA_EXCEEDED"
	CodeDuplicateMessage = "DUPLICATE_MESSAGE"
	CodeTaskExpired      = "TASK_EXPIRED"
	CodeSendError        = "SEND_ERROR"
	CodeEmptyBatch       = "EMPTY_BATCH"
	CodeStatsError       = "STATS_ERROR"
)

// HandleSend is the HTTP handler for POST /messages/send.
func (s *Service) HandleSend(w http.ResponseWriter, r *http.Request) {
	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build())
		return
	}
	if err := s.Send(r.Context(), req); err != nil {
		writeServiceError(w, r, err)
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusAccepted, sendAcceptedResponse{
		ID:     req.ID,
		Status: messageStatusQueued,
	}, nil)
}

// HandleBatchSend is the HTTP handler for POST /messages/batch.
func (s *Service) HandleBatchSend(w http.ResponseWriter, r *http.Request) {
	var batch BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build())
		return
	}
	if len(batch.Requests) == 0 {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(CodeEmptyBatch).
			Message("requests array is empty").
			Build())
		return
	}
	result := s.SendBatch(r.Context(), batch)
	_ = contract.WriteResponse(w, r, http.StatusOK, result, nil)
}

// HandleStats is the HTTP handler for GET /messages/stats.
func (s *Service) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Stats(r.Context())
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(CodeStatsError).
			Message("message stats unavailable").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, stats, nil)
}

// HandleGetReceipt is the HTTP handler for GET /messages/:id/receipt.
func (s *Service) HandleGetReceipt(w http.ResponseWriter, r *http.Request) {
	id := contract.RequestContextFromContext(r.Context()).Params["id"]
	if id == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Code(contract.CodeRequired).
			Message("message id is required").
			Build())
		return
	}
	receipt, found := s.receipts.Get(id)
	if !found {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Code(contract.CodeResourceNotFound).
			Message("receipt not found").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, receipt, nil)
}

// HandleListReceipts is the HTTP handler for GET /messages/receipts.
func (s *Service) HandleListReceipts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := ReceiptFilter{
		Channel:  Channel(q.Get("channel")),
		Status:   q.Get("status"),
		TenantID: q.Get("tenant_id"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}
	receipts := s.receipts.List(filter)
	_ = contract.WriteResponse(w, r, http.StatusOK, receiptListResponse{
		Receipts: receipts,
		Count:    len(receipts),
	}, nil)
}

// HandleChannelHealth is the HTTP handler for GET /messages/channels.
func (s *Service) HandleChannelHealth(w http.ResponseWriter, r *http.Request) {
	statuses := s.monitor.Status()
	_ = contract.WriteResponse(w, r, http.StatusOK, channelHealthResponse{Channels: statuses}, nil)
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	_ = contract.WriteError(w, r, classifyServiceError(err))
}

func classifyServiceError(err error) contract.APIError {
	switch {
	case errors.Is(err, ErrProviderFailure):
		return contract.NewErrorBuilder().
			Type(contract.TypeBadGateway).
			Code(CodeProviderError).
			Message("provider error").
			Build()
	case errors.Is(err, ErrQuotaExceeded):
		return contract.NewErrorBuilder().
			Type(contract.TypeRateLimited).
			Code(CodeQuotaExceeded).
			Message("quota exceeded").
			Build()
	case errors.Is(err, mq.ErrDuplicateTask):
		return contract.NewErrorBuilder().
			Type(contract.TypeConflict).
			Code(CodeDuplicateMessage).
			Message("duplicate message").
			Build()
	case errors.Is(err, mq.ErrTaskExpired):
		return contract.NewErrorBuilder().
			Type(contract.TypeInvalidRequest).
			Message("task expired").
			Build()
	case errors.Is(err, mq.ErrNotInitialized):
		return contract.NewErrorBuilder().
			Type(contract.TypeUnavailable).
			Message("messaging service unavailable").
			Build()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return contract.NewErrorBuilder().
			Type(contract.TypeGatewayTimeout).
			Code(contract.CodeGatewayTimeout).
			Message("request timed out").
			Build()
	case isValidationError(err), errors.Is(err, mq.ErrInvalidConfig):
		return contract.NewErrorBuilder().
			Type(contract.TypeInvalidRequest).
			Code(contract.CodeValidationError).
			Message("message validation failed").
			Build()
	default:
		return contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(CodeSendError).
			Message("send failed").
			Build()
	}
}

func isValidationError(err error) bool {
	return errors.Is(err, ErrMissingID) ||
		errors.Is(err, ErrInvalidChannel) ||
		errors.Is(err, ErrInvalidEmail) ||
		errors.Is(err, ErrInvalidPhone) ||
		errors.Is(err, ErrMissingBody) ||
		errors.Is(err, ErrMissingRecipient) ||
		errors.Is(err, ErrMissingSubject) ||
		errors.Is(err, ErrTemplateRender)
}
