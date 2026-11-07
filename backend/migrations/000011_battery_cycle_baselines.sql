-- Source provenance is independent of telemetry freshness and operational status.
-- Existing totals are deliberately unverified; the migration invents no readings.
ALTER TABLE drones ADD COLUMN battery_cycles_source text NOT NULL DEFAULT 'unknown'
 CHECK (battery_cycles_source IN ('unknown','telemetry','simulated','manual'));
ALTER TABLE drones ADD COLUMN battery_cycles_recorded_at timestamptz;
ALTER TABLE drones ADD COLUMN battery_cycles_note text NOT NULL DEFAULT '' CHECK (length(battery_cycles_note)<=500);
ALTER TABLE drones ADD COLUMN cycle_baseline_revision bigint NOT NULL DEFAULT 0 CHECK (cycle_baseline_revision>=0);

-- Manual readings are durable temporal counter boundaries, including after newer
-- telemetry changes the current provenance. Like audit history, retain these
-- records after drone/user removal; organisation removal is the sole exception.
CREATE TABLE battery_cycle_readings (
 organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
 drone_id uuid NOT NULL,
 revision bigint NOT NULL CHECK (revision>0),
 actor_id uuid NOT NULL,
 total bigint NOT NULL CHECK (total>=0),
 reason text NOT NULL CHECK (length(reason) BETWEEN 5 AND 500),
 recorded_at timestamptz NOT NULL,
 PRIMARY KEY (organisation_id,drone_id,revision)
);
CREATE INDEX battery_cycle_readings_time_idx ON battery_cycle_readings(organisation_id,drone_id,recorded_at);
CREATE TRIGGER battery_cycle_readings_append_only BEFORE UPDATE OR DELETE ON battery_cycle_readings
 FOR EACH ROW EXECUTE FUNCTION protect_audit_events();
CREATE TRIGGER battery_cycle_readings_no_truncate BEFORE TRUNCATE ON battery_cycle_readings
 FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_events_truncate();
