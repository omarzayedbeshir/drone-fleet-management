-- Preserve the response timestamp for records written before the batch writer.
-- The migration runner executes this under its transaction and advisory lock.
ALTER TABLE telemetry_receipts ADD COLUMN sample_received_at timestamptz;
ALTER TABLE telemetry_receipts DISABLE TRIGGER telemetry_receipts_append_only;
UPDATE telemetry_receipts r SET sample_received_at = s.received_at
FROM telemetry_samples s
WHERE s.organisation_id=r.organisation_id AND s.drone_id=r.drone_id
  AND s.source=r.source AND s.event_id=r.event_id AND s.observed_at=r.observed_at;
ALTER TABLE telemetry_receipts ENABLE TRIGGER telemetry_receipts_append_only;
-- New writer receipts already share received_at with the sample. Null also
-- permits fallback for historical observations removed before this migration.
