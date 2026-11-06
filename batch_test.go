package ardupilot

import (
	"context"
	"errors"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"reflect"
	"testing"
	"time"
)

func TestBatchRetryKeepsIdentityAndCountsOutcomes(t *testing.T) {
	a, err := New(Config{RetryMin: time.Millisecond, RetryMax: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	queue := make(chan telemetry.Sample, 3)
	for _, id := range []string{"one", "two", "three"} {
		queue <- telemetry.Sample{EventID: id}
	}
	close(queue)
	attempts := 0
	a.deliverBatch(context.Background(), queue, func(ctx context.Context, batch []telemetry.Sample) ([]error, error) {
		attempts++
		ids := []string{}
		for _, s := range batch {
			ids = append(ids, s.EventID)
		}
		if !reflect.DeepEqual(ids, []string{"one", "two", "three"}) {
			t.Fatalf("batch changed: %v", ids)
		}
		if attempts == 1 {
			return nil, errors.New("temporary database failure")
		}
		return []error{nil, &telemetry.Rejection{Code: "invalid_sample", Message: "invalid"}, nil}, nil
	})
	h := a.Health()
	if attempts != 2 || h.Accepted != 2 || h.Rejected != 1 {
		t.Fatalf("wrong accounting: %d %+v", attempts, h)
	}
}
