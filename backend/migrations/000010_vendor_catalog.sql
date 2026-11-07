-- One-time preparation for adapter modules. Future vendor installation changes
-- catalog/profile data only; it does not change this schema or existing history.
CREATE TABLE supported_vendors (
 code text PRIMARY KEY CHECK (code ~ '^[a-z][a-z0-9_]{0,31}$' AND code <> 'csv'),
 label text NOT NULL CHECK (length(label) BETWEEN 1 AND 100)
);
INSERT INTO supported_vendors(code,label) VALUES ('dji','DJI'),('ardupilot','ArduPilot');
ALTER TABLE drones DROP CONSTRAINT drones_vendor_check;
ALTER TABLE drones ADD CONSTRAINT drones_vendor_code_check
 CHECK (vendor ~ '^[a-z][a-z0-9_]{0,31}$' AND vendor <> 'csv');
ALTER TABLE drones ADD CONSTRAINT drones_vendor_catalog_fkey
 FOREIGN KEY (vendor) REFERENCES supported_vendors(code);
ALTER TABLE telemetry_receipts DROP CONSTRAINT telemetry_receipts_source_check;
ALTER TABLE telemetry_receipts ADD CONSTRAINT telemetry_receipts_source_code_check
 CHECK (source ~ '^[a-z][a-z0-9_]{0,31}$');
ALTER TABLE telemetry_samples DROP CONSTRAINT telemetry_samples_source_check;
ALTER TABLE telemetry_samples ADD CONSTRAINT telemetry_samples_source_code_check
 CHECK (source ~ '^[a-z][a-z0-9_]{0,31}$');
