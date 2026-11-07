package scheduler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Predefined cron schedule constants for common patterns.
const (
	CronEveryMinute = "* * * * *"
	CronHourly      = "0 * * * *"
	CronDaily       = "0 0 * * *"
	CronWeekly      = "0 0 * * 0"
	CronMonthly     = "0 0 1 * *"
	CronYearly      = "0 0 1 1 *"
)

// Expr returns the original cron expression string for this spec.
// For @every specs it returns "@every <duration>".
// For standard cron fields it reconstructs the expression.
func (c CronSpec) Expr() string {
	if c.interval > 0 {
		return "@every " + c.interval.String()
	}
	if len(c.minutes) == 0 {
		return ""
	}
	hasSec := len(c.seconds) > 0 && (len(c.seconds) != 1 || c.seconds[0] != 0)
	if hasSec {
		return cronFieldStr(c.seconds, 0, 59) + " " +
			cronFieldStr(c.minutes, 0, 59) + " " +
			cronFieldStr(c.hours, 0, 23) + " " +
			cronFieldStr(c.dom, 1, 31) + " " +
			cronFieldStr(c.months, 1, 12) + " " +
			cronFieldStr(c.dow, 0, 6)
	}
	return cronFieldStr(c.minutes, 0, 59) + " " +
		cronFieldStr(c.hours, 0, 23) + " " +
		cronFieldStr(c.dom, 1, 31) + " " +
		cronFieldStr(c.months, 1, 12) + " " +
		cronFieldStr(c.dow, 0, 6)
}

// cronFieldStr reconstructs a cron field string from an expanded values slice.
// Returns "*" when the field covers the full range.
func cronFieldStr(values []int, min, max int) string {
	full := buildRange(min, max, 1)
	if len(values) == len(full) {
		allMatch := true
		for i, v := range values {
			if v != full[i] {
				allMatch = false
				break
			}
		}
		if allMatch {
			return "*"
		}
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// CronSpec represents a parsed cron expression (minute, hour, dom, month, dow).
type CronSpec struct {
	seconds  []int
	minutes  []int
	hours    []int
	dom      []int
	months   []int
	dow      []int
	domStar  bool           // true when the day-of-month field was an unrestricted "*"
	dowStar  bool           // true when the day-of-week field was an unrestricted "*"
	location *time.Location // Time zone for scheduling
	interval time.Duration  // For @every syntax
}

// ParseCronSpec parses a cron expression with 5 (minute) or 6 (second) fields.
// Fields: [second] minute hour day-of-month month day-of-week
// Also supports descriptors like @hourly, @daily, @weekly, @monthly, @yearly
// and @every <duration> (e.g., @every 5m, @every 1h30m)
func ParseCronSpec(expr string) (CronSpec, error) {
	return ParseCronSpecWithLocation(expr, time.UTC)
}

// ParseCronSpecWithLocation parses a cron expression with a specific timezone.
func ParseCronSpecWithLocation(expr string, loc *time.Location) (CronSpec, error) {
	if loc == nil {
		loc = time.UTC
	}

	// Handle @every syntax
	if strings.HasPrefix(expr, "@every ") {
		durationStr := strings.TrimPrefix(expr, "@every ")
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return CronSpec{}, fmt.Errorf("%w: %w", ErrInvalidEveryDuration, err)
		}
		if duration <= 0 {
			return CronSpec{}, fmt.Errorf("%w: must be positive", ErrInvalidEveryDuration)
		}
		return CronSpec{
			location: loc,
			interval: duration,
		}, nil
	}

	// Handle predefined descriptors
	switch expr {
	case "@hourly":
		expr = CronHourly
	case "@daily", "@midnight":
		expr = CronDaily
	case "@weekly":
		expr = CronWeekly
	case "@monthly":
		expr = CronMonthly
	case "@yearly", "@annually":
		expr = CronYearly
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 && len(fields) != 6 {
		return CronSpec{}, fmt.Errorf("%w: expected 5 or 6 fields, got %d", ErrInvalidCronExpr, len(fields))
	}

	var seconds []int
	offset := 0
	if len(fields) == 6 {
		parsed, err := parseCronField(fields[0], 0, 59)
		if err != nil {
			return CronSpec{}, fmt.Errorf("second: %w", err)
		}
		seconds = parsed
		offset = 1
	} else {
		seconds = []int{0}
	}

	minutes, err := parseCronField(fields[offset], 0, 59)
	if err != nil {
		return CronSpec{}, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseCronField(fields[offset+1], 0, 23)
	if err != nil {
		return CronSpec{}, fmt.Errorf("hour: %w", err)
	}
	dom, err := parseCronField(fields[offset+2], 1, 31)
	if err != nil {
		return CronSpec{}, fmt.Errorf("day-of-month: %w", err)
	}
	months, err := parseCronField(fields[offset+3], 1, 12)
	if err != nil {
		return CronSpec{}, fmt.Errorf("month: %w", err)
	}
	dow, err := parseCronField(fields[offset+4], 0, 6)
	if err != nil {
		return CronSpec{}, fmt.Errorf("day-of-week: %w", err)
	}

	// Track whether DOM/DOW were wildcards so Next() can apply correct Vixie
	// cron semantics: when only one of the two is restricted the other is
	// ignored; OR behaviour applies only when both are restricted.
	domStar := strings.TrimSpace(fields[offset+2]) == "*"
	dowStar := strings.TrimSpace(fields[offset+4]) == "*"

	return CronSpec{
		seconds:  seconds,
		minutes:  minutes,
		hours:    hours,
		dom:      dom,
		months:   months,
		dow:      dow,
		domStar:  domStar,
		dowStar:  dowStar,
		location: loc,
	}, nil
}

func parseCronField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return buildRange(min, max, 1), nil
	}

	parts := strings.Split(field, ",")
	values := map[int]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%w: empty field", ErrInvalidCronField)
		}

		step := 1
		if strings.Contains(part, "/") {
			s := strings.Split(part, "/")
			if len(s) != 2 {
				return nil, fmt.Errorf("%w: invalid step syntax", ErrInvalidCronField)
			}
			part = s[0]
			parsed, err := strconv.Atoi(s[1])
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf("%w: invalid step value", ErrInvalidCronField)
			}
			step = parsed
		}

		var start, end int
		if part == "" || part == "*" {
			start = min
			end = max
		} else if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 {
				return nil, fmt.Errorf("%w: invalid range syntax", ErrInvalidCronField)
			}
			var err error
			start, err = strconv.Atoi(bounds[0])
			if err != nil {
				return nil, fmt.Errorf("%w: invalid range start", ErrInvalidCronField)
			}
			end, err = strconv.Atoi(bounds[1])
			if err != nil {
				return nil, fmt.Errorf("%w: invalid range end", ErrInvalidCronField)
			}
		} else {
			val, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid value", ErrInvalidCronField)
			}
			start = val
			end = val
		}

		if start < min || end > max || start > end {
			return nil, fmt.Errorf("%w: value out of bounds [%d-%d]", ErrInvalidCronField, min, max)
		}

		for v := start; v <= end; v += step {
			values[v] = struct{}{}
		}
	}

	result := make([]int, 0, len(values))
	for v := range values {
		result = append(result, v)
	}
	sort.Ints(result)
	return result, nil
}

func buildRange(min, max, step int) []int {
	result := make([]int, 0, ((max-min)/step)+1)
	for v := min; v <= max; v += step {
		result = append(result, v)
	}
	return result
}

// Next returns the next time after the given time that matches the spec.
func (c CronSpec) Next(after time.Time) time.Time {
	// Handle @every interval syntax
	if c.interval > 0 {
		return after.Add(c.interval)
	}

	// Convert to the spec's timezone
	loc := c.location
	if loc == nil {
		loc = time.UTC
	}
	next := after.In(loc).Truncate(time.Second).Add(time.Second)

	for i := 0; i < 366*24*60*60; i++ {
		if !containsInt(c.months, int(next.Month())) {
			next = time.Date(next.Year(), next.Month()+1, 1, 0, 0, 0, 0, next.Location())
			continue
		}
		// Vixie cron day-matching semantics:
		//   • Both DOM and DOW unrestricted ("*"): always match.
		//   • Only DOM restricted: match by DOM only (ignore DOW).
		//   • Only DOW restricted: match by DOW only (ignore DOM).
		//   • Both restricted: match if EITHER matches (OR behaviour).
		domMatch := containsInt(c.dom, next.Day())
		dowMatch := containsInt(c.dow, int(next.Weekday()))
		var dayMatch bool
		switch {
		case c.domStar && c.dowStar:
			dayMatch = true
		case c.domStar:
			dayMatch = dowMatch
		case c.dowStar:
			dayMatch = domMatch
		default:
			dayMatch = domMatch || dowMatch
		}
		if !dayMatch {
			next = time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, next.Location()).AddDate(0, 0, 1)
			continue
		}

		hour, carry := nextAllowed(next.Hour(), c.hours)
		if carry {
			next = time.Date(next.Year(), next.Month(), next.Day()+1, c.hours[0], 0, 0, 0, next.Location())
			continue
		}
		if hour != next.Hour() {
			next = time.Date(next.Year(), next.Month(), next.Day(), hour, 0, 0, 0, next.Location())
			continue
		}

		minute, carry := nextAllowed(next.Minute(), c.minutes)
		if carry {
			next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour()+1, c.minutes[0], 0, 0, next.Location())
			continue
		}
		if minute != next.Minute() {
			next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), minute, 0, 0, next.Location())
			continue
		}

		second, carry := nextAllowed(next.Second(), c.seconds)
		if carry {
			next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute()+1, c.seconds[0], 0, next.Location())
			continue
		}
		if second != next.Second() {
			next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute(), second, 0, next.Location())
			continue
		}

		return next
	}
	return time.Time{}
}

func containsInt(list []int, value int) bool {
	idx := sort.SearchInts(list, value)
	return idx < len(list) && list[idx] == value
}

func nextAllowed(current int, allowed []int) (int, bool) {
	if len(allowed) == 0 {
		return current, false
	}
	idx := sort.SearchInts(allowed, current)
	if idx < len(allowed) && allowed[idx] == current {
		return current, false
	}
	if idx < len(allowed) {
		return allowed[idx], false
	}
	return allowed[0], true
}
