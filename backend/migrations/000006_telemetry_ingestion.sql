ALTER TABLE drones ADD CONSTRAINT drones_organisation_id_id_key UNIQUE (organisation_id, id);
ALTER TABLE drones ADD COLUMN flight_time_seconds bigint NOT NULL DEFAULT 0 CHECK (flight_time_seconds >= 0);
ALTER TABLE drones ADD COLUMN battery_cycles bigint NOT NULL DEFAULT 0 CHECK (battery_cycles >= 0);
ALTER TABLE drones ADD COLUMN fault_flags text[] NOT NULL DEFAULT '{}' CHECK (cardinality(fault_flags) <= 32);

-- The ordinary-table identity key spans all time chunks: event IDs cannot be
-- reused with a different timestamp/payload to evade hypertable uniqueness.
CREATE TABLE telemetry_receipts (
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    drone_id uuid NOT NULL,
    source text NOT NULL CHECK (source IN ('dji', 'ardupilot', 'csv')),
    event_id text NOT NULL CHECK (event_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$'),
    observed_at timestamptz NOT NULL,
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (organisation_id, drone_id, source, event_id),
    UNIQUE (organisation_id, drone_id, source, event_id, observed_at),
    FOREIGN KEY (organisation_id, drone_id) REFERENCES drones(organisation_id, id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE telemetry_samples (
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    drone_id uuid NOT NULL,
    source text NOT NULL CHECK (source IN ('dji', 'ardupilot', 'csv')),
    event_id text NOT NULL CHECK (event_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$'),
    observed_at timestamptz NOT NULL CHECK (observed_at >= '1970-01-01T00:00:00Z'),
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    status text NOT NULL CHECK (status IN ('available','charging','in_mission','grounded','unknown')),
    flight_time_seconds bigint NOT NULL CHECK (flight_time_seconds >= 0),
    battery_cycles bigint NOT NULL CHECK (battery_cycles >= 0),
    battery_percent double precision CHECK (battery_percent BETWEEN 0 AND 100),
    latitude double precision CHECK (latitude BETWEEN -90 AND 90),
    longitude double precision CHECK (longitude BETWEEN -180 AND 180),
    altitude_m double precision CHECK (altitude_m BETWEEN -12000 AND 100000),
    speed_mps double precision CHECK (speed_mps BETWEEN 0 AND 500),
    fault_flags text[] NOT NULL DEFAULT '{}' CHECK (cardinality(fault_flags) <= 32),
    CHECK ((latitude IS NULL) = (longitude IS NULL)),
    PRIMARY KEY (organisation_id, drone_id, observed_at),
    FOREIGN KEY (organisation_id, drone_id) REFERENCES drones(organisation_id, id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (organisation_id, drone_id, source, event_id, observed_at)
        REFERENCES telemetry_receipts(organisation_id, drone_id, source, event_id, observed_at)
        DEFERRABLE INITIALLY DEFERRED
);
SELECT public.create_hypertable('telemetry_samples', public.by_range('observed_at', INTERVAL '1 day'));
CREATE INDEX telemetry_samples_drone_time_idx ON telemetry_samples (organisation_id, drone_id, observed_at DESC);

CREATE TABLE telemetry_rejections (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    drone_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    reason text NOT NULL CHECK (reason IN ('invalid_sample','event_conflict','timestamp_conflict','vendor_mismatch','counter_regression','body_too_large')),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
    -- No raw vendor payload, imagery, or arbitrary error strings are retained.
);
CREATE INDEX telemetry_rejections_drone_time_idx ON telemetry_rejections (organisation_id, drone_id, created_at DESC);

CREATE TRIGGER telemetry_samples_append_only BEFORE UPDATE OR DELETE ON telemetry_samples
    FOR EACH ROW EXECUTE FUNCTION protect_audit_events();
CREATE TRIGGER telemetry_receipts_append_only BEFORE UPDATE OR DELETE ON telemetry_receipts
    FOR EACH ROW EXECUTE FUNCTION protect_audit_events();
CREATE TRIGGER telemetry_rejections_append_only BEFORE UPDATE OR DELETE ON telemetry_rejections
    FOR EACH ROW EXECUTE FUNCTION protect_audit_events();
CREATE TRIGGER telemetry_samples_no_truncate BEFORE TRUNCATE ON telemetry_samples
    FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_events_truncate();
CREATE TRIGGER telemetry_receipts_no_truncate BEFORE TRUNCATE ON telemetry_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_events_truncate();
CREATE TRIGGER telemetry_rejections_no_truncate BEFORE TRUNCATE ON telemetry_rejections
    FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_events_truncate();
