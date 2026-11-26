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

-- Mission scheduling constraints.
DO $$
DECLARE
    tenant uuid;
    other_tenant uuid;
    drone_a uuid;
    drone_b uuid;
    pilot_a uuid;
    pilot_b uuid;
    first_mission uuid;
    start_time constant timestamptz := '2026-11-27 09:00:00+00';
    end_time constant timestamptz := '2026-11-27 10:00:00+00';
BEGIN
    INSERT INTO organisations (name) VALUES ('Mission test') RETURNING id INTO tenant;
    INSERT INTO organisations (name) VALUES ('Mission test other tenant') RETURNING id INTO other_tenant;
    INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
        VALUES (tenant, 'Drone A', 'dji', 'Test model', 'MISSION-A') RETURNING id INTO drone_a;
    INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
        VALUES (tenant, 'Drone B', 'dji', 'Test model', 'MISSION-B') RETURNING id INTO drone_b;
    INSERT INTO users (organisation_id, name, email, password_hash, role)
        VALUES (tenant, 'Pilot A', 'mission-a@example.test', 'test-hash', 'pilot') RETURNING id INTO pilot_a;
    INSERT INTO users (organisation_id, name, email, password_hash, role)
        VALUES (tenant, 'Pilot B', 'mission-b@example.test', 'test-hash', 'pilot') RETURNING id INTO pilot_b;

    INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
        VALUES (tenant, drone_a, pilot_a, 'Test field', start_time, end_time) RETURNING id INTO first_mission;

    BEGIN
        INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
            VALUES (tenant, drone_a, pilot_b, 'Test field', start_time, end_time);
        RAISE EXCEPTION 'Accepted overlapping drone bookings';
    EXCEPTION WHEN exclusion_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
            VALUES (tenant, drone_b, pilot_a, 'Test field', start_time, end_time);
        RAISE EXCEPTION 'Accepted overlapping pilot bookings';
    EXCEPTION WHEN exclusion_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
            VALUES (other_tenant, drone_a, pilot_a, 'Test field', start_time, end_time);
        RAISE EXCEPTION 'Accepted a cross-tenant assignment';
    EXCEPTION WHEN foreign_key_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at, near_populated_area)
            VALUES (tenant, drone_b, pilot_b, 'Test field', start_time, end_time, true);
        RAISE EXCEPTION 'Accepted flagged mission without safety details';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    UPDATE drones SET status = 'grounded' WHERE id = drone_b;
    BEGIN
        INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
            VALUES (tenant, drone_b, pilot_b, 'Test field', start_time, end_time);
        RAISE EXCEPTION 'Accepted a grounded drone';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    -- Adjacent windows are valid, and cancellations release both resources.
    INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
        VALUES (tenant, drone_a, pilot_a, 'Test field', end_time, end_time + interval '1 hour');
    UPDATE missions SET cancelled_at = now() WHERE id = first_mission;
    INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at,
                          near_populated_area, safety_rationale, safety_acknowledged)
        VALUES (tenant, drone_a, pilot_a, 'Test field', start_time, end_time,
                true, 'Maintain separation from nearby residences', true);

    DELETE FROM organisations WHERE id = tenant;
    IF EXISTS (SELECT 1 FROM missions WHERE organisation_id = tenant) THEN
        RAISE EXCEPTION 'Organisation deletion left mission records';
    END IF;
END;
$$;
ROLLBACK;
