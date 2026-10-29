-- Generalize existing registry history in place. Do not edit migration 000004:
-- its checksum and all existing event IDs/snapshots must remain intact.
ALTER TABLE drone_registry_events RENAME TO audit_events;
ALTER TABLE audit_events RENAME COLUMN drone_id TO resource_id;
ALTER TABLE audit_events ADD COLUMN resource_type text NOT NULL DEFAULT 'drone';
ALTER TABLE audit_events ALTER COLUMN resource_type DROP DEFAULT;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_resource_type_check
    CHECK (resource_type ~ '^[a-z][a-z0-9_]{0,63}$');

ALTER TABLE audit_events DROP CONSTRAINT drone_registry_events_action_check;
ALTER TABLE audit_events DROP CONSTRAINT drone_registry_events_check;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check
    CHECK (action ~ '^[a-z][a-z0-9_]{0,99}$');
ALTER TABLE audit_events ADD CONSTRAINT audit_events_snapshots_check CHECK (
    (before_data IS NULL OR jsonb_typeof(before_data) = 'object')
    AND (after_data IS NULL OR jsonb_typeof(after_data) = 'object')
    AND (before_data IS NOT NULL OR after_data IS NOT NULL)
    AND (action <> 'created' OR (before_data IS NULL AND after_data IS NOT NULL))
    AND (action <> 'updated' OR (before_data IS NOT NULL AND after_data IS NOT NULL))
    AND (action <> 'deleted' OR (before_data IS NOT NULL AND after_data IS NULL))
);
ALTER TABLE audit_events RENAME CONSTRAINT drone_registry_events_pkey TO audit_events_pkey;
ALTER TABLE audit_events RENAME CONSTRAINT drone_registry_events_organisation_id_fkey
    TO audit_events_organisation_id_fkey;
DROP INDEX drone_registry_events_organisation_drone_idx;
CREATE INDEX audit_events_organisation_resource_idx
    ON audit_events (organisation_id, resource_type, resource_id, id);

CREATE FUNCTION protect_audit_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (
        SELECT 1 FROM organisations WHERE id = OLD.organisation_id
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Audit events are append-only' USING ERRCODE = '42501';
END;
$$;
DROP TRIGGER drone_registry_events_append_only ON audit_events;
CREATE TRIGGER audit_events_append_only BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION protect_audit_events();
DROP FUNCTION protect_drone_registry_events();

CREATE FUNCTION prevent_audit_events_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Audit events cannot be truncated' USING ERRCODE = '42501';
END;
$$;
CREATE TRIGGER audit_events_no_truncate BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION prevent_audit_events_truncate();
