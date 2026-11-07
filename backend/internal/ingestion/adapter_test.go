package ingestion

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/dji"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/credentials"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/mockfeed"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

func TestRecordedSourcePrivacyNormalizationRetry(t *testing.T) {
	source, err := csvreplay.New(csvreplay.Config{Vendor: "dji", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("device_sn,timestamp,latitude,longitude,height,battery_capacity_percent,total_flight_time,battery_loop_times,camera_frame_b64\nD1,2026-11-07T08:00:00Z,30,31,50,1,3600,20,SECRET\nD2,2026-11-07T08:00:01Z,30,31,50,80,3601,20,SECRET\nD1,2026-11-07T08:00:02Z,north,31,50,80,3602,20,SECRET\nD1,2026-11-07T08:00:03Z,30,31,50,80,3603,20,SECRET\n")), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := New(Config{Source: source, Serial: "D1", Vendor: "dji", Capacity: 1, RetryMin: time.Millisecond, RetryMax: time.Millisecond})
	attempts := 0
	var first telemetry.Sample
	var got []telemetry.Sample
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = a.Run(ctx, func(_ context.Context, s telemetry.Sample) error {
		attempts++
		if attempts == 1 {
			first = s
			return errors.New("temporary outage")
		}
		if attempts == 2 && !reflect.DeepEqual(first, s) {
			t.Fatal("retry changed sample")
		}
		got = append(got, s)
		return nil
	})
	if err != nil || len(got) != 2 || attempts != 3 || got[0].Source != "csv" || *got[0].BatteryPercent != 1 || a.Health().Rejected != 1 {
		t.Fatalf("result %+v attempts %d health %+v err %v", got, attempts, a.Health(), err)
	}
}
func TestMockHTTPSourceUsesSameContract(t *testing.T) {
	feed := mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"}, IncludeImagery: true})
	server := httptest.NewServer(mockfeed.NewServer(feed, mockfeed.ServerConfig{Token: "test-token"}))
	defer server.Close()
	source, err := dji.New(dji.Config{BaseURL: server.URL, Credentials: credentials.StaticProvider{"dji": {"api_key": "test-token"}}, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := New(Config{Source: source, Serial: "D1", Vendor: "dji", Simulated: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	count := 0
	err = a.Run(ctx, func(_ context.Context, s telemetry.Sample) error {
		count++
		if s.Source != "csv" || s.BatteryCycles <= 0 || s.FlightTimeSeconds <= 0 {
			t.Fatalf("invalid mock normalization %+v", s)
		}
		cancel()
		return nil
	})
	if err != nil || count != 1 {
		t.Fatalf("count %d err %v", count, err)
	}
}

type brokenSource struct {
	calls     int
	permanent bool
}

func (s *brokenSource) Name() string { return "broken" }
func (s *brokenSource) Connect(context.Context) error {
	s.calls++
	if s.permanent {
		return vendor.ErrPermanent
	}
	if s.calls == 1 {
		return errors.New("temporary")
	}
	return nil
}
func (s *brokenSource) Subscribe(context.Context, chan<- vendor.RawRecord) error { return nil }
func (s *brokenSource) Close() error                                             { return nil }
func (s *brokenSource) Health() vendor.Health                                    { return vendor.Health{} }
func TestSourceReconnectAndPermanentFailure(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		s := &brokenSource{permanent: permanent}
		a, _ := New(Config{Source: s, Serial: "D1", Vendor: "dji", RetryMin: time.Millisecond, RetryMax: time.Millisecond})
		err := a.Run(context.Background(), func(context.Context, telemetry.Sample) error { return nil })
		if permanent {
			if !errors.Is(err, vendor.ErrPermanent) || s.calls != 1 {
				t.Fatalf("permanent %v calls %d", err, s.calls)
			}
		} else if err != nil || s.calls != 2 {
			t.Fatalf("retry %v calls %d", err, s.calls)
		}
	}
}
