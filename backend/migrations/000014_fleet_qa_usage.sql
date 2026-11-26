CREATE TABLE fleet_qa_usage (
    organisation_id uuid NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    usage_day date NOT NULL,
    requests integer NOT NULL CHECK (requests > 0),
    PRIMARY KEY (organisation_id, usage_day)
);
