package normalize

import "github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"

// SchemaVersion identifies the documented common schema, not an extra wire field.
const SchemaVersion = "1.0"

type Telemetry = telemetry.Sample
