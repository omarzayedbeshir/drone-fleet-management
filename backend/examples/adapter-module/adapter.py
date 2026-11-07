#!/usr/bin/env python3
"""Synthetic adapter-module starter. No hardware SDK or live vendor claim."""
import datetime
import json
import sys
import uuid

binding = json.load(sys.stdin)
if binding["protocol"] != "aerosync.telemetry.v1":
    raise SystemExit("unsupported host protocol")
# A real module obtains these values from the vendor SDK, filters private data,
# converts its units/datum, and persists stable event IDs across source retries.
# Only this demonstration manufactures a synthetic observation from the baseline.
json.dump({
    "event_id": "starter-" + str(uuid.uuid4()),
    "source": binding["vendor"],
    "observed_at": (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=1)).isoformat(),
    "status": "unknown",
    "flight_time_seconds": binding["flight_time_seconds"],
    "battery_cycles": binding["battery_cycles"],
    "fault_flags": [],
    "latitude": 30.85,
    "longitude": 31.05,
    "altitude_m": 50,
}, sys.stdout)
sys.stdout.write("\n")
