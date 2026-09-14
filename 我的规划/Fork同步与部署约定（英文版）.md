---
title: Fork maintenance and deployment
description: Branch roles, upstream synchronization, and deployment rules for this fork
---

# Fork maintenance and deployment

This repository is a maintained fork of `basketikun/infinite-canvas`.

## Fixed remote and branch roles

| Ref | Role |
| --- | --- |
| `upstream` | The original source repository. Treat `upstream/main` as read-only source code. |
| `origin` | This fork, used for pushing reviewed changes and deployment. |
| `main` | The only long-lived stable integration branch. It contains the latest upstream code plus accepted fork changes. |
| `codex/account-backend-plan` | The current long-lived planning and implementation branch for the account and backend work. |

Short-lived feature or synchronization branches are optional. The project does not require many permanent branches.

## Synchronization procedure

Run the following from a clean worktree:

```bash
git fetch upstream --prune
git switch main
git merge upstream/main
git push origin main
git switch codex/account-backend-plan
git merge main
git push origin codex/account-backend-plan
```

Review conflicts and the source changelog before pushing. If `main` has not added fork commits, the merge will normally be a fast-forward; after the fork has custom code, a regular merge is expected.

## Development and deployment

The account and backend plan is currently documentation only. The planned Go service belongs in this repository under `server/`, because it shares authentication, data routing, storage, and deployment decisions with `web/`. The eventual production stack is expected to use Docker Compose for the web app, API, database, and object storage integration.

Deploy only from a reviewed commit on `origin/main` or from a release tag created from it. Do not deploy `upstream/main` or the planning branch directly. The current repository still primarily ships a static web application, and its production Docker static-resource path requires final verification before being treated as fully validated.

When backend work is ready, merge it into `main` only after the phase gate and user acceptance checks in the account backend plan pass. Then deploy that tested `main` commit or tag; use the planning branch only for development or staging previews.

## Divergence assessment and staged sync strategy (verified 2026-09)

Upstream is highly active (395 commits in six months, last commit days ago) and its hottest files are exactly the ones this fork plans to delete or rewrite (`project.tsx` 63 changes in three months, `image.ts` 29, `use-config-store.ts` 21). Local `main` currently matches `upstream/main` exactly, so the window before Phase 1 lands is the cheapest time to sync. `origin/main` carries five superseded planning-doc commits (no code) already replaced by the planning branch.

The sync approach must degrade as the fork deepens; do not use wholesale merges throughout:

- **Until Phase 1 merges**: keep the merge flow above, once before each phase starts, never during one.
- **After Phase 2 merges** (data layer rewritten): merge only untouched areas (`components/canvas/`, `plugins/`, `canvas-agent/`); cherry-pick from upstream for rewritten paths (`services/`, `stores/`, `router.tsx`, `pages/config/`).
- **After Phase 4 merges** (AI path rewritten, user channels removed): hard-fork `web/src/services`, `use-config-store`, `router.tsx`, `pages/config/`, and the channel-related i18n keys. Upstream model-support changes become reference material for the server-side providers, not merge targets.

Conflict rules by class: docs and changelog take the union (ours first); i18n takes the union of keys plus the symmetry check; upstream changes to files we deleted stay deleted (port bugfix ideas server-side; the known `cleanupUnusedMedia` scan bug is already handled this way); upstream changes to files we rewrote always resolve to ours, with desirable canvas-level improvements ported manually; new upstream files auto-merge but `bun run typecheck` is the semantic-conflict detector — new features calling removed APIs surface there and get re-wired to the new architecture. A sync counts as complete only when typecheck, tests, and a login-plus-canvas smoke run all pass.

One-time `origin/main` reconciliation: commit the current docs reorg to the planning branch, verify no content is lost via `git diff origin/main codex/account-backend-plan -- docs 我的规划`, then (with explicit user confirmation) `git push --force-with-lease origin main` based on local `main`; afterwards `origin/main` follows normal pushes.
