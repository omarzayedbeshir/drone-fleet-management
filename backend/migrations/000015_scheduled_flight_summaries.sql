CREATE TABLE flight_summary_settings (
    organisation_id uuid PRIMARY KEY REFERENCES organisations(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT false
);

CREATE TABLE flight_summary_deliveries (
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    reporting_day date NOT NULL,
    report jsonb NOT NULL CHECK (jsonb_typeof(report) = 'object'),
    delivered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (organisation_id, reporting_day)
);
