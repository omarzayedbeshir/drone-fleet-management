# DJI source contract and verified limitations

The current REST polling adapter is tested against the project's HTTP mock.
Its configurable endpoints are assumptions, not verified DJI Cloud API support.
The committed CSV is generated demo data, not captured hardware telemetry.

The [official DJI M3D property reference](https://github.com/dji-sdk/Cloud-API-Doc/blob/master/docs/en/60.api-reference/20.dock-to-cloud/00.mqtt/10.aircraft/10.m3d-properties.md),
checked on 2026-11-07, establishes these distinctions:

- OSD/state properties are published over MQTT topics.
- `height` is an ellipsoidal height, rather than the application's MSL altitude.
- `battery.batteries[].loop_times` belongs to an individual battery pack.
- `total_flight_time` is accumulated aircraft flight time in seconds.

Consequently, the normalizer no longer treats a bare `height` as MSL. It accepts
an explicitly normalized `altitude_m`, or the synthetic CSV contract's `height`
with `height_reference=msl`. Otherwise altitude is unreported; the host does not
invent a geoid correction. Existing recording values/event identities are
unchanged, so adding the explicit datum does not invalidate accepted retries.

The generated fixture's `battery_loop_times` is an explicitly synthetic cumulative
drone counter. It must not be represented as native DJI battery-array mapping.
Unprocessed pack arrays do not become aircraft lifetime counts; missing required
counters cause rejection. A production source module needs pack identity and
swap/reset reconciliation before supplying cumulative drone totals. Per-pack
retirement/disposal and maintenance alerts remain later-sprint workflows.

A reference-shaped contract test verifies that unprocessed pack counts are not
silently invented as aircraft totals. Datum tests ensure undeclared height is
not labelled MSL. These checks improve format fidelity but do not establish
access to live DJI hardware, MQTT authentication, or a completed DJI SDK module.
The permitted simulated CSV fallback remains the working second source.
