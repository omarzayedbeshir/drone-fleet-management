\set ON_ERROR_STOP on
BEGIN;

-- All fixtures and mutations are rolled back, including on assertion failure.
DO $$
DECLARE
    org_a uuid;
    org_b uuid;
    drone_id uuid;
    admin_id uuid;
    user_id uuid;
    observed_status text;
    observed_timestamp timestamptz;
BEGIN
    INSERT INTO organisations (name) VALUES ('Schema test A') RETURNING id INTO org_a;
    INSERT INTO organisations (name) VALUES ('Schema test B') RETURNING id INTO org_b;
    INSERT INTO users (name, email, password_hash, role)
        VALUES ('Administrator', 'schema-admin@example.test', 'test-hash', 'system_administrator')
        RETURNING id INTO admin_id;
    INSERT INTO users (organisation_id, name, email, password_hash, role)
        VALUES (org_a, 'Pilot', 'schema-pilot@example.test', 'test-hash', 'pilot')
        RETURNING id INTO user_id;
    UPDATE users SET is_active = false WHERE id = user_id;
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = user_id AND NOT is_active) THEN
        RAISE EXCEPTION 'User deactivation failed';
    END IF;

    BEGIN
        INSERT INTO users (name, email, password_hash, role)
            VALUES ('No tenant', 'no-tenant@example.test', 'test-hash', 'pilot');
        RAISE EXCEPTION 'Accepted tenant user without organisation';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO users (organisation_id, name, email, password_hash, role)
            VALUES (org_a, 'Admin', 'tenant-admin@example.test', 'test-hash', 'system_administrator');
        RAISE EXCEPTION 'Accepted administrator with organisation';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO users (organisation_id, name, email, password_hash, role)
            VALUES (org_b, 'Duplicate', 'schema-pilot@example.test', 'test-hash', 'pilot');
        RAISE EXCEPTION 'Accepted duplicate login email';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO users (organisation_id, name, email, password_hash, role)
            VALUES (org_a, 'Mixed case', 'Mixed@example.test', 'test-hash', 'pilot');
        RAISE EXCEPTION 'Accepted noncanonical email';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO users (organisation_id, name, email, password_hash, role)
            VALUES (org_a, 'Bad role', 'bad-role@example.test', 'test-hash', 'owner');
        RAISE EXCEPTION 'Accepted invalid role';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
        VALUES (org_a, 'Drone A', 'dji', 'Test model', 'SCHEMA-001') RETURNING id INTO drone_id;
    SELECT status, last_telemetry_at INTO observed_status, observed_timestamp FROM drones WHERE id = drone_id;
    IF observed_status <> 'unknown' OR observed_timestamp IS NOT NULL THEN
        RAISE EXCEPTION 'New drone must have unknown status and no telemetry timestamp';
    END IF;
    -- Serial identity is scoped to the tenant and vendor.
    INSERT INTO drones (organisation_id, name, vendor, model, serial_number) VALUES
        (org_b, 'Drone B', 'dji', 'Test model', 'SCHEMA-001'),
        (org_a, 'Drone C', 'ardupilot', 'Test model', 'SCHEMA-001');
    BEGIN
        INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
            VALUES (org_a, 'Duplicate', 'dji', 'Test model', 'SCHEMA-001');
        RAISE EXCEPTION 'Accepted duplicate tenant/vendor serial';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
            VALUES (gen_random_uuid(), 'Orphan', 'dji', 'Test model', 'ORPHAN');
        RAISE EXCEPTION 'Accepted missing organisation';
    EXCEPTION WHEN foreign_key_violation THEN NULL;
    END;
    BEGIN
        UPDATE drones SET status = 'flying' WHERE id = drone_id;
        RAISE EXCEPTION 'Accepted invalid drone status';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        UPDATE drones SET vendor = 'csv' WHERE id = drone_id;
        RAISE EXCEPTION 'Accepted CSV as a vendor';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        UPDATE drones SET serial_number = '   ' WHERE id = drone_id;
        RAISE EXCEPTION 'Accepted blank serial';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    UPDATE drones SET updated_at = '2000-01-01', name = 'Renamed' WHERE id = drone_id;
    SELECT updated_at INTO observed_timestamp FROM drones WHERE id = drone_id;
    IF observed_timestamp <= '2000-01-01'::timestamptz THEN
        RAISE EXCEPTION 'Update timestamp was not maintained';
    END IF;
    IF EXISTS (SELECT 1 FROM drones WHERE id = drone_id AND last_telemetry_at IS NOT NULL) THEN
        RAISE EXCEPTION 'Metadata edit incorrectly changed telemetry freshness';
    END IF;

    DELETE FROM organisations WHERE id = org_a;
    IF EXISTS (SELECT 1 FROM users WHERE organisation_id = org_a)
       OR EXISTS (SELECT 1 FROM drones WHERE organisation_id = org_a) THEN
        RAISE EXCEPTION 'Organisation deletion left tenant records';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = admin_id)
       OR NOT EXISTS (SELECT 1 FROM drones WHERE organisation_id = org_b) THEN
        RAISE EXCEPTION 'Organisation deletion affected another tenant or administrator';
    END IF;
END;
$$;
ROLLBACK;
