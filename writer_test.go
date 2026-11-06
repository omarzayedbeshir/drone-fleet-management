package telemetry

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestBatchBoundsBeforeDatabase(t *testing.T) {
	store := &PostgresStore{}
	for _, raws := range [][][]byte{nil, {}, make([][]byte, MaxBatch+1)} {
		if _, err := store.IngestBatch(context.Background(), "", "", "", raws); err == nil {
			t.Fatal("accepted an empty or oversized batch")
		}
	}
}

func TestCumulativeHistoryBounds(t *testing.T) {
	at := time.Now()
	previous := Sample{ObservedAt: at, FlightTimeSeconds: 3600, BatteryCycles: 10}
	for _, test := range []struct {
		name            string
		seconds, cycles int64
		delta           time.Duration
		conflict        bool
	}{
		{"newer totals", 4000, 11, time.Second, false},
		{"repeated totals", 3600, 10, time.Second, false},
		{"newer flight regression", 3599, 10, time.Second, true},
		{"newer cycle regression", 4000, 9, time.Second, true},
		{"late totals", 3500, 9, -time.Second, false},
		{"late flight contradiction", 3601, 9, -time.Second, true},
		{"late cycle contradiction", 3500, 11, -time.Second, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := Sample{ObservedAt: at.Add(test.delta), FlightTimeSeconds: test.seconds, BatteryCycles: test.cycles}
			if contradicts(sample, previous) != test.conflict {
				t.Fatal("incorrect temporal counter bound")
			}
		})
	}
}

func TestBatteryRatingValidation(t *testing.T) {
	rated, zero, negative := int64(500), int64(0), int64(-1)
	for _, rating := range []BatteryRating{{&rated, 0.8}, {nil, 0.8}, {&rated, 1}} {
		if err := rating.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, rating := range []BatteryRating{{&zero, 0.8}, {&negative, 0.8}, {&rated, 0}, {&rated, -1}, {&rated, 1.01}, {&rated, math.NaN()}, {&rated, math.Inf(1)}} {
		if err := rating.Validate(); err == nil {
			t.Fatalf("accepted invalid rating %+v", rating)
		}
	}
}
