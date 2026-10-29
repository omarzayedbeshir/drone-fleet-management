# Review and branch protection

Verified on GitHub on 2026-10-29 for `main`:

- Other contributors' pull requests require Omar's approval as code owner (`@omarzayedbeshir`). New commits dismiss stale approvals.
- The active `Omar approval for main` ruleset allows only Omar's account to bypass review, and only through a pull request. CI requirements remain enforced separately for everyone.
- Required status checks: `backend`, `frontend`, `database`, `openapi`.
- The branch must be up to date before merging and review conversations must be resolved.
- Protection applies to administrators. Force pushes and branch deletion are disabled.

These are repository settings, not settings installed by cloning this file. Inspect them in [branch settings](https://github.com/omarzayedbeshir/drone-fleet-management/settings/branches), or with:

```sh
gh api repos/omarzayedbeshir/drone-fleet-management/branches/main/protection
```

Work on a feature branch, open a pull request, and wait for CI. Other contributors must obtain Omar's approval; Omar may merge his own pull requests using the account-specific review bypass. Use the `codex/` prefix for agent-created branches. Direct pushes to `main` remain prohibited.

| Author | Sprint reviewer |
| --- | --- |
| Omar | Hamza |
| Seif | Omar |
| Ali | Seif |
| Ziad | Ali |
| Hamza | Ziad |

The rotation is an optional peer-review convention. `.github/CODEOWNERS` names Omar for all files, and the review ruleset requires code-owner approval for other contributors. A teammate's peer review does not substitute for Omar's permission. GitHub does not allow self-approval; Omar uses the explicit review bypass instead. Protected-branch setup does not replace team pairing or integration.

## Omar's handoff review

Start with the [architecture decision](ARCHITECTURE.md), [R-15 evidence](spikes/R15.md), and [SITL walkthrough](SITL_DEMO.md). Implementation commit `9a5616a` and its predecessors are already on `main`; a new documentation PR does not retroactively establish approval of that implementation. Record the implementation review separately and open follow-up PRs for any fixes.

Hamza's review should cover:

- Registry role/tenant boundaries, register/edit/remove behavior, and errors in [the registry contract](../backend/DRONES.md).
- Audit snapshots in the same transaction, rollback on recorder failure, and the organisation-deletion exception in [the audit contract](../backend/AUDIT.md).
- Agreement between [OpenAPI](../backend/openapi.yaml) and implemented routes, including the documented unfinished NFR-08 scope.
- Adapter identity, UTC/SI mappings, unknown values, counter resets, reconnects, bounded buffering, and immutable retries in [the adapter reference](../backend/ARDUPILOT.md).
- Successful CI and the distinction between binary mock tests, local live SITL tests, and the still-pending shared pipeline test.

Attach the reviewed commit and any findings to the PR or team review record. Before merging, confirm all four checks pass and resolve conversations. Other contributors must obtain Omar's approval; Omar can use his review bypass. Keep the local PDF, editor swap files, simulator state, and secrets out of the PR.
