package store

import (
	"errors"
	"strings"
	"time"
)

type ScheduleTriggerKind string

const (
	ScheduleTriggerOneShot  ScheduleTriggerKind = "one_shot"
	ScheduleTriggerInterval ScheduleTriggerKind = "interval"
	ScheduleTriggerCron     ScheduleTriggerKind = "cron"
)

type ScheduleQuietHours struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type ScheduleSpec struct {
	ID              string              `json:"id"`
	Workspace       string              `json:"workspace,omitempty"`
	TriggerKind     ScheduleTriggerKind `json:"trigger_kind"`
	Trigger         string              `json:"trigger"`
	Prompt          string              `json:"prompt"`
	Timezone        string              `json:"timezone"`
	QuietHoursStart string              `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd   string              `json:"quiet_hours_end,omitempty"`
	Enabled         bool                `json:"enabled"`
	SchemaVersion   int                 `json:"schema_version"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

type CreateScheduleRequest struct {
	ID              string              `json:"id"`
	Workspace       string              `json:"workspace,omitempty"`
	TriggerKind     ScheduleTriggerKind `json:"trigger_kind"`
	Trigger         string              `json:"trigger"`
	Prompt          string              `json:"prompt"`
	Timezone        string              `json:"timezone,omitempty"`
	QuietHoursStart string              `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd   string              `json:"quiet_hours_end,omitempty"`
	Enabled         *bool               `json:"enabled,omitempty"`
}

type UpdateScheduleRequest struct {
	Workspace   *string              `json:"workspace,omitempty"`
	TriggerKind *ScheduleTriggerKind `json:"trigger_kind,omitempty"`
	Trigger     *string              `json:"trigger,omitempty"`
	Prompt      *string              `json:"prompt,omitempty"`
	Timezone    *string              `json:"timezone,omitempty"`
	QuietHours  *ScheduleQuietHours  `json:"quiet_hours,omitempty"`
	Enabled     *bool                `json:"enabled,omitempty"`
}

type ScheduleListOptions struct {
	Workspace       string
	Limit           int
	IncludeDisabled bool
}

func (s *Store) CreateSchedule(request CreateScheduleRequest) (ScheduleSpec, error) {
	schedule, err := normalizeScheduleForCreate(request)
	if err != nil {
		return ScheduleSpec{}, err
	}
	db, err := s.OpenDB()
	if err != nil {
		return ScheduleSpec{}, err
	}
	defer db.Close()
	now := time.Now().UTC()
	schedule.SchemaVersion = CurrentSchemaVersion
	schedule.CreatedAt = now
	schedule.UpdatedAt = now
	_, err = db.Exec(`
		INSERT INTO schedules (id, workspace, trigger_kind, trigger, prompt, timezone, quiet_hours_start, quiet_hours_end, enabled, schema_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, schedule.ID, schedule.Workspace, string(schedule.TriggerKind), schedule.Trigger, schedule.Prompt, schedule.Timezone, schedule.QuietHoursStart, schedule.QuietHoursEnd, boolInt(schedule.Enabled), schedule.SchemaVersion, formatTime(schedule.CreatedAt), formatTime(schedule.UpdatedAt))
	if err != nil {
		return ScheduleSpec{}, err
	}
	return schedule, nil
}

func (s *Store) GetSchedule(id string) (ScheduleSpec, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ScheduleSpec{}, errors.New("schedule id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return ScheduleSpec{}, err
	}
	defer db.Close()
	return getSchedule(db, id)
}

func (s *Store) ListSchedules(options ScheduleListOptions) ([]ScheduleSpec, error) {
	db, err := s.OpenDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return querySchedules(db, options)
}

func (s *Store) UpdateSchedule(id string, request UpdateScheduleRequest) (ScheduleSpec, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ScheduleSpec{}, errors.New("schedule id is required")
	}
	db, err := s.OpenDB()
	if err != nil {
		return ScheduleSpec{}, err
	}
	defer db.Close()
	schedule, err := getSchedule(db, id)
	if err != nil {
		return ScheduleSpec{}, err
	}
	if request.Workspace != nil {
		schedule.Workspace = strings.TrimSpace(*request.Workspace)
	}
	if request.TriggerKind != nil {
		kind, err := normalizeScheduleTriggerKind(*request.TriggerKind)
		if err != nil {
			return ScheduleSpec{}, err
		}
		schedule.TriggerKind = kind
	}
	if request.Trigger != nil {
		schedule.Trigger = strings.TrimSpace(*request.Trigger)
	}
	if request.Prompt != nil {
		prompt := strings.TrimSpace(*request.Prompt)
		if prompt == "" {
			return ScheduleSpec{}, errors.New("schedule prompt is required")
		}
		schedule.Prompt = prompt
	}
	if request.Timezone != nil {
		timezone, err := normalizeScheduleTimezone(*request.Timezone)
		if err != nil {
			return ScheduleSpec{}, err
		}
		schedule.Timezone = timezone
	}
	if request.QuietHours != nil {
		start, end, err := normalizeQuietHours(request.QuietHours.Start, request.QuietHours.End)
		if err != nil {
			return ScheduleSpec{}, err
		}
		schedule.QuietHoursStart = start
		schedule.QuietHoursEnd = end
	}
	if request.Enabled != nil {
		schedule.Enabled = *request.Enabled
	}
	if err := validateScheduleTrigger(schedule.TriggerKind, schedule.Trigger); err != nil {
		return ScheduleSpec{}, err
	}
	schedule.UpdatedAt = time.Now().UTC()
	_, err = db.Exec(`
		UPDATE schedules
		SET workspace = ?, trigger_kind = ?, trigger = ?, prompt = ?, timezone = ?, quiet_hours_start = ?, quiet_hours_end = ?, enabled = ?, schema_version = ?, updated_at = ?
		WHERE id = ?
	`, schedule.Workspace, string(schedule.TriggerKind), schedule.Trigger, schedule.Prompt, schedule.Timezone, schedule.QuietHoursStart, schedule.QuietHoursEnd, boolInt(schedule.Enabled), CurrentSchemaVersion, formatTime(schedule.UpdatedAt), schedule.ID)
	if err != nil {
		return ScheduleSpec{}, err
	}
	return schedule, nil
}

func (s *Store) SetScheduleEnabled(id string, enabled bool) (ScheduleSpec, error) {
	return s.UpdateSchedule(id, UpdateScheduleRequest{Enabled: &enabled})
}
