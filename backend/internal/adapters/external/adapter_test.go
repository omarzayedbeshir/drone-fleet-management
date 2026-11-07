package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

// The test binary stands in for an independently compiled vendor module.
func TestModuleProcess(t *testing.T) {
	scenario := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(scenario, "_module_") {
		return
	}
	var binding Binding
	if json.NewDecoder(os.Stdin).Decode(&binding) != nil || binding.Protocol != "aerosync.telemetry.v1" || binding.Serial != "D1" || os.Getenv("DATABASE_URL") != "" {
		os.Exit(8)
	}
	if scenario == "_module_fail" {
		fmt.Fprintln(os.Stderr, "PRIVATE source diagnostics")
		os.Exit(7)
	}
	if scenario == "_module_malformed" {
		fmt.Println(strings.Repeat("x", telemetry.MaxBody+1000))
		fmt.Println(`{"source":"acme","camera_image":"PRIVATE"}`)
	}
	sample := telemetry.Sample{Source: binding.Vendor, EventID: "module-one", ObservedAt: time.Now().UTC().Add(-time.Minute), Status: "available", FlightTimeSeconds: binding.FlightBaseline + 1, BatteryCycles: binding.CycleBaseline, FaultFlags: []string{}, Latitude: floatptr(30.85), Longitude: floatptr(31.05)}
	_ = json.NewEncoder(os.Stdout).Encode(sample)
	if scenario == "_module_hang" {
		time.Sleep(time.Hour)
	}
	os.Exit(0)
}
func floatptr(v float64) *float64 { return &v }
func module(t *testing.T, scenario string) *Adapter {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Executable: exe, Arguments: []string{"-test.run=^TestModuleProcess$", "--", "_module_" + scenario}, Vendor: "acme", Serial: "D1", QueueSize: 1, FlightBaseline: 10, CycleBaseline: 2})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestModuleContractRetryAndPrivacy(t *testing.T) {
	t.Setenv("DATABASE_URL", "must-not-be-inherited")
	a := module(t, "malformed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	var original telemetry.Sample
	err := a.Run(ctx, func(_ context.Context, s telemetry.Sample) error {
		calls++
		if calls == 1 {
			original = s
			return errors.New("temporary storage failure")
		}
		if !reflect.DeepEqual(original, s) {
			t.Fatal("retry changed record")
		}
		if s.Source != "acme" || s.FlightTimeSeconds != 11 || s.BatteryCycles != 2 {
			t.Fatalf("binding/schema mismatch %+v", s)
		}
		return nil
	})
	if err != nil || calls != 2 || a.Health().Accepted != 1 || a.Health().Rejected != 2 {
		t.Fatalf("calls %d health %+v err %v", calls, a.Health(), err)
	}
}
func TestModuleCancellation(t *testing.T) {
	a := module(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Run(ctx, func(context.Context, telemetry.Sample) error { cancel(); return nil }); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("module shutdown was not bounded")
	}
}
func TestModuleExitFailure(t *testing.T) {
	a := module(t, "fail")
	if err := a.Run(context.Background(), func(context.Context, telemetry.Sample) error { return nil }); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("unexpected error %v", err)
	}
}
