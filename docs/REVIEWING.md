# Review and branch protection

Verified on GitHub on 2026-10-29 for `main`:

- Pull requests require at least one approving reviewer. New commits dismiss stale approvals.
- Required status checks: `backend`, `frontend`, `database`, `openapi`.
- The branch must be up to date before merging and review conversations must be resolved.
- Protection applies to administrators. Force pushes and branch deletion are disabled.

These are repository settings, not settings installed by cloning this file. Inspect them in [branch settings](https://github.com/omarzayedbeshir/drone-fleet-management/settings/branches), or with:

```sh
gh api repos/omarzayedbeshir/drone-fleet-management/branches/main/protection
```

Work on a feature branch, open a pull request, wait for CI, and request the sprint reviewer. Use the `codex/` prefix for agent-created branches. Do not push directly to `main` or temporarily remove protection to merge your own work.

| Author | Sprint reviewer |
| --- | --- |
| Omar | Hamza |
| Seif | Omar |
| Ali | Seif |
| Ziad | Ali |
| Hamza | Ziad |

The rotation is a team convention. GitHub enforces the approval count, but no CODEOWNERS file or identity-specific review rule is configured. Request the actual teammate's GitHub account when known; do not invent account names. An approval cannot be supplied by the pull request author. Protected-branch setup does not replace the required review or pair session.

## Omar's handoff review

Start with the [architecture decision](ARCHITECTURE.md), [R-15 evidence](spikes/R15.md), and [SITL walkthrough](SITL_DEMO.md). Implementation commit `9a5616a` and its predecessors are already on `main`; a new documentation PR does not retroactively establish approval of that implementation. Record the implementation review separately and open follow-up PRs for any fixes.

Hamza's review should cover:

- Registry role/tenant boundaries, register/edit/remove behavior, and errors in [the registry contract](../backend/DRONES.md).
- Audit snapshots in the same transaction, rollback on recorder failure, and the organisation-deletion exception in [the audit contract](../backend/AUDIT.md).
- Agreement between [OpenAPI](../backend/openapi.yaml) and implemented routes, including the documented unfinished NFR-08 scope.
- Adapter identity, UTC/SI mappings, unknown values, counter resets, reconnects, bounded buffering, and immutable retries in [the adapter reference](../backend/ARDUPILOT.md).
- Successful CI and the distinction between binary mock tests, local live SITL tests, and the still-pending shared pipeline test.

Attach the reviewed commit and any findings to the PR or team review record. Before merging the documentation PR, confirm all four checks pass, obtain one approval, and resolve conversations. Keep the local PDF, editor swap files, simulator state, and secrets out of the PR.
