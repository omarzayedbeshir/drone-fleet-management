# Sprint 1 completion status

Assessment updated 2026-11-07 against Milestone 1 sections 4 and 5.3. This
records implementation and evidence separately; green CI alone is not full
requirements acceptance. The local requirements PDF remains outside Git.

| Sprint 1 item | State | Evidence / remaining boundary |
| --- | --- | --- |
| FR-04 / UC-05 backend registry | Implemented | Tenant-scoped CRUD/schema and transactional audit tests |
| FR-08 / UC-11 ingestion | Implemented with permitted simulated fallback | Real ArduPilot SITL evidence plus labelled generated DJI-format CSV; live DJI access unverified |
| FR-09 / UC-12 common format | Implemented | SI/UTC contract, cross-source normalization tests, explicit altitude datum |
| FR-10 / UC-13 condition history | Implemented within source capabilities | Flight/cycle/fault history; ArduPilot preserves battery baseline rather than measuring pack cycles |
| NFR-03 / UC-25 connections/recovery | Implemented | Saved managed connectors, leader election, bounded queues, retries, health and stale-state snapshots |
| NFR-05 / UC-12a / UC-12c / UC-24 | Implemented for Sprint 1 workflows | Invalid-record logging, duplicate/counter checks, append-only audits, atomic rollback tests |
| NFR-07 extension architecture | Implemented and prototype-tested | New vendor/module works through metadata and common storage without vendor-specific core/schema/UI changes; real SDK person-day target remains unmeasured |
| NFR-08 Sprint 1 schema/OpenAPI | Implemented | Common schema and documented routes; exports remain Sprint 3 |
| NFR-09 Sprint 1 CI/mock feed | Implemented | All packages are checked, database suite includes managed-connector tests, fault-capable mock |
| SOC-03 privacy | Implemented | Allowlisted raw path and strict normalized module boundary before buffering |
| SOC-04 Sprint 1 cycle tracking | Implemented with documented source limitations | Synthetic/vendor-provided cumulative counters; no fabricated ArduPilot cycles or blind per-pack substitution |
| SOC-05 stack/server choices | Documented | Open-source components, single-node topology; capacity/availability not claimed as measured |

## Work completed after the integration review

- Saved connector configuration, API supervision, role/tenant controls, visible
  health, restart/completion semantics, revocation and generation fencing.
- Trusted external adapter modules with a versioned process protocol, installed
  vendor catalog, generic registry/source validation and data-driven UI labels.
- New-vendor acceptance prototype and a runnable module starter/guide.
- Source-reference check and correction of DJI altitude-datum assumptions.
- Audited battery-cycle baseline recording for managers and technicians, visible
  provenance, historical counter boundaries and automatic active-connector reload.
- Documentation distinguishes generated fixtures, native source semantics and
  actual live-vendor evidence. The architecture and operator guides reflect
  the implemented pipeline rather than the original handoff gaps.

## Evidence still needed / external dependencies

1. Confirm whether actual DJI access is granted. If unavailable, explicitly
   demonstrate and label the generated CSV fallback. A live module needs verified
   MQTT/API access, datum conversion and pack-counter reconciliation.
2. Record a timed real-SDK extension exercise against NFR-07's two-person-day
   target. The automated synthetic prototype demonstrates the extension mechanism,
   not the effort required for an arbitrary vendor SDK.
3. Acquire and document the source of real battery-cycle baselines using the
   [baseline workflow](BATTERY_CYCLES.md). SITL does not supply a
   drone-wide cumulative battery cycle count; actual pack lifecycle acquisition
   needs vendor data/hardware and an agreed reconciliation policy.

Mission scheduling, maintenance alerts/service screens, user/organisation
administration, exports, AI, localisation, final coverage, full-system load,
production security and backup/restore acceptance are not labelled Sprint 1
omissions: follow their assigned later-sprint scope. NFR-03's 99% monthly uptime
is a design target, with broader outage demonstrations planned in Sprint 3.

See [connectors](CONNECTORS.md), [module installation](ADAPTER_MODULES.md),
[DJI source contract](DJI_SOURCE_CONTRACT.md), and [ingestion](ingestion-test-infra.md)
for commands and test locations.

Latest local verification passed the complete backend race suite with real
TimescaleDB tests, vet/build, all 32 frontend tests and production build, and
OpenAPI/schema checks (16 operations, 38 examples). Migration 000011 upgraded
the existing development database and reran idempotently; SQL constraints passed.
The new-vendor prototype completed end to end in approximately 1.4 seconds,
which is explicitly execution time rather than developer-effort evidence.
