package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresBusinessCalendarVersionsAndOccurrences(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	t.Setenv("SCHEDULER_CALENDAR_PUBLISHER_ACTOR", "calendar-admin")
	newTenantApp := func(tenant, actor string) http.Handler {
		t.Helper()
		app, err := newAppWithActor("", "test-token", tenant, actor, repo)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	owner := newTenantApp("tenant", "calendar-admin")
	ordinary := newTenantApp("tenant", "api-user")
	other := newTenantApp("other", "calendar-admin")
	request := func(app http.Handler, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		response := auditHTTPRequest(ctx, app, method, path, "", "", body)
		if response.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.Code, want, response.Body)
		}
		return response
	}
	const days = `["Monday","Tuesday","Wednesday","Thursday","Friday"]`
	calendar := func(newYearOpen bool) string {
		return fmt.Sprintf(`{"time_zone":"Asia/Shanghai","open_weekdays":%s,"exceptions":[
			{"date":"2024-12-31","open":false},{"date":"2025-01-01","open":%t},{"date":"2025-01-05","open":true}]}`, days, newYearOpen)
	}
	const v1 = "/business-calendars/settlement/versions/1"
	const v2 = "/business-calendars/settlement/versions/2"
	request(ordinary, "PUT", v1, calendar(false), 403)
	request(owner, "PUT", v1, calendar(false), 204)
	request(owner, "PUT", v1, calendar(false), 204)
	request(owner, "PUT", v1, calendar(true), 409)
	request(owner, "PUT", "/business-calendars/settlement/versions/3",
		`{"time_zone":"UTC","open_weekdays":["Monday"],"exceptions":[{"date":"2025-02-29","open":true}]}`, 400)

	definition := func(name, expression string, version int) string {
		return fmt.Sprintf(`"expression":%q,"time_zone":"UTC","topic":"demo.record","payload":{},
			"jitter_window_ms":1000,"calendar":{"name":%q,"version":%d}`, expression, name, version)
	}
	plan := definition("settlement", "30 16 * * *", 1)
	request(other, "POST", "/schedules", `{"id":"daily",`+plan+`}`, 400)
	request(owner, "POST", "/schedules", `{"id":"interval",`+definition("settlement", "@every 1m", 1)+`}`, 400)
	request(owner, "POST", "/schedules", `{"id":"daily",`+plan+`}`, 201)
	const previewPath = "/schedules/daily/calendar-preview?after=2024-12-29T16:30:00Z&limit=3"
	request(other, "GET", previewPath, "", 404)
	expectPreview := func(version int, dates []string, firstUTC string) {
		t.Helper()
		res := request(owner, "GET", previewPath, "", 200)
		var body struct{ Data calendarPreview }
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		got := body.Data
		if got.Calendar != (calendarPin{"settlement", version}) || got.ScheduleVersion != int64(version) || len(got.Items) != len(dates) {
			t.Fatalf("preview = %+v", got)
		}
		var actualDates []string
		for _, item := range got.Items {
			actualDates = append(actualDates, item.CalendarDate)
			if delay := item.DispatchAt.Sub(item.ScheduledAt); delay < 0 || delay > time.Second {
				t.Fatalf("unexpected jitter %s", delay)
			}
		}
		if !slices.Equal(actualDates, dates) || got.Items[0].ScheduledAt.Format(time.RFC3339) != firstUTC {
			t.Fatalf("preview = %+v", got)
		}
	}
	expectPreview(1, []string{"2025-01-02", "2025-01-03", "2025-01-05"}, "2025-01-01T16:30:00Z")
	request(owner, "PUT", v2, calendar(true), 204)
	expectPreview(1, []string{"2025-01-02", "2025-01-03", "2025-01-05"}, "2025-01-01T16:30:00Z")
	request(owner, "PUT", "/schedules/daily", `{"version":1,`+definition("settlement", "30 16 * * *", 2)+`}`, 200)
	request(owner, "PUT", "/schedules/daily", `{"version":1,`+plan+`}`, 409)
	expectPreview(2, []string{"2025-01-01", "2025-01-02", "2025-01-03"}, "2024-12-31T16:30:00Z")

	at := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	for version := 1; version <= 2; version++ {
		rules := `{"time_zone":"UTC","open_weekdays":["Sunday","Monday","Tuesday","Wednesday","Thursday","Friday","Saturday"]}`
		request(owner, "PUT", fmt.Sprintf("/business-calendars/live/versions/%d", version), rules, 204)
	}
	liveSchedule := definition("live", "* * * * *", 1)
	request(owner, "POST", "/schedules", `{"id":"live",`+liveSchedule+`}`, 201)
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at=$1 WHERE tenant_id='tenant' AND id='live'", at); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.materializeNext(ctx); id == "" || err != nil {
		t.Fatalf("materialize %q: %v", id, err)
	}
	request(owner, "PUT", "/schedules/live", `{"version":1,`+definition("live", "* * * * *", 2)+`}`, 200)
	old, err := repo.plannedOccurrence(ctx, "tenant", "live", at)
	if err != nil || old.Calendar == nil || old.Calendar.Version != 1 || old.ScheduleVersion != 1 {
		t.Fatalf("persisted old occurrence %+v: %v", old, err)
	}
	state, err := repo.scheduleStatus(ctx, "tenant", "live")
	if err != nil || state.Calendar == nil || state.Calendar.Version != 2 || state.NextFireAt == nil || !state.NextFireAt.After(at.Add(time.Minute)) {
		t.Fatalf("replacement state %+v: %v", state, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at=$1 WHERE tenant_id='tenant' AND id='live'", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.materializeNext(ctx); id == "" || err != nil {
		t.Fatalf("materialize using new binding %q: %v", id, err)
	}
	current, err := repo.plannedOccurrence(ctx, "tenant", "live", at.Add(time.Minute))
	if err != nil || current.Calendar == nil || current.Calendar.Version != 2 || current.ScheduleVersion != 2 {
		t.Fatalf("persisted new occurrence %+v: %v", current, err)
	}
	var audits int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_audit WHERE operation='calendar.publish' AND actor='calendar-admin'").Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("publication audits = %d: %v", audits, err)
	}
}

func TestBusinessCalendarMissedRunsAndClosedDayBoundary(t *testing.T) {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	cal := &businessCalendar{loc: loc, openDays: 127, exceptions: map[string]bool{"2018-11-03": false}}
	cron, err := parseSchedule("* * * * * *", "UTC", nil)
	if err != nil {
		t.Fatal(err)
	}
	rule := calendarRecurrence{cron, cal}
	closed := time.Date(2018, 11, 3, 3, 0, 0, 0, time.UTC)
	nextOpen := time.Date(2018, 11, 4, 3, 0, 0, 0, time.UTC)
	if got := rule.Next(closed.Add(-time.Second)); !got.Equal(nextOpen) {
		t.Fatalf("next open across midnight DST transition = %s; want %s", got, nextOpen)
	}
	at := closed.Add(-time.Second)
	now := nextOpen.Add(5 * time.Second)
	for _, tc := range []struct {
		policy, action string
		fire, next     time.Time
	}{
		{"skip", "skip", time.Time{}, now.Add(time.Second)},
		{"coalesce", "coalesce", nextOpen, now.Add(time.Second)},
		{"catch_up", "trim", nextOpen, nextOpen.Add(time.Second)},
	} {
		definition := scheduleDefinition{MisfirePolicy: tc.policy, CatchUpWindow: 3600}
		fire, next, action := scheduleOccurrence(definition, rule, at, now)
		if !fire.Equal(tc.fire) || !next.Equal(tc.next) || action != tc.action {
			t.Errorf("%s = %s, %s, %s", tc.policy, fire, next, action)
		}
	}
}
