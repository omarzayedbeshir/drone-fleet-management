package ardupilot

import (
	"context"
	"errors"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func (a *Adapter) deliverBatch(ctx context.Context, queue <-chan telemetry.Sample, emit func(context.Context, []telemetry.Sample) ([]error, error)) {
	for {
		var first telemetry.Sample
		select {
		case <-ctx.Done():
			return
		case value, ok := <-queue:
			if !ok {
				return
			}
			first = value
		}
		batch := []telemetry.Sample{first}
		timer := time.NewTimer(2 * time.Second)
		closed := false
	collect:
		for len(batch) < telemetry.MaxBatch {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				break collect
			case value, ok := <-queue:
				if !ok {
					closed = true
					break collect
				}
				batch = append(batch, value)
			}
		}
		timer.Stop()
		delay := a.config.RetryMin
		for {
			outcomes, err := emit(ctx, batch)
			if err == nil && len(outcomes) != len(batch) {
				err = errors.New("batch callback returned an incorrect outcome count")
			}
			if err == nil {
				a.change(func(h *adapters.Health) {
					h.LastError = ""
					for _, outcome := range outcomes {
						if outcome != nil {
							h.Rejected++
							h.LastError = outcome.Error()
						} else {
							h.Accepted++
							h.LastEmitted = time.Now()
						}
					}
				})
				break
			}
			if ctx.Err() != nil {
				return
			}
			var permanent *adapters.PermanentError
			var rejection *telemetry.Rejection
			if errors.As(err, &permanent) || errors.As(err, &rejection) || errors.Is(err, telemetry.ErrNotFound) {
				a.change(func(h *adapters.Health) { h.Rejected += uint64(len(batch)); h.LastError = err.Error() })
				break
			}
			a.change(func(h *adapters.Health) { h.LastError = "Ingestion unavailable; retrying buffered batch" })
			retry := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				retry.Stop()
				return
			case <-retry.C:
			}
			delay = min(delay*2, a.config.RetryMax)
		}
		if closed {
			return
		}
	}
}
