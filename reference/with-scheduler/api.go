package main

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
)

func newApp(address, token, tenant string, repo repository) (*core.App, error) {
	return newAppWithActor(address, token, tenant, "", repo)
}

func newAppWithActor(address, token, tenant, actor string, repo repository) (*core.App, error) {
	tracing, err := newSchedulerTracing(false)
	if err != nil {
		return nil, err
	}
	return newTracedApp(address, token, tenant, actor, repo, tracing, nil)
}

func newTracedApp(address, token, tenant, actor string, repo repository, tracing *schedulerTracing, objects *resultObjects) (*core.App, error) {
	if actor == "" {
		actor = "tenant-api"
	}
	if !runCursorID.MatchString(actor) {
		return nil, errors.New("invalid SCHEDULER_API_ACTOR")
	}
	if strings.TrimSpace(token) == "" || strings.TrimSpace(tenant) == "" {
		return nil, errors.New("SCHEDULER_API_TOKEN and SCHEDULER_TENANT are required")
	}
	cfg := core.DefaultConfig()
	cfg.Addr = address
	app := core.New(cfg, core.AppDependencies{})
	if err := app.EnableStandardPathValues(); err != nil {
		return nil, err
	}
	authenticate, err := auth.Authenticate(authn.StaticToken(token), auth.WithAuthRealm("scheduler"))
	if err != nil {
		return nil, err
	}
	h := api{repo: repo, tenant: tenant, objects: objects, compensationActor: os.Getenv("SCHEDULER_COMPENSATION_ACTOR"),
		workflowReleaseActor: os.Getenv("SCHEDULER_WORKFLOW_RELEASE_ACTOR"),
		calendarPublisher:    os.Getenv("SCHEDULER_CALENDAR_PUBLISHER_ACTOR"),
		sandboxEnabled:       os.Getenv("SCHEDULER_SANDBOX_TASKS") == "1"}
	mutation := func(handler http.HandlerFunc) http.Handler {
		return authenticate(auditRequest(actor, tracing, handler))
	}
	for _, route := range []struct {
		method, path string
		handler      http.Handler
	}{
		{http.MethodGet, "/healthz", http.HandlerFunc(h.live)},
		{http.MethodGet, "/readyz", readinessHandler(repo.db, func() bool { return true })},
		{http.MethodPost, "/runs", mutation(h.submit)},
		{http.MethodPost, "/workflows", mutation(h.createWorkflow)},
		{http.MethodPut, "/event-sources/:source", mutation(h.configureEventSource)},
		{http.MethodGet, "/event-sources/:source", authenticate(http.HandlerFunc(h.eventSourceStatus))},
		{http.MethodPost, "/event-sources/:source/events", mutation(h.ingestWorkflowEvent)},
		{http.MethodGet, "/event-sources/:source/events/:offset", authenticate(http.HandlerFunc(h.workflowEventStatus))},
		{http.MethodPost, "/event-sources/:source/replays", mutation(h.startEventReplay)},
		{http.MethodGet, "/event-sources/:source/replays/:id", authenticate(http.HandlerFunc(h.eventReplayStatus))},
		{http.MethodPut, "/workflow-definitions/:name/versions/:version", mutation(h.publishWorkflowDefinition)},
		{http.MethodPut, "/workflow-definitions/:name/active", mutation(h.activateWorkflowDefinition)},
		{http.MethodPost, "/workflows/:id/signals", mutation(h.signalWorkflow)},
		{http.MethodPost, "/workflows/:id/fanouts/:node/cancel", mutation(h.cancelWorkflowFanout)},
		{http.MethodGet, "/workflows/:id/fanouts/:node/children", authenticate(http.HandlerFunc(h.workflowFanoutChildren))},
		{http.MethodGet, "/workflows/:id", authenticate(http.HandlerFunc(h.workflowStatus))},
		{http.MethodGet, "/workflows/:id/compensations", authenticate(http.HandlerFunc(h.listCompensations))},
		{http.MethodPost, "/workflows/:id/compensations/:run", mutation(h.resolveCompensation)},
		{http.MethodPost, "/runs/batch", mutation(h.submitRunBatch)},
		{http.MethodPost, "/bulk-cancellations", mutation(h.planBulkCancel)},
		{http.MethodGet, "/bulk-cancellations/:id", authenticate(http.HandlerFunc(h.getBulkOperation))},
		{http.MethodPatch, "/bulk-cancellations/:id", mutation(h.pauseBulkOperation)},
		{http.MethodGet, "/runs", authenticate(http.HandlerFunc(h.listRuns))},
		{http.MethodGet, "/runs/:id/dead-letters", authenticate(http.HandlerFunc(h.deadLetters))},
		{http.MethodGet, "/runs/:id/result", authenticate(http.HandlerFunc(h.downloadResult))},
		{http.MethodPost, "/dead-letters/replay", mutation(h.replayDeadLetters)},
		{http.MethodGet, "/runs/:id", authenticate(http.HandlerFunc(h.status))},
		{http.MethodGet, "/runs/:id/attempts", authenticate(http.HandlerFunc(h.attempts))},
		{http.MethodGet, "/runs/:id/checkpoints", authenticate(http.HandlerFunc(h.checkpoints))},
		{http.MethodPost, "/runs/:id/cancel", mutation(h.cancelRun)},
		{http.MethodPut, "/business-calendars/:name/versions/:version", mutation(h.publishBusinessCalendar)},
		{http.MethodPost, "/schedules", mutation(h.createSchedule)},
		{http.MethodGet, "/schedules/:id/calendar-preview", authenticate(http.HandlerFunc(h.previewBusinessCalendar))},
		{http.MethodGet, "/schedules/:id", authenticate(http.HandlerFunc(h.scheduleStatus))},
		{http.MethodGet, "/schedules/:id/occurrence", authenticate(http.HandlerFunc(h.occurrence))},
		{http.MethodPatch, "/schedules/:id", mutation(h.setPaused)},
		{http.MethodPut, "/schedules/:id", mutation(h.replaceSchedule)},
		{http.MethodPost, "/schedules/:id/trigger", mutation(h.triggerSchedule)},
	} {
		if err := app.AddRoute(route.method, route.path, route.handler); err != nil {
			return nil, err
		}
	}
	return app, nil
}

type api struct {
	repo                 repository
	tenant               string
	objects              *resultObjects
	compensationActor    string
	workflowReleaseActor string
	calendarPublisher    string
	sandboxEnabled       bool
}

func (h api) live(w http.ResponseWriter, req *http.Request) {
	_ = contract.WriteResponse(w, req, http.StatusOK, map[string]bool{"live": true}, nil)
}

func (h api) submit(w http.ResponseWriter, req *http.Request) {
	var input submission
	if !decodeRequest(w, req, &input) || !h.requireTaskRuntime(w, req, input.Topic) {
		return
	}
	id, err := h.repo.submit(req.Context(), h.tenant, req.Header.Get("Idempotency-Key"), input)
	if writeSubmissionError(w, req, err) {
		return
	}
	w.Header().Set("Location", "/runs/"+id)
	_ = contract.WriteResponse(w, req, http.StatusAccepted, map[string]string{"id": id}, nil)
}

func writeSubmissionError(w http.ResponseWriter, req *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errTenantCapacity):
		w.Header().Set("Retry-After", "1")
		writeError(w, req, contract.TypeRateLimited, "tenant capacity exceeded")
	case errors.Is(err, errRequestConflict):
		writeError(w, req, contract.TypeConflict, "idempotency key already used")
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "invalid task submission")
	default:
		writeError(w, req, contract.TypeUnavailable, "submission unavailable, retry with the same idempotency key")
	}
	return true
}

func (h api) status(w http.ResponseWriter, req *http.Request) {
	result, err := h.repo.runStatus(req.Context(), h.tenant, req.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "run not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "run status unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}

func (h api) downloadResult(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	result, err := h.repo.runResult(req.Context(), h.tenant, req.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "run result not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "run result unavailable")
	case result.expired:
		writeError(w, req, contract.TypeGone, "run result expired")
	case result.objectID.Valid:
		h.downloadResultObject(w, req, result)
	default:
		w.Header().Set("Content-Disposition", `attachment; filename="result.json"`)
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}

func (h api) downloadResultObject(w http.ResponseWriter, req *http.Request, result runResult) {
	if h.objects == nil {
		writeError(w, req, contract.TypeUnavailable, "run result storage unavailable")
		return
	}
	f, err := h.objects.root.Open(resultObjectName(h.tenant, result.objectID.String))
	if err != nil {
		// Cleanup may have retired the object after the first metadata read.
		current, readErr := h.repo.runResult(req.Context(), h.tenant, result.RunID)
		if readErr == nil && current.expired {
			writeError(w, req, contract.TypeGone, "run result expired")
			return
		}
		writeError(w, req, contract.TypeUnavailable, "run result storage unavailable")
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !result.size.Valid || info.Size() != result.size.Int64 {
		writeError(w, req, contract.TypeUnavailable, "run result storage unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="result.bin"`)
	http.ServeContent(w, req, "result.bin", time.Time{}, f)
}

func (h api) cancelRun(w http.ResponseWriter, req *http.Request) {
	err := h.repo.cancelRun(req.Context(), h.tenant, req.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "run not found")
	case errors.Is(err, errRunFinalized):
		writeError(w, req, contract.TypeConflict, "run already finalized")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "cancellation unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
	}
}

func (h api) createSchedule(w http.ResponseWriter, req *http.Request) {
	var input scheduleDefinition
	if !decodeRequest(w, req, &input) || !h.requireTaskRuntime(w, req, input.Topic) {
		return
	}
	input.Tenant = h.tenant
	err := h.repo.addSchedule(req.Context(), input)
	switch {
	case errors.Is(err, errTenantCapacity):
		w.Header().Set("Retry-After", "1")
		writeError(w, req, contract.TypeRateLimited, "tenant capacity exceeded")
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "invalid schedule")
	case errors.Is(err, errScheduleExists):
		writeError(w, req, contract.TypeConflict, "schedule already exists")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "schedule storage unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusCreated, map[string]string{"id": input.ID}, nil)
	}
}

func (h api) scheduleStatus(w http.ResponseWriter, req *http.Request) {
	result, err := h.repo.scheduleStatus(req.Context(), h.tenant, req.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "schedule not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "schedule status unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}

func (h api) setPaused(w http.ResponseWriter, req *http.Request) {
	var input pauseCommand
	if !decodeRequest(w, req, &input) {
		return
	}
	if input.Version < 1 || (input.Paused == nil && input.ClaimPaused == nil && !input.CancelRunning) {
		writeError(w, req, contract.TypeBadRequest, "version and a pause or cancellation instruction are required")
		return
	}
	result, err := h.repo.setSchedulePause(req.Context(), h.tenant, req.PathValue("id"), input)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "schedule not found")
	case errors.Is(err, errScheduleVersion):
		writeError(w, req, contract.TypeConflict, "schedule version mismatch")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "schedule storage unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}

func (h api) replaceSchedule(w http.ResponseWriter, req *http.Request) {
	var input struct {
		scheduleDefinition
		Version         int64 `json:"version"`
		RefreshTimeZone bool  `json:"refresh_time_zone"`
	}
	if !decodeRequest(w, req, &input) || !h.requireTaskRuntime(w, req, input.Topic) {
		return
	}
	id := req.PathValue("id")
	if input.Version < 1 || (input.ID != "" && input.ID != id) {
		writeError(w, req, contract.TypeBadRequest, "version and matching schedule id are required")
		return
	}
	input.ID, input.Tenant = id, h.tenant
	input.refreshZone = input.RefreshTimeZone
	version, err := h.repo.replaceSchedule(req.Context(), input.Version, input.scheduleDefinition)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "schedule not found")
	case errors.Is(err, errScheduleVersion):
		writeError(w, req, contract.TypeConflict, "schedule version mismatch")
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "invalid schedule")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "schedule storage unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, map[string]int64{"version": version}, nil)
	}
}

func (h api) triggerSchedule(w http.ResponseWriter, req *http.Request) {
	id, err := h.repo.triggerSchedule(req.Context(), h.tenant, req.PathValue("id"), req.Header.Get("Idempotency-Key"))
	switch {
	case errors.Is(err, errTenantCapacity):
		w.Header().Set("Retry-After", "1")
		writeError(w, req, contract.TypeRateLimited, "tenant capacity exceeded")
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "schedule not found")
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "a valid idempotency key is required")
	case errors.Is(err, errRequestConflict):
		writeError(w, req, contract.TypeConflict, "idempotency key already used")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "submission unavailable, retry with the same idempotency key")
	default:
		w.Header().Set("Location", "/runs/"+id)
		_ = contract.WriteResponse(w, req, http.StatusAccepted, map[string]string{"id": id}, nil)
	}
}

func decodeRequest(w http.ResponseWriter, req *http.Request, dst any) bool {
	if err := contract.DecodeUniqueJSONRequest(req, dst, contract.JSONRequestOptions{MaxBytes: 128 << 10}); err != nil {
		apiErr, _ := contract.AsAPIError(err)
		_ = contract.WriteError(w, req, apiErr)
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, req *http.Request, kind contract.ErrorType, message string) {
	_ = contract.WriteError(w, req, contract.NewErrorBuilder().Type(kind).Message(message).Build())
}
