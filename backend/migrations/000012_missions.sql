CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;

-- The tenant-scoped drone key is already installed by 000006_telemetry_ingestion.
ALTER TABLE users ADD CONSTRAINT users_organisation_id_id_key UNIQUE (organisation_id, id);

CREATE TABLE missions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    drone_id uuid NOT NULL,
    pilot_id uuid NOT NULL,
    location text NOT NULL CHECK (location = btrim(location) AND char_length(location) BETWEEN 1 AND 500),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    near_populated_area boolean NOT NULL DEFAULT false,
    safety_rationale text,
    safety_acknowledged boolean NOT NULL DEFAULT false,
    cancelled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organisation_id, drone_id) REFERENCES drones (organisation_id, id) DEFERRABLE,
    FOREIGN KEY (organisation_id, pilot_id) REFERENCES users (organisation_id, id) DEFERRABLE,
    CONSTRAINT missions_time_window_check CHECK (
        isfinite(starts_at) AND isfinite(ends_at) AND starts_at < ends_at
    ),
    CONSTRAINT missions_safety_check CHECK (
        (near_populated_area AND safety_acknowledged AND safety_rationale IS NOT NULL
            AND safety_rationale = btrim(safety_rationale)
            AND char_length(safety_rationale) BETWEEN 1 AND 2000)
        OR (NOT near_populated_area AND NOT safety_acknowledged AND safety_rationale IS NULL)
    ),
    CONSTRAINT missions_drone_overlap EXCLUDE USING gist (
        organisation_id WITH =, drone_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&
    ) WHERE (cancelled_at IS NULL),
    CONSTRAINT missions_pilot_overlap EXCLUDE USING gist (
        organisation_id WITH =, pilot_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&
    ) WHERE (cancelled_at IS NULL)
);

CREATE INDEX missions_organisation_starts_idx ON missions (organisation_id, starts_at, id);
CREATE INDEX missions_organisation_pilot_starts_idx ON missions (organisation_id, pilot_id, starts_at, id);
CREATE INDEX missions_organisation_drone_idx ON missions (organisation_id, drone_id);

CREATE FUNCTION check_mission_assignment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    drone_status text;
    pilot_eligible boolean;
BEGIN
    IF NEW.cancelled_at IS NOT NULL THEN
        RETURN NEW;
    END IF;

    -- Hold the resource rows until commit so concurrent status and role changes
    -- cannot invalidate the eligibility checks before the mission is written.
    SELECT status INTO drone_status FROM drones
        WHERE organisation_id = NEW.organisation_id AND id = NEW.drone_id FOR SHARE;
    IF drone_status = 'grounded' THEN
        RAISE EXCEPTION 'Grounded drones cannot be scheduled'
            USING ERRCODE = '23514', CONSTRAINT = 'missions_drone_eligible';
    END IF;

    SELECT role = 'pilot' AND is_active INTO pilot_eligible FROM users
        WHERE organisation_id = NEW.organisation_id AND id = NEW.pilot_id FOR SHARE;
    IF pilot_eligible IS FALSE THEN
        RAISE EXCEPTION 'Assigned user must be an active pilot'
            USING ERRCODE = '23514', CONSTRAINT = 'missions_pilot_eligible';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER missions_check_assignment BEFORE INSERT OR UPDATE ON missions
    FOR EACH ROW EXECUTE FUNCTION check_mission_assignment();
CREATE TRIGGER missions_set_updated_at BEFORE UPDATE ON missions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
