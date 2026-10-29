package drones

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNotFound = errors.New("drone not found")
	ErrConflict = errors.New("drone conflicts with an existing record")
)

type Drone struct {
	ID              string     `json:"id"`
	OrganisationID  string     `json:"organisation_id"`
	Name            string     `json:"name"`
	Vendor          string     `json:"vendor"`
	Model           string     `json:"model"`
	SerialNumber    string     `json:"serial_number"`
	Status          string     `json:"status"`
	LastTelemetryAt *time.Time `json:"last_telemetry_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Metadata struct {
	Name         string `json:"name"`
	Vendor       string `json:"vendor"`
	Model        string `json:"model"`
	SerialNumber string `json:"serial_number"`
}

type Patch map[string]string

type Filter struct {
	Limit  int
	Offset int
	Vendor string
	Status string
	Query  string
}

type Store interface {
	Create(context.Context, string, string, Metadata) (Drone, error)
	List(context.Context, string, Filter) ([]Drone, bool, error)
	Get(context.Context, string, string) (Drone, error)
	Update(context.Context, string, string, string, Patch) (Drone, error)
	Delete(context.Context, string, string, string) error
}

func ValidVendor(value string) bool { return value == "dji" || value == "ardupilot" }
func ValidStatus(value string) bool {
	switch value {
	case "available", "charging", "in_mission", "grounded", "unknown":
		return true
	}
	return false
}

func normalizeField(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 200 {
		return "", fmt.Errorf("%s must contain 1–200 characters", field)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", fmt.Errorf("%s cannot contain control characters", field)
		}
	}
	if field == "vendor" {
		value = strings.ToLower(value)
		if !ValidVendor(value) {
			return "", errors.New("vendor must be dji or ardupilot")
		}
	}
	return value, nil
}

func (m *Metadata) NormalizeValidate() error {
	fields := []struct {
		name  string
		value *string
	}{
		{"name", &m.Name}, {"vendor", &m.Vendor}, {"model", &m.Model}, {"serial_number", &m.SerialNumber},
	}
	for _, field := range fields {
		value, err := normalizeField(field.name, *field.value)
		if err != nil {
			return err
		}
		*field.value = value
	}
	return nil
}

func (p Patch) NormalizeValidate() error {
	if len(p) == 0 {
		return errors.New("provide at least one of name, vendor, model, or serial_number")
	}
	for field, value := range p {
		switch field {
		case "name", "vendor", "model", "serial_number":
		default:
			return fmt.Errorf("field %q is not editable", field)
		}
		normalized, err := normalizeField(field, value)
		if err != nil {
			return err
		}
		p[field] = normalized
	}
	return nil
}

func (p Patch) Apply(d Drone) Metadata {
	m := Metadata{Name: d.Name, Vendor: d.Vendor, Model: d.Model, SerialNumber: d.SerialNumber}
	for field, value := range p {
		switch field {
		case "name":
			m.Name = value
		case "vendor":
			m.Vendor = value
		case "model":
			m.Model = value
		case "serial_number":
			m.SerialNumber = value
		}
	}
	return m
}
