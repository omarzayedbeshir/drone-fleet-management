package httpapi

import (
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"net/http/httptest"
	"testing"
)

func TestDroneFilters(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=101", "limit=abc", "offset=-1", "offset=1000001", "vendor=csv", "status=flying", "organisation_id=other", "limit=1&limit=2", "q=%00", "q=%GG"} {
		r := httptest.NewRequest("GET", "/api/v1/drones?"+query, nil)
		f := drones.Filter{Limit: 20}
		if _, err := parseDroneFilter(r, &f); err == nil {
			t.Fatalf("accepted query %s", query)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/drones?limit=5&offset=10&vendor=dji&status=unknown&q=Survey", nil)
	f := drones.Filter{Limit: 20}
	if _, err := parseDroneFilter(r, &f); err != nil {
		t.Fatal(err)
	}
	if f.Limit != 5 || f.Offset != 10 || f.Vendor != "dji" || f.Status != "unknown" || f.Query != "Survey" {
		t.Fatalf("bad filter: %+v", f)
	}
}
func TestDroneIDs(t *testing.T) {
	for _, id := range []string{"bad", "00000000000000000000000000000000", "00000000X0000X0000X0000X000000000000"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		if _, ok := droneID(w, r); ok || w.Code != 400 {
			t.Fatalf("accepted invalid id %q", id)
		}
	}
}
