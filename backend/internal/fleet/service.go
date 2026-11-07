// Package fleet implements fleet use cases independently of HTTP and SQL.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/catalog"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
)

var ErrForbidden = errors.New("fleet access is not permitted")
var ErrInvalid = errors.New("invalid fleet input")

// Service accepts an authenticated identity, never a client-selected tenant.
// The caller resolves active sessions; repositories retain atomic audit writes
// and database-dependent checks, including vendor installation and uniqueness.
type Service struct{ repository drones.Store }

func New(repository drones.Store) *Service { return &Service{repository: repository} }

func scope(user auth.User, write bool) (string, error) {
	if user.ID == "" || user.OrganisationID == nil || *user.OrganisationID == "" {
		return "", ErrForbidden
	}
	switch user.Role {
	case auth.FleetManager:
		return *user.OrganisationID, nil
	case auth.Pilot, auth.MaintenanceTechnician:
		if !write {
			return *user.OrganisationID, nil
		}
	}
	return "", ErrForbidden
}
func invalid(err error) error { return fmt.Errorf("%w: %s", ErrInvalid, err) }

func (s *Service) Create(ctx context.Context, user auth.User, input drones.Metadata) (drones.Drone, error) {
	org, err := scope(user, true)
	if err != nil {
		return drones.Drone{}, err
	}
	if err := input.NormalizeValidate(); err != nil {
		return drones.Drone{}, invalid(err)
	}
	return s.repository.Create(ctx, org, user.ID, input)
}
func (s *Service) Get(ctx context.Context, user auth.User, id string) (drones.Drone, error) {
	org, err := scope(user, false)
	if err != nil {
		return drones.Drone{}, err
	}
	return s.repository.Get(ctx, org, id)
}
func (s *Service) Update(ctx context.Context, user auth.User, id string, input drones.Patch) (drones.Drone, error) {
	org, err := scope(user, true)
	if err != nil {
		return drones.Drone{}, err
	}
	// Normalize a copy: callers may reuse their request or share it across retries.
	patch := make(drones.Patch, len(input))
	for key, value := range input {
		patch[key] = value
	}
	if err := patch.NormalizeValidate(); err != nil {
		return drones.Drone{}, invalid(err)
	}
	return s.repository.Update(ctx, org, user.ID, id, patch)
}
func (s *Service) Delete(ctx context.Context, user auth.User, id string) error {
	org, err := scope(user, true)
	if err != nil {
		return err
	}
	return s.repository.Delete(ctx, org, user.ID, id)
}

type Page struct {
	Drones  []drones.Drone
	Vendors []catalog.Vendor
	More    bool
}

func (s *Service) List(ctx context.Context, user auth.User, filter drones.Filter) (Page, error) {
	org, err := scope(user, false)
	if err != nil {
		return Page{}, err
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > 1000000 ||
		(filter.Vendor != "" && !drones.ValidVendor(filter.Vendor)) || (filter.Status != "" && !drones.ValidStatus(filter.Status)) ||
		!utf8.ValidString(filter.Query) || utf8.RuneCountInString(filter.Query) > 200 || strings.ContainsRune(filter.Query, 0) {
		return Page{}, invalid(errors.New("invalid fleet filters"))
	}
	items, more, err := s.repository.List(ctx, org, filter)
	if err != nil {
		return Page{}, err
	}
	vendors := append([]catalog.Vendor(nil), catalog.Builtins...)
	if provider, ok := s.repository.(interface {
		Catalog(context.Context) ([]catalog.Vendor, error)
	}); ok {
		vendors, err = provider.Catalog(ctx)
		if err != nil {
			return Page{}, err
		}
	}
	return Page{Drones: items, Vendors: vendors, More: more}, nil
}
