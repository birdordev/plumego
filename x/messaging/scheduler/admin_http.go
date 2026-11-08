package scheduler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/contract"
)

const maxAdminJobIDLen = 256

type adminActionResponse struct {
	Status string `json:"status"`
}

type adminClearDeadLettersResponse struct {
	Cleared int `json:"cleared"`
}

type adminBulkActionResponse struct {
	Affected int `json:"affected"`
}

// AdminHandler exposes minimal management endpoints over net/http.
type AdminHandler struct {
	scheduler *Scheduler
	prefix    string
}

// NewAdminHandler constructs an admin handler.
func NewAdminHandler(s *Scheduler) *AdminHandler {
	return &AdminHandler{scheduler: s, prefix: "/scheduler"}
}

// WithPrefix sets the handler path prefix (default: /scheduler).
func (h *AdminHandler) WithPrefix(prefix string) *AdminHandler {
	if prefix != "" {
		h.prefix = prefix
	}
	return h
}

// ServeHTTP implements http.Handler.
func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.scheduler == nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeUnavailable).Message("scheduler not configured").Build())
		return
	}
	path := strings.TrimPrefix(r.URL.Path, h.prefix)
	if path == "" {
		path = "/"
	}

	switch {
	case r.Method == http.MethodGet && path == "/health":
		_ = contract.WriteResponse(w, r, http.StatusOK, h.scheduler.Health(), nil)
		return
	case r.Method == http.MethodGet && path == "/stats":
		_ = contract.WriteResponse(w, r, http.StatusOK, h.scheduler.Stats(), nil)
		return
	case r.Method == http.MethodGet && path == "/jobs":
		query, err := parseJobQueryE(r)
		if err != nil {
			writeAdminInvalidQueryError(w, r, err.Error())
			return
		}
		result := h.scheduler.QueryJobs(query)
		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		_ = contract.WriteResponse(w, r, http.StatusOK, result.Jobs, nil)
		return
	case strings.HasPrefix(path, "/jobs/"):
		h.handleJob(w, r, strings.TrimPrefix(path, "/jobs/"))
		return
	// Dead letter queue endpoints
	case r.Method == http.MethodGet && path == "/dlq":
		_ = contract.WriteResponse(w, r, http.StatusOK, h.scheduler.ListDeadLetters(), nil)
		return
	case r.Method == http.MethodDelete && path == "/dlq":
		count := h.scheduler.ClearDeadLetters()
		_ = contract.WriteResponse(w, r, http.StatusOK, adminClearDeadLettersResponse{Cleared: count}, nil)
		return
	case strings.HasPrefix(path, "/dlq/"):
		h.handleDLQEntry(w, r, strings.TrimPrefix(path, "/dlq/"))
		return
	// Bulk group operations: POST /scheduler/groups/{group}/{action}
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/groups/"):
		h.handleGroupAction(w, r, strings.TrimPrefix(path, "/groups/"))
		return
	// Bulk tag operations: POST /scheduler/tags/{tag}/{action}
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/tags/"):
		h.handleTagAction(w, r, strings.TrimPrefix(path, "/tags/"))
		return
	default:
		writeAdminRouteNotFound(w, r)
	}
}

func (h *AdminHandler) handleJob(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeAdminRouteNotFound(w, r)
		return
	}
	// Validate job ID length to prevent abuse via extremely long path segments.
	if len(parts[0]) > maxAdminJobIDLen {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Message("validation failed for field 'job_id': job ID too long").
			Detail("field", "job_id").
			Detail("validation_message", "job ID too long").
			Build())
		return
	}
	id := JobID(parts[0])

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r)
			return
		}
		status, ok := h.scheduler.Status(id)
		if !ok {
			writeAdminJobNotFound(w, r, id)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, status, nil)
		return
	}

	action := parts[1]
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	switch action {
	case "pause":
		if !h.scheduler.Pause(id) {
			writeAdminJobNotFound(w, r, id)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, adminActionResponse{Status: "paused"}, nil)
	case "resume":
		if !h.scheduler.Resume(id) {
			writeAdminJobNotFound(w, r, id)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, adminActionResponse{Status: "resumed"}, nil)
	case "cancel":
		if !h.scheduler.Cancel(id) {
			writeAdminJobNotFound(w, r, id)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, adminActionResponse{Status: "canceled"}, nil)
	case "trigger":
		if err := h.scheduler.TriggerNow(id); err != nil {
			if errors.Is(err, ErrJobNotFound) {
				writeAdminJobNotFound(w, r, id)
				return
			}
			writeAdminTriggerFailed(w, r)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, adminActionResponse{Status: "triggered"}, nil)
	default:
		writeAdminRouteNotFound(w, r)
	}
}

// handleDLQEntry handles per-entry dead letter queue operations.
func (h *AdminHandler) handleDLQEntry(w http.ResponseWriter, r *http.Request, suffix string) {
	id := strings.Trim(suffix, "/")
	if id == "" {
		writeAdminRouteNotFound(w, r)
		return
	}
	if len(id) > maxAdminJobIDLen {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Message("validation failed for field 'job_id': job ID too long").
			Detail("field", "job_id").
			Detail("validation_message", "job ID too long").
			Build())
		return
	}
	jobID := JobID(id)

	switch r.Method {
	case http.MethodGet:
		entry, ok := h.scheduler.GetDeadLetter(jobID)
		if !ok {
			writeAdminDeadLetterNotFound(w, r, jobID)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, entry, nil)
	case http.MethodDelete:
		if !h.scheduler.DeleteDeadLetter(jobID) {
			writeAdminDeadLetterNotFound(w, r, jobID)
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, adminActionResponse{Status: "deleted"}, nil)
	default:
		writeMethodNotAllowed(w, r)
	}
}

// handleGroupAction handles bulk operations on a job group.
// Path: /groups/{group}/{action}  Method: POST
// Supported actions: pause, resume, cancel
func (h *AdminHandler) handleGroupAction(w http.ResponseWriter, r *http.Request, suffix string) {
	h.handleBulkAction(w, r, suffix, bulkActions{
		pause:  h.scheduler.PauseByGroup,
		resume: h.scheduler.ResumeByGroup,
		cancel: h.scheduler.CancelByGroup,
	})
}

// handleTagAction handles bulk operations on jobs sharing a tag.
// Path: /tags/{tag}/{action}  Method: POST
// Supported actions: pause, resume, cancel
func (h *AdminHandler) handleTagAction(w http.ResponseWriter, r *http.Request, suffix string) {
	h.handleBulkAction(w, r, suffix, bulkActions{
		pause:  func(tag string) int { return h.scheduler.PauseByTags(tag) },
		resume: func(tag string) int { return h.scheduler.ResumeByTags(tag) },
		cancel: func(tag string) int { return h.scheduler.CancelByTags(tag) },
	})
}

type bulkActions struct {
	pause  func(string) int
	resume func(string) int
	cancel func(string) int
}

func (h *AdminHandler) handleBulkAction(w http.ResponseWriter, r *http.Request, suffix string, actions bulkActions) {
	parts := strings.SplitN(strings.Trim(suffix, "/"), "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeAdminRouteNotFound(w, r)
		return
	}
	key, action := parts[0], parts[1]
	var n int
	switch action {
	case "pause":
		n = actions.pause(key)
	case "resume":
		n = actions.resume(key)
	case "cancel":
		n = actions.cancel(key)
	default:
		writeAdminRouteNotFound(w, r)
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, adminBulkActionResponse{Affected: n}, nil)
}

// parseJobQuery builds a JobQuery from URL query parameters.
// Supported parameters:
//
//	state=running&state=failed  — filter by job state (repeatable)
//	group=mygroup               — filter by group
//	tag=t1&tag=t2               — filter by tags (all must match)
//	kind=cron&kind=delay        — filter by kind (repeatable)
//	running=true                — filter by running flag
//	paused=false                — filter by paused flag
//	order_by=next_run           — sort field: id, next_run, last_run, group
//	asc=false                   — sort direction (default true)
//	limit=50                    — max results
//	offset=0                    — pagination offset
func parseJobQuery(r *http.Request) JobQuery {
	q, _ := parseJobQueryE(r)
	return q
}

func parseJobQueryE(r *http.Request) (JobQuery, error) {
	var q JobQuery
	if r == nil {
		return q, nil
	}
	values := r.URL.Query()

	q.States = parseJobStates(values["state"])
	q.Group = values.Get("group")
	q.Tags = append([]string(nil), values["tag"]...)
	q.Kinds = parseJobKinds(values["kind"])
	q.OrderBy = values.Get("order_by")
	q.Ascending = true
	q.Limit = parseOptionalBoundedInt(values.Get("limit"), 10_000)
	q.Offset = parseOptionalBoundedInt(values.Get("offset"), 1_000_000)

	var firstErr error
	var err error
	if q.Running, err = parseOptionalBoolE(values.Get("running"), "running"); err != nil && firstErr == nil {
		firstErr = err
	}
	if q.Paused, err = parseOptionalBoolE(values.Get("paused"), "paused"); err != nil && firstErr == nil {
		firstErr = err
	}
	asc, err := parseOptionalBoolE(values.Get("asc"), "asc")
	if err != nil && firstErr == nil {
		firstErr = err
	}
	if asc != nil {
		q.Ascending = *asc
	}

	return q, firstErr
}

func parseJobStates(values []string) []JobState {
	out := make([]JobState, 0, len(values))
	for _, v := range values {
		if s := parseJobState(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseJobKinds(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		k := strings.ToLower(v)
		if k == "cron" || k == "delay" {
			out = append(out, k)
		}
	}
	return out
}

func parseOptionalBoolE(value, field string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		if field == "" {
			field = "boolean"
		}
		return nil, errors.New(field)
	}
	return &b, nil
}

func parseOptionalBoundedInt(value string, max int) int {
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}

func parseJobState(value string) JobState {
	switch JobState(strings.ToLower(value)) {
	case JobStateQueued,
		JobStateScheduled,
		JobStateRunning,
		JobStateFailed,
		JobStateRetrying,
		JobStateCanceled,
		JobStateCompleted:
		return JobState(strings.ToLower(value))
	default:
		return ""
	}
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeMethodNotAllowed).
		Message("method not allowed").
		Build())
}

func writeAdminRouteNotFound(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotFound).
		Message("scheduler admin route not found").
		Detail("path", r.URL.Path).
		Build())
}

func writeAdminJobNotFound(w http.ResponseWriter, r *http.Request, id JobID) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotFound).
		Message("scheduler job not found").
		Detail("job_id", id).
		Build())
}

func writeAdminDeadLetterNotFound(w http.ResponseWriter, r *http.Request, id JobID) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotFound).
		Message("dead letter entry not found").
		Detail("job_id", id).
		Build())
}

func writeAdminTriggerFailed(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInvalidRequest).
		Code(contract.CodeInvalidRequest).
		Message("scheduler job trigger failed").
		Build())
}

func writeAdminInvalidQueryError(w http.ResponseWriter, r *http.Request, field string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeInvalidQuery).
		Message("invalid scheduler admin query").
		Detail("field", field).
		Build())
}
