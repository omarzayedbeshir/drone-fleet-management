package fleet

import (
	"context"
	"errors"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/catalog"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"testing"
)

type repository struct {
	calls      int
	org, actor string
	metadata   drones.Metadata
	patch      drones.Patch
	err        error
}

func (r *repository) Create(_ context.Context, o, a string, m drones.Metadata) (drones.Drone, error) {
	r.calls++
	r.org, r.actor, r.metadata = o, a, m
	return drones.Drone{}, r.err
}
func (r *repository) Get(_ context.Context, o, id string) (drones.Drone, error) {
	r.calls++
	r.org = o
	return drones.Drone{}, r.err
}
func (r *repository) Update(_ context.Context, o, a, id string, p drones.Patch) (drones.Drone, error) {
	r.calls++
	r.org, r.actor, r.patch = o, a, p
	return drones.Drone{}, r.err
}
func (r *repository) Delete(_ context.Context, o, a, id string) error {
	r.calls++
	r.org, r.actor = o, a
	return r.err
}
func (r *repository) List(_ context.Context, o string, f drones.Filter) ([]drones.Drone, bool, error) {
	r.calls++
	r.org = o
	return []drones.Drone{}, false, r.err
}
func identity(role auth.Role) auth.User {
	o := "own-tenant"
	return auth.User{ID: "actor", OrganisationID: &o, Role: role}
}
func TestServiceAccessWithoutHTTP(t *testing.T) {
	ctx := context.Background()
	for _, role := range []auth.Role{auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician, auth.SystemAdministrator, "untrusted"} {
		t.Run(string(role), func(t *testing.T) {
			r := &repository{}
			s := New(r)
			u := identity(role)
			writes := []func() error{
				func() error {
					_, e := s.Create(ctx, u, drones.Metadata{Name: "Aircraft", Vendor: "dji", Model: "Model", SerialNumber: "Serial"})
					return e
				},
				func() error { _, e := s.Update(ctx, u, "id", drones.Patch{"name": "Updated"}); return e },
				func() error { return s.Delete(ctx, u, "id") },
			}
			for _, call := range writes {
				e := call()
				if role == auth.FleetManager {
					if e != nil {
						t.Fatal(e)
					}
				} else if !errors.Is(e, ErrForbidden) {
					t.Fatalf("write allowed: %v", e)
				}
			}
			_, e := s.Get(ctx, u, "id")
			allowed := role == auth.FleetManager || role == auth.Pilot || role == auth.MaintenanceTechnician
			if allowed && (e != nil || r.org != "own-tenant") {
				t.Fatal("read scope lost", e)
			}
			if !allowed && (!errors.Is(e, ErrForbidden) || r.calls != 0) {
				t.Fatal("forbidden caller reached repository")
			}
		})
	}
	for _, u := range []auth.User{{}, {ID: "actor", Role: auth.FleetManager}, {OrganisationID: identity(auth.FleetManager).OrganisationID, Role: auth.FleetManager}} {
		r := &repository{}
		_, e := New(r).List(ctx, u, drones.Filter{Limit: 20})
		if !errors.Is(e, ErrForbidden) || r.calls != 0 {
			t.Fatal("incomplete identity reached repository")
		}
	}
}
func TestServiceValidationAndFailures(t *testing.T) {
	r := &repository{}
	s := New(r)
	ctx := context.Background()
	u := identity(auth.FleetManager)
	if _, e := s.Create(ctx, u, drones.Metadata{}); !errors.Is(e, ErrInvalid) || r.calls != 0 {
		t.Fatal("invalid create reached repository")
	}
	if _, e := s.Update(ctx, u, "id", drones.Patch{"status": "available"}); !errors.Is(e, ErrInvalid) || r.calls != 0 {
		t.Fatal("protected field reached repository")
	}
	if _, e := s.List(ctx, u, drones.Filter{Limit: 101}); !errors.Is(e, ErrInvalid) || r.calls != 0 {
		t.Fatal("invalid filter reached repository")
	}
	_, e := s.Create(ctx, u, drones.Metadata{Name: " Aircraft ", Vendor: " DJI ", Model: " Model ", SerialNumber: " Serial "})
	if e != nil || r.metadata.Vendor != "dji" || r.metadata.Name != "Aircraft" || r.actor != "actor" {
		t.Fatal("normalization or attribution lost")
	}
	p := drones.Patch{"name": " Updated "}
	_, e = s.Update(ctx, u, "id", p)
	if e != nil || p["name"] != " Updated " || r.patch["name"] != "Updated" {
		t.Fatal("request mutated or normalization lost")
	}
	for _, cause := range []error{drones.ErrConflict, drones.ErrUnsupported, errors.New("audit failed")} {
		r.err = cause
		if _, e := s.Create(ctx, u, r.metadata); !errors.Is(e, cause) {
			t.Fatal("repository error lost")
		}
	}
}

type catalogRepository struct {
	repository
	catalogErr error
}

func (r *catalogRepository) Catalog(context.Context) ([]catalog.Vendor, error) {
	return []catalog.Vendor{{Code: "new_vendor", Label: "New vendor"}}, r.catalogErr
}
func TestServiceCatalog(t *testing.T) {
	ctx := context.Background()
	u := identity(auth.Pilot)
	p, e := New(&repository{}).List(ctx, u, drones.Filter{Limit: 20})
	if e != nil || len(p.Vendors) != len(catalog.Builtins) {
		t.Fatal("builtin fallback lost")
	}
	r := &catalogRepository{}
	p, e = New(r).List(ctx, u, drones.Filter{Limit: 20})
	if e != nil || p.Vendors[0].Code != "new_vendor" {
		t.Fatal("installed vendor lost")
	}
	r.catalogErr = errors.New("unavailable")
	if _, e = New(r).List(ctx, u, drones.Filter{Limit: 20}); !errors.Is(e, r.catalogErr) {
		t.Fatal("catalog failure hidden")
	}
}
