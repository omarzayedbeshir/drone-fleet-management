package missions

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
)

const (
	ErrNotFound        Error = "Mission not found"
	ErrDroneBusy       Error = "Drone already booked for this time"
	ErrPilotBusy       Error = "Pilot already booked for this time"
	ErrGrounded        Error = "Grounded drones cannot be scheduled"
	ErrPilotIneligible Error = "Assigned user must be an active pilot"
	ErrAssignment      Error = "Select a drone and pilot in your organisation"
	ErrCancelled       Error = "Cancelled missions cannot be edited"
	ErrForbidden       Error = "Pilots can only schedule themselves"
)

type Error string

func (e Error) Error() string { return string(e) }

type Input struct {
	DroneID            string    `json:"drone_id"`
	PilotID            string    `json:"pilot_id"`
	Location           string    `json:"location"`
	StartsAt           time.Time `json:"starts_at"`
	EndsAt             time.Time `json:"ends_at"`
	NearPopulatedArea  bool      `json:"near_populated_area"`
	SafetyRationale    *string   `json:"safety_rationale"`
	SafetyAcknowledged bool      `json:"safety_acknowledged"`
}

type Mission struct {
	Input
	ID             string     `json:"id"`
	OrganisationID string     `json:"organisation_id"`
	CancelledAt    *time.Time `json:"cancelled_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type ListItem struct {
	Mission
	DroneStatus string `json:"drone_status"`
}

type AssignmentOption struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Eligible bool   `json:"eligible"`
}

type AssignmentOptions struct {
	Drones []AssignmentOption `json:"drones"`
	Pilots []AssignmentOption `json:"pilots"`
}

type Store interface {
	Options(context.Context, auth.User) (AssignmentOptions, error)
	List(context.Context, auth.User, int, int) ([]ListItem, error)
	Save(context.Context, auth.User, string, Input) (Mission, error)
	Cancel(context.Context, auth.User, string) error
}

func ValidID(id string) bool {
	var uuid pgtype.UUID
	return len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' && uuid.Scan(id) == nil && uuid.Valid
}

func (i *Input) Normalize() error {
	if !ValidID(i.DroneID) || !ValidID(i.PilotID) {
		return errors.New("drone_id and pilot_id must be UUIDs")
	}
	i.DroneID, i.PilotID = strings.ToLower(i.DroneID), strings.ToLower(i.PilotID)
	i.StartsAt = i.StartsAt.UTC().Truncate(time.Microsecond)
	i.EndsAt = i.EndsAt.UTC().Truncate(time.Microsecond)
	if i.StartsAt.IsZero() || i.EndsAt.IsZero() || !i.StartsAt.Before(i.EndsAt) {
		return errors.New("starts_at and ends_at are required and must form a positive time window")
	}
	i.Location = strings.TrimSpace(i.Location)
	if !validText(i.Location, 500) {
		return errors.New("location must contain 1 to 500 characters and no null characters")
	}
	if i.NearPopulatedArea {
		if i.SafetyRationale == nil || !i.SafetyAcknowledged {
			return errors.New("flagged missions require a safety rationale and acknowledgement")
		}
		rationale := strings.TrimSpace(*i.SafetyRationale)
		if !validText(rationale, 2000) {
			return errors.New("safety_rationale must contain 1 to 2000 characters and no null characters")
		}
		i.SafetyRationale = &rationale
	} else if i.SafetyRationale != nil || i.SafetyAcknowledged {
		return errors.New("safety details are only allowed for missions flagged as near a populated area")
	}
	return nil
}

func validText(value string, max int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, 0)
}
