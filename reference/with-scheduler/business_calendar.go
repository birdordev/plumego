package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

type calendarPin struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

func (p calendarPin) valid() bool {
	return runCursorID.MatchString(p.Name) && p.Version > 0 && p.Version <= math.MaxInt32
}

func (p calendarPin) binding() *calendarPin {
	if p.Name == "" {
		return nil
	}
	return &p
}

type calendarInput struct {
	TimeZone     string   `json:"time_zone"`
	OpenWeekdays []string `json:"open_weekdays"`
	Exceptions   []struct {
		Date string `json:"date"`
		Open *bool  `json:"open"`
	} `json:"exceptions"`
}

func (input calendarInput) rules() (int, []byte, error) {
	if len(input.OpenWeekdays) < 1 || len(input.OpenWeekdays) > 7 || len(input.Exceptions) > 366 {
		return 0, nil, errInvalidTask
	}
	var mask int
	for _, day := range input.OpenWeekdays {
		var bit int
		for weekday := time.Sunday; weekday <= time.Saturday; weekday++ {
			if day == weekday.String() {
				bit = 1 << weekday
				break
			}
		}
		if bit == 0 || mask&bit != 0 {
			return 0, nil, errInvalidTask
		}
		mask |= bit
	}
	exceptions := make(map[string]bool, len(input.Exceptions))
	for _, item := range input.Exceptions {
		date, err := time.Parse(time.DateOnly, item.Date)
		if err != nil || date.Year() < 1 || date.Format(time.DateOnly) != item.Date || item.Open == nil {
			return 0, nil, errInvalidTask
		}
		if _, exists := exceptions[item.Date]; exists {
			return 0, nil, errInvalidTask
		}
		exceptions[item.Date] = *item.Open
	}
	raw, err := json.Marshal(exceptions)
	return mask, raw, err
}

func (r repository) publishCalendar(ctx context.Context, tenant string, pin calendarPin, input calendarInput) error {
	if !pin.valid() {
		return errInvalidTask
	}
	mask, raw, err := input.rules()
	if err != nil {
		return err
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var matches bool
	readVersion := func() error {
		return tx.QueryRowContext(ctx, `SELECT time_zone=$4 AND open_days=$5 AND exceptions=$6::jsonb
			FROM scheduler_business_calendars WHERE tenant_id=$1 AND name=$2 AND version=$3`,
			tenant, pin.Name, pin.Version, input.TimeZone, mask, raw).Scan(&matches)
	}
	switch err := readVersion(); {
	case err == nil:
		if !matches {
			return errRequestConflict
		}
		return tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	zoneRules, err := loadScheduleZone(input.TimeZone)
	if err != nil {
		return err
	}
	if _, err := scheduleLocation(input.TimeZone, zoneRules); err != nil {
		return errors.Join(errInvalidTask, err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO scheduler_business_calendars
		(tenant_id,name,version,time_zone,zone_rules,open_days,exceptions)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
		tenant, pin.Name, pin.Version, input.TimeZone, zoneRules, mask, raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		if err := readVersion(); err != nil {
			return err
		}
		if !matches {
			return errRequestConflict
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'calendar.publish',$2,'{}'::jsonb,
		jsonb_build_object('version',$3::integer))`, tenant, pin.Name, pin.Version); err != nil {
		return err
	}
	return tx.Commit()
}

type businessCalendar struct {
	loc        *time.Location
	openDays   int
	exceptions map[string]bool
}

func parseCalendar(zone string, rules []byte, mask int, raw []byte) (*businessCalendar, error) {
	loc, err := scheduleLocation(zone, rules)
	if err != nil {
		return nil, err
	}
	cal := &businessCalendar{loc: loc, openDays: mask}
	if err := json.Unmarshal(raw, &cal.exceptions); err != nil {
		return nil, err
	}
	return cal, nil
}

func loadBusinessCalendar(ctx context.Context, tx *sql.Tx, tenant string, pin *calendarPin) (*businessCalendar, error) {
	if pin == nil {
		return nil, nil
	}
	var zone string
	var rules, raw []byte
	var mask int
	err := tx.QueryRowContext(ctx, `SELECT time_zone,zone_rules,open_days,exceptions FROM scheduler_business_calendars
		WHERE tenant_id=$1 AND name=$2 AND version=$3`, tenant, pin.Name, pin.Version).
		Scan(&zone, &rules, &mask, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInvalidTask
	}
	if err != nil {
		return nil, err
	}
	return parseCalendar(zone, rules, mask, raw)
}

type scheduleRecurrence interface {
	Next(time.Time) time.Time
}

type calendarRecurrence struct {
	cron     scheduleRecurrence
	calendar *businessCalendar
}

func (schedule calendarRecurrence) Next(after time.Time) time.Time {
	if schedule.calendar == nil {
		return schedule.cron.Next(after)
	}
	limit := after.AddDate(400, 0, 0)
	for next := schedule.cron.Next(after); !next.IsZero() && !next.After(limit); next = schedule.cron.Next(after) {
		local := next.In(schedule.calendar.loc)
		open, exception := schedule.calendar.exceptions[local.Format(time.DateOnly)]
		if (exception && open) || (!exception && schedule.calendar.openDays&(1<<local.Weekday()) != 0) {
			return next
		}
		// 以当前偏移计算下一个午夜。如果时区先换偏移，从转换点重新判断本地日期。
		_, offset := local.Zone()
		boundary := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, time.UTC).
			Add(-time.Duration(offset) * time.Second)
		_, transition := local.ZoneBounds()
		if transition.After(next) && transition.Before(boundary) {
			boundary = transition
		}
		if boundary.After(next) {
			after = boundary.Add(-time.Nanosecond)
		} else {
			after = next
		}
	}
	return time.Time{}
}

func calendarBinding(pin *calendarPin) (name any, version any) {
	if pin == nil {
		return nil, nil
	}
	return pin.Name, pin.Version
}
