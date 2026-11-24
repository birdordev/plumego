package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
)

func (h api) publishBusinessCalendar(w http.ResponseWriter, req *http.Request) {
	identity, _ := req.Context().Value(auditRequestKey{}).(auditIdentity)
	if h.calendarPublisher == "" || identity.actor != h.calendarPublisher {
		writeError(w, req, contract.TypeForbidden, "business calendar publication forbidden")
		return
	}
	version, err := strconv.Atoi(req.PathValue("version"))
	pin := calendarPin{req.PathValue("name"), version}
	if err != nil || !pin.valid() {
		writeError(w, req, contract.TypeBadRequest, "invalid business calendar name or version")
		return
	}
	var input calendarInput
	if !decodeRequest(w, req, &input) {
		return
	}
	err = h.repo.publishCalendar(req.Context(), h.tenant, pin, input)
	switch {
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "invalid business calendar rules")
	case errors.Is(err, errRequestConflict):
		writeError(w, req, contract.TypeConflict, "business calendar version already has different rules")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "business calendar store unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
	}
}

type calendarOccurrence struct {
	ScheduledAt  time.Time `json:"scheduled_at"`
	DispatchAt   time.Time `json:"dispatch_at"`
	CalendarDate string    `json:"calendar_date"`
}

type calendarPreview struct {
	ScheduleVersion int64                `json:"schedule_version"`
	Calendar        calendarPin          `json:"calendar"`
	Items           []calendarOccurrence `json:"items"`
}

var errCalendarUnbound = errors.New("schedule has no business calendar")

func (r repository) previewBusinessCalendar(ctx context.Context, tenant, scheduleID string, after time.Time, limit int) (calendarPreview, error) {
	var preview calendarPreview
	var expression, scheduleZone string
	var scheduleRules, calendarRules, exceptions []byte
	var calendarZone sql.NullString
	var mask sql.NullInt32
	var jitter int
	err := r.db.QueryRowContext(ctx, `SELECT s.version,s.expression,s.time_zone,s.zone_rules,
		COALESCE(c.name,''),COALESCE(c.version,0),c.time_zone,c.zone_rules,c.open_days,c.exceptions,
		scheduler_jitter_milliseconds(s.tenant_id,s.id,s.jitter_window_ms)
		FROM schedules s LEFT JOIN scheduler_business_calendars c
		ON (s.tenant_id,s.calendar_name,s.calendar_version)=(c.tenant_id,c.name,c.version)
		WHERE s.tenant_id=$1 AND s.id=$2`, tenant, scheduleID).
		Scan(&preview.ScheduleVersion, &expression, &scheduleZone, &scheduleRules, &preview.Calendar.Name, &preview.Calendar.Version,
			&calendarZone, &calendarRules, &mask, &exceptions, &jitter)
	if err != nil {
		return calendarPreview{}, err
	}
	if !calendarZone.Valid {
		return calendarPreview{}, errCalendarUnbound
	}
	calendar, err := parseCalendar(calendarZone.String, calendarRules, int(mask.Int32), exceptions)
	if err != nil {
		return calendarPreview{}, err
	}
	cron, err := parseSchedule(expression, scheduleZone, scheduleRules)
	if err != nil {
		return calendarPreview{}, err
	}
	recurrence := calendarRecurrence{cron, calendar}
	preview.Items = make([]calendarOccurrence, 0, limit)
	for len(preview.Items) < limit {
		next := recurrence.Next(after).UTC()
		dispatch := next.Add(time.Duration(jitter) * time.Millisecond)
		if next.IsZero() || dispatch.Year() > 9999 || next.Year() > 9999 {
			break
		}
		preview.Items = append(preview.Items, calendarOccurrence{next, dispatch, next.In(calendar.loc).Format(time.DateOnly)})
		after = next
	}
	return preview, nil
}

func (h api) previewBusinessCalendar(w http.ResponseWriter, req *http.Request) {
	params, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil || len(params["after"]) != 1 || len(params) > 2 ||
		(len(params) == 2 && len(params["limit"]) != 1) {
		writeError(w, req, contract.TypeBadRequest, "one after and an optional limit are required")
		return
	}
	after, err := time.Parse(time.RFC3339Nano, params.Get("after"))
	after = after.UTC()
	if err != nil || after.Nanosecond()%1000 != 0 || after.Year() < 1 || after.Year() > 9999 {
		writeError(w, req, contract.TypeBadRequest, "after must be RFC 3339 with at most microsecond precision")
		return
	}
	limit := 20
	if len(params) == 2 {
		limit, err = strconv.Atoi(params.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, req, contract.TypeBadRequest, "limit must be between 1 and 100")
			return
		}
	}
	preview, err := h.repo.previewBusinessCalendar(req.Context(), h.tenant, req.PathValue("id"), after, limit)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "schedule not found")
	case errors.Is(err, errCalendarUnbound):
		writeError(w, req, contract.TypeConflict, "schedule has no business calendar")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "business calendar preview unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, preview, nil)
	}
}
