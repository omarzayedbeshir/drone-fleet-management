package drones

import (
	"strings"
	"testing"
)

func TestMetadataValidation(t *testing.T) {
	valid := Metadata{Name: "  Survey drone  ", Vendor: " DJI ", Model: " Mavic ", SerialNumber: " AbC-123 "}
	if err := valid.NormalizeValidate(); err != nil {
		t.Fatal(err)
	}
	if valid.Name != "Survey drone" || valid.Vendor != "dji" || valid.Model != "Mavic" || valid.SerialNumber != "AbC-123" {
		t.Fatalf("bad normalization: %+v", valid)
	}
	for _, patch := range []Patch{{"name": ""}, {"name": " \t "}, {"model": strings.Repeat("é", 201)}, {"serial_number": "SN\x00"}, {"vendor": "csv"}, {"vendor": "unsupported"}, {"name": "hello\nworld"}, {"status": "available"}, {"organisation_id": "other"}, {"last_telemetry_at": "now"}, {}} {
		if err := patch.NormalizeValidate(); err == nil {
			t.Fatalf("accepted invalid patch: %+v", patch)
		}
	}
	unicode := Patch{"name": strings.Repeat("é", 200)}
	if err := unicode.NormalizeValidate(); err != nil {
		t.Fatal(err)
	}
}
func TestPartialUpdatePreservesOtherFields(t *testing.T) {
	before := Drone{Name: "Old", Vendor: "dji", Model: "Model", SerialNumber: "CaseSensitive", Status: "grounded"}
	patch := Patch{"name": " New "}
	if err := patch.NormalizeValidate(); err != nil {
		t.Fatal(err)
	}
	after := patch.Apply(before)
	if after.Name != "New" || after.Vendor != before.Vendor || after.Model != before.Model || after.SerialNumber != before.SerialNumber {
		t.Fatal("partial update overwrote other metadata")
	}
}
