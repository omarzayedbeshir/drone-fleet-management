package ardupilot

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

// Opt in against real ArduPilot SITL. No arming/movement commands are sent.
func TestSITLIntegration(t *testing.T) {
	endpoint := os.Getenv("SITL_MAVLINK_ENDPOINT")
	if endpoint == "" {
		t.Skip("set SITL_MAVLINK_ENDPOINT to a running ArduPilot SITL TCP/UDP endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adapter, err := New(Config{Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	err = adapter.Run(ctx, func(_ context.Context, sample telemetry.Sample) error {
		raw, err := json.Marshal(sample)
		if err != nil {
			return err
		}
		if _, err := telemetry.Decode(raw, time.Now()); err != nil {
			return err
		}
		t.Logf("normalized SITL: %s", raw)
		if sample.Latitude != nil && sample.Longitude != nil && sample.AltitudeM != nil {
			accepted++
		}
		if accepted >= 3 {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted < 3 {
		t.Fatalf("expected three GPS-backed SITL observations, got %d; health=%+v", accepted, adapter.Health())
	}
}
