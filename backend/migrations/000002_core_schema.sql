CREATE TABLE organisations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organisation_id uuid REFERENCES organisations(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    -- Store a canonical login identifier; email syntax validation belongs in the API.
    email text NOT NULL UNIQUE CHECK (
        email = lower(btrim(email)) AND char_length(email) BETWEEN 3 AND 254
    ),
    -- Password hashing and verification belong in the authentication service.
    password_hash text NOT NULL CHECK (btrim(password_hash) <> ''),
    role text NOT NULL CHECK (role IN (
        'system_administrator', 'fleet_manager', 'pilot', 'maintenance_technician'
    )),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_role_organisation_check CHECK (
        (role = 'system_administrator' AND organisation_id IS NULL)
        OR (role <> 'system_administrator' AND organisation_id IS NOT NULL)
    )
);
CREATE INDEX users_organisation_id_idx ON users (organisation_id);

CREATE TABLE drones (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    -- ArduPilot is integrated via MAVLink. CSV is an ingestion format, not a vendor.
    vendor text NOT NULL CHECK (vendor IN ('dji', 'ardupilot')),
    model text NOT NULL CHECK (model = btrim(model) AND char_length(model) BETWEEN 1 AND 200),
    serial_number text NOT NULL CHECK (
        serial_number = btrim(serial_number) AND char_length(serial_number) BETWEEN 1 AND 200
    ),
    status text NOT NULL DEFAULT 'unknown' CHECK (
        status IN ('available', 'charging', 'in_mission', 'grounded', 'unknown')
    ),
    -- Updated only when telemetry is received, independently of metadata edits.
    last_telemetry_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT drones_organisation_vendor_serial_key UNIQUE (organisation_id, vendor, serial_number)
);
-- The unique constraint also indexes organisation_id for tenant fleet queries.
CREATE INDEX drones_organisation_status_idx ON drones (organisation_id, status);

CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = clock_timestamp();
    RETURN NEW;
END;
$$;

CREATE TRIGGER organisations_set_updated_at BEFORE UPDATE ON organisations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER drones_set_updated_at BEFORE UPDATE ON drones
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
