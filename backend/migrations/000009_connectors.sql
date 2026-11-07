-- One managed source per registered aircraft. Only profile references are stored;
-- endpoints, file paths and credential references belong to operator configuration.
CREATE TABLE drone_connectors (
    drone_id uuid PRIMARY KEY,
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    actor_id uuid NOT NULL,
    profile_id text NOT NULL CHECK (length(profile_id) BETWEEN 1 AND 64),
    enabled boolean NOT NULL,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN
        ('pending','starting','running','retrying','completed','failed','stopped','disabled')),
    health jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(health)='object'),
    runtime_owner uuid,
    runtime_updated_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (organisation_id, drone_id) REFERENCES drones(organisation_id,id) ON DELETE CASCADE
);
CREATE INDEX drone_connectors_organisation_idx ON drone_connectors(organisation_id);
