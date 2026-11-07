-- Forward-only extension of the existing ingestion schema.
ALTER TABLE drones ADD COLUMN battery_rated_cycles bigint CHECK (battery_rated_cycles > 0);
ALTER TABLE drones ADD COLUMN battery_alert_fraction numeric NOT NULL DEFAULT 0.8
    CHECK (battery_alert_fraction > 0 AND battery_alert_fraction <= 1);

-- Receipts survive chunk retention so an old retry cannot resurrect data or
-- reuse a timestamp. Their hash and receipt time reconstruct an exact retry.
ALTER TABLE telemetry_receipts ADD CONSTRAINT telemetry_receipts_drone_time_key
    UNIQUE (organisation_id, drone_id, observed_at);

CREATE TABLE telemetry_storage_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    retention interval NOT NULL CHECK (retention > INTERVAL '0'),
    compression_after interval NOT NULL CHECK (compression_after > INTERVAL '0' AND compression_after < retention)
);

ALTER TABLE telemetry_samples SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'organisation_id,drone_id',
    timescaledb.compress_orderby = 'observed_at DESC'
);

-- Operators can change both policies and the admission cutoff atomically.
CREATE FUNCTION configure_telemetry_storage(retain interval, compress_after interval)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF retain IS NULL OR compress_after IS NULL OR compress_after <= INTERVAL '0'
        OR retain <= compress_after THEN
        RAISE EXCEPTION 'Require 0 < compression age < retention age';
    END IF;
    PERFORM pg_advisory_xact_lock(748291606);
    PERFORM public.remove_retention_policy('telemetry_samples', if_exists => true);
    PERFORM public.remove_compression_policy('telemetry_samples', if_exists => true);
    PERFORM public.add_compression_policy('telemetry_samples', compress_after);
    PERFORM public.add_retention_policy('telemetry_samples', retain);
    INSERT INTO telemetry_storage_settings(singleton,retention,compression_after)
        VALUES(true,retain,compress_after)
        ON CONFLICT(singleton) DO UPDATE SET retention=EXCLUDED.retention,
            compression_after=EXCLUDED.compression_after;
END;
$$;
SELECT configure_telemetry_storage(INTERVAL '90 days', INTERVAL '7 days');
