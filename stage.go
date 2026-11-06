package pipeline

import (
	"context"

	"aerosync/telemetry-pipeline/telemetry"
)

// Stage is the shared contract for a synchronous telemetry processing stage.
// Stages may return a modified record or an error. A nil record means the
// record was intentionally dropped.
type Stage interface {
	Name() string
	Process(ctx context.Context, record telemetry.RawRecord) (telemetry.RawRecord, error)
}
