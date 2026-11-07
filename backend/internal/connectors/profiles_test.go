package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesValidationAndTenantScope(t *testing.T) {
	valid := Profile{ID: "recording", Label: "Recording", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "fixture.csv"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	live := Profile{ID: "live", Label: "Live", Source: "dji", Vendor: "dji", Endpoint: "https://vendor.example", OrganisationID: "11111111-1111-4111-8111-111111111111", CredentialRef: "tenant-one"}
	if err := live.Validate(); err != nil {
		t.Fatal(err)
	}
	if live.Allowed("22222222-2222-4222-8222-222222222222", "dji") {
		t.Fatal("live profile shared across tenants")
	}
	for _, change := range []func(*Profile){func(p *Profile) { p.OrganisationID = "" }, func(p *Profile) { p.Endpoint = "http://vendor.example" }, func(p *Profile) { p.Endpoint = "https://user:secret@vendor.example" }, func(p *Profile) { p.Endpoint = "https://vendor.example?token=secret" }} {
		p := live
		change(&p)
		if p.Validate() == nil {
			t.Fatalf("accepted invalid profile %+v", p)
		}
	}
	raw, _ := json.Marshal(live.Summary())
	if strings.Contains(string(raw), "vendor.example") || strings.Contains(string(raw), "tenant-one") {
		t.Fatal("private profile data leaked")
	}
}
func TestLoadProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(path, []byte(`[{"id":"demo","label":"Demo","source":"csv","vendor":"dji","simulated":true,"csv_path":"sample.csv"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	profiles, err := LoadProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if profiles["demo"].CSVPath != filepath.Join(dir, "sample.csv") {
		t.Fatal("relative profile path not resolved")
	}
	_ = os.WriteFile(path, []byte(`[] {}`), 0600)
	if _, err = LoadProfiles(path); err == nil {
		t.Fatal("accepted extra JSON")
	}
}
