package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func normalizeScheduleForCreate(request CreateScheduleRequest) (ScheduleSpec, error) {
	id := strings.TrimSpace(request.ID)
	if id == "" {
		return ScheduleSpec{}, errors.New("schedule id is required")
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return ScheduleSpec{}, errors.New("schedule prompt is required")
	}
	kind, err := normalizeScheduleTriggerKind(request.TriggerKind)
	if err != nil {
		return ScheduleSpec{}, err
	}
	trigger := strings.TrimSpace(request.Trigger)
	if err := validateScheduleTrigger(kind, trigger); err != nil {
		return ScheduleSpec{}, err
	}
	timezone, err := normalizeScheduleTimezone(request.Timezone)
	if err != nil {
		return ScheduleSpec{}, err
	}
	start, end, err := normalizeQuietHours(request.QuietHoursStart, request.QuietHoursEnd)
	if err != nil {
		return ScheduleSpec{}, err
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	return ScheduleSpec{
		ID:              id,
		Workspace:       strings.TrimSpace(request.Workspace),
		TriggerKind:     kind,
		Trigger:         trigger,
		Prompt:          prompt,
		Timezone:        timezone,
		QuietHoursStart: start,
		QuietHoursEnd:   end,
		Enabled:         enabled,
	}, nil
}

func normalizeScheduleTriggerKind(kind ScheduleTriggerKind) (ScheduleTriggerKind, error) {
	switch ScheduleTriggerKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case ScheduleTriggerOneShot:
		return ScheduleTriggerOneShot, nil
	case ScheduleTriggerInterval:
		return ScheduleTriggerInterval, nil
	case ScheduleTriggerCron:
		return ScheduleTriggerCron, nil
	default:
		return "", fmt.Errorf("unknown schedule trigger kind %q", kind)
	}
}

func validateScheduleTrigger(kind ScheduleTriggerKind, trigger string) error {
	if strings.TrimSpace(trigger) == "" {
		return errors.New("schedule trigger is required")
	}
	switch kind {
	case ScheduleTriggerOneShot:
		if _, err := time.Parse(time.RFC3339, trigger); err != nil {
			return fmt.Errorf("invalid one-shot trigger %q: %w", trigger, err)
		}
		return nil
	case ScheduleTriggerInterval:
		duration, err := time.ParseDuration(trigger)
		if err != nil {
			return fmt.Errorf("invalid interval trigger %q: %w", trigger, err)
		}
		if duration <= 0 {
			return errors.New("interval trigger must be positive")
		}
		return nil
	case ScheduleTriggerCron:
		return validateCronLikeTrigger(trigger)
	default:
		return fmt.Errorf("unknown schedule trigger kind %q", kind)
	}
}

func validateCronLikeTrigger(trigger string) error {
	fields := strings.Fields(trigger)
	if len(fields) != 5 {
		return fmt.Errorf("cron-like trigger must have 5 fields, got %d", len(fields))
	}
	for _, field := range fields {
		if !cronFieldPattern.MatchString(field) {
			return fmt.Errorf("cron-like trigger field %q contains unsupported characters", field)
		}
	}
	return nil
}

func normalizeScheduleTimezone(timezone string) (string, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return "Local", nil
	}
	if timezone == "Local" {
		return "Local", nil
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", fmt.Errorf("unknown schedule timezone %q: %w", timezone, err)
	}
	return timezone, nil
}

var (
	cronFieldPattern  = regexp.MustCompile(`^[0-9*/,-]+$`)
	quietHoursPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

func normalizeQuietHours(start string, end string) (string, string, error) {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	if start == "" && end == "" {
		return "", "", nil
	}
	if start == "" || end == "" {
		return "", "", errors.New("quiet hours require both start and end")
	}
	if !quietHoursPattern.MatchString(start) {
		return "", "", fmt.Errorf("invalid quiet hours start %q", start)
	}
	if !quietHoursPattern.MatchString(end) {
		return "", "", fmt.Errorf("invalid quiet hours end %q", end)
	}
	return start, end, nil
}

func querySchedules(db *sql.DB, options ScheduleListOptions) ([]ScheduleSpec, error) {
	var clauses []string
	args := []any{}
	if !options.IncludeDisabled {
		clauses = append(clauses, `enabled = 1`)
	}
	if workspace := strings.TrimSpace(options.Workspace); workspace != "" {
		clauses = append(clauses, `workspace = ?`)
		args = append(args, workspace)
	}
	sqlText := `SELECT id, workspace, trigger_kind, trigger, prompt, timezone, quiet_hours_start, quiet_hours_end, enabled, schema_version, created_at, updated_at FROM schedules`
	if len(clauses) > 0 {
		sqlText += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	sqlText += ` ORDER BY updated_at DESC, id DESC`
	if options.Limit > 0 {
		sqlText += fmt.Sprintf(" LIMIT %d", options.Limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schedules := []ScheduleSpec{}
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func getSchedule(db *sql.DB, id string) (ScheduleSpec, error) {
	return scanSchedule(db.QueryRow(`
		SELECT id, workspace, trigger_kind, trigger, prompt, timezone, quiet_hours_start, quiet_hours_end, enabled, schema_version, created_at, updated_at
		FROM schedules
		WHERE id = ?
	`, id))
}

func scanSchedule(scanner interface {
	Scan(dest ...any) error
}) (ScheduleSpec, error) {
	var schedule ScheduleSpec
	var enabled int
	var createdAt, updatedAt string
	if err := scanner.Scan(&schedule.ID, &schedule.Workspace, &schedule.TriggerKind, &schedule.Trigger, &schedule.Prompt, &schedule.Timezone, &schedule.QuietHoursStart, &schedule.QuietHoursEnd, &enabled, &schedule.SchemaVersion, &createdAt, &updatedAt); err != nil {
		return ScheduleSpec{}, err
	}
	schedule.Enabled = enabled != 0
	schedule.CreatedAt = parseTime(createdAt)
	schedule.UpdatedAt = parseTime(updatedAt)
	return schedule, nil
}
