-- Keep registry snapshots after a drone or actor account is removed. Only
-- organisation removal purges these records, matching the retention exception.
CREATE TABLE drone_registry_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    actor_id uuid NOT NULL,
    drone_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('created', 'updated', 'deleted')),
    before_data jsonb,
    after_data jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (action = 'created' AND before_data IS NULL AND jsonb_typeof(after_data) = 'object' AND after_data IS NOT NULL)
        OR (action = 'updated' AND jsonb_typeof(before_data) = 'object' AND before_data IS NOT NULL AND jsonb_typeof(after_data) = 'object' AND after_data IS NOT NULL)
        OR (action = 'deleted' AND jsonb_typeof(before_data) = 'object' AND before_data IS NOT NULL AND after_data IS NULL)
    )
);
CREATE INDEX drone_registry_events_organisation_drone_idx
    ON drone_registry_events (organisation_id, drone_id, id);

CREATE FUNCTION protect_drone_registry_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (
        SELECT 1 FROM organisations WHERE id = OLD.organisation_id
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'Drone registry audit events are append-only' USING ERRCODE = '42501';
END;
$$;
CREATE TRIGGER drone_registry_events_append_only
    BEFORE UPDATE OR DELETE ON drone_registry_events
    FOR EACH ROW EXECUTE FUNCTION protect_drone_registry_events();
