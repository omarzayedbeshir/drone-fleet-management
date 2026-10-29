package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	SystemAdministrator   Role = "system_administrator"
	FleetManager          Role = "fleet_manager"
	Pilot                 Role = "pilot"
	MaintenanceTechnician Role = "maintenance_technician"
)

func (r Role) Valid() bool {
	return r == SystemAdministrator || r == FleetManager || r == Pilot || r == MaintenanceTechnician
}

type User struct {
	ID             string  `json:"id"`
	OrganisationID *string `json:"organisation_id"`
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	Role           Role    `json:"role"`
}

type Credentials struct {
	User         User
	PasswordHash string
	Active       bool
}

var ErrUnauthenticated = errors.New("invalid or expired authentication")

// Store keeps authentication independent of HTTP. Implementations must check
// active users and enforce expiry when resolving a session.
type Store interface {
	FindCredentials(context.Context, string) (Credentials, error)
	CreateSession(context.Context, Credentials, string) (User, error)
	ResolveSession(context.Context, string) (User, error)
	DeleteSession(context.Context, string) error
	ChangePassword(context.Context, string, string, string) error
}

func NormalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", errors.New("enter a valid email address")
	}
	return value, nil
}

func HashPassword(password string) (string, error) {
	// bcrypt accepts at most 72 bytes; never silently truncate a password.
	if len(password) < 12 || len(password) > 72 {
		return "", errors.New("password must be between 12 and 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(hash), err
}

func NewToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func TokenHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func ValidToken(token string) bool {
	bytes, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(bytes) == 32
}
