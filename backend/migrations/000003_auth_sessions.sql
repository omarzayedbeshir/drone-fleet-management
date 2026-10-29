CREATE TABLE auth_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    CHECK (expires_at > created_at)
);
CREATE INDEX auth_sessions_user_id_idx ON auth_sessions (user_id);
CREATE INDEX auth_sessions_expires_at_idx ON auth_sessions (expires_at);

-- Changes to credentials or access immediately invalidate every existing session.
CREATE FUNCTION revoke_user_sessions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.password_hash IS DISTINCT FROM OLD.password_hash
       OR NEW.role IS DISTINCT FROM OLD.role
       OR NEW.organisation_id IS DISTINCT FROM OLD.organisation_id
       OR NEW.is_active IS DISTINCT FROM OLD.is_active THEN
        DELETE FROM auth_sessions WHERE user_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER users_revoke_sessions AFTER UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION revoke_user_sessions();
