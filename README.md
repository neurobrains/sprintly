# Sprintly

**One workspace for the whole team.** Boards, docs, discussion, time tracking
and workload — together, so work stops scattering across five tools.

Go API · Next.js 15 · Supabase (Postgres + Auth) · Google sign-in

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white)](backend/go.mod)
[![Next.js](https://img.shields.io/badge/next.js-15-black?logo=nextdotjs)](frontend/package.json)

---

## Architecture

```
frontend/  Next.js 15 · TypeScript · Tailwind · TanStack Query
     │
     ├── Supabase Auth ──────── Google sign-in only. Returns a JWT.
     │                          The browser never reads a table.
     │
     └── REST + WebSocket ──▶ backend/  Go 1.24 · chi
                                  │     verifies the JWT via JWKS
                                  │     owns all authorization
                                  │
                                  └── PostgREST ──▶ Supabase Postgres
                                      service role key   RLS · triggers · RPCs
```

**Every byte of data goes through the Go API.** The browser holds the Supabase
URL and anon key purely to complete the Google OAuth handshake and hold a
session; it has no data path to Postgres. Authorization lives in one place —
`requireWorkspace` and `requireRole` in `handlers/server.go`.

**The API talks PostgREST, not Postgres.** The deployment holds the project URL,
the anon key and the service role key — never the database password — so there
is no connection string and no pool. `db` is a small PostgREST client;
`handlers` uses it for CRUD and calls SQL functions for anything it can't
express. See [CLAUDE.md](CLAUDE.md) for the rules that keeps honest.

**The schema is one idempotent file.** `supabase/schema.sql` — every object is
`if not exists`, `or replace`, or dropped first. Re-running it is how you apply
a change. No migration ordering, no drift.

---

## Setup

Needs Go 1.24+, Node 22+, and a Supabase project.

**1. Database.** Paste [`supabase/schema.sql`](supabase/schema.sql) into the
Supabase SQL Editor and run it. Safe to re-run.

**2. Google sign-in.** OAuth client with redirect
`https://<ref>.supabase.co/auth/v1/callback` → paste ID and secret into
*Authentication → Providers → Google* → add `http://localhost:3000/auth/callback`
to *URL Configuration → Redirect URLs*.

**3. Env.** One `.env` at the repository root drives the API:

```
SUPABASE_URL=https://<ref>.supabase.co
SUPABASE_SERVICE_ROLE_KEY=<service_role key>   # bypasses RLS — server only
SUPABASE_ANON_KEY=<anon key>
ALLOWED_ORIGINS=http://localhost:3000
```

`frontend/.env.local` gets the three `NEXT_PUBLIC_*` values (see
`.env.local.example`). Copy from `.env.example` / `frontend/.env.local.example`.

**4. Run.**

```bash
cd backend  && go run .   # :8080
cd frontend && npm install && npm run dev   # :3000
```

---

## Layout

```
Dockerfile                  API image (Cloud Run builds this)
.env                        API config — the only backend env file
backend/
  main.go                   entrypoint, graceful shutdown
  config/                   env loading
  db/                       PostgREST client + SQLSTATE error mapping
  handlers/                 routing and request handlers
  middleware/               Supabase JWT verification (JWKS), role gating
  models/                   wire types — JSON tags are the API contract
  httpx/                    JSON helpers, the typed error shape
  realtime/                 WebSocket hub
frontend/src/
  app/                      routes; w/[slug]/ is the workspace shell
  components/               board, onboarding, workspace, ui
  lib/api.ts                the only data path to the backend
supabase/schema.sql         the entire database, one file
```

---

## Roles

| Role | Can |
| --- | --- |
| **owner** | Everything. Exactly one per workspace (partial unique index). |
| **admin** | Settings, join code, add/remove members. |
| **manager** | Projects, teams, approve joins, view anyone's time. |
| **contributor** | Create/edit tasks, comment, track time. |
| **guest** | Read-only. |

---

## Notable details

**Board ordering.** Fractional `board_rank` doubles — a drag is one `UPDATE`,
not a column renumber. The client sends the neighbours' ranks and the server
picks the midpoint, so two simultaneous drags can't corrupt order. `move_task`
re-spreads the column when ranks get too close to split.

**Dependency cycles.** A trigger runs a recursive CTE on insert and rejects any
`blocks` edge that would close a loop. Sub-task parenting is guarded the same way.

**One timer per person.** Partial unique index on `time_entries (user_id) WHERE
ended_at IS NULL` turns a double-click into a 409.

**Board reads are one round trip.** A card carries five aggregates and its
labels. Over plain PostgREST that is an HTTP call per card per count, so
`task_list` / `task_detail` keep it as SQL — see section 8 of `schema.sql`.

**Optimistic drags.** The board applies the move locally, reconciles with the
response, rolls back on failure.

---

## API

`/api/v1`, `Authorization: Bearer <supabase-jwt>`. Workspace routes take the slug
or the UUID. Errors are always `{ "code": "...", "message": "..." }` with a
stable `code` the frontend switches on.

```
GET    /healthz                              unauthenticated

GET    /me · PATCH /me · GET /me/workspaces · GET /me/tasks
POST   /workspaces · POST /workspaces/join · GET /workspaces/lookup?reference=

GET    /workspaces/{w}                       PATCH (admin+)
POST   /workspaces/{w}/rotate-join-code      admin+
GET    /workspaces/{w}/members               PATCH/DELETE {userID} (manager/admin)
GET    /workspaces/{w}/join-requests         POST {id} — approve/decline (manager+)

GET    /workspaces/{w}/projects              POST (manager+)
GET    /workspaces/{w}/projects/{id}         PATCH (manager+)

GET    /workspaces/{w}/tasks                 project_id, assignee_id, state,
                                             priority, search, parent_id,
                                             due_before, due_after, include_done
POST   /workspaces/{w}/tasks
GET/PATCH/DELETE /workspaces/{w}/tasks/{id}
POST   /workspaces/{w}/tasks/{id}/move       drag & drop

GET/POST /workspaces/{w}/tasks/{id}/comments      @mentions fan out
GET    /workspaces/{w}/tasks/{id}/activity
GET/POST /workspaces/{w}/tasks/{id}/dependencies
DELETE /workspaces/{w}/dependencies/{id}

GET/POST /workspaces/{w}/time-entries
POST   /workspaces/{w}/timer/start · /stop   GET /timer/active
GET    /workspaces/{w}/workload

GET/POST /workspaces/{w}/teams · /labels
GET    /workspaces/{w}/activity · /notifications
POST   /workspaces/{w}/notifications/read
GET    /workspaces/{w}/events                WebSocket (?access_token=)
```

---

## Branches

`develop` is the default and where work lands. `main` is production — Cloud Run
builds the root `Dockerfile` on push.

---

## Status

MVP in progress. Backend builds and vets clean; frontend typechecks and builds.

**Not yet verified against a live database:** `schema.sql` section 8 (the API
RPCs) and the PostgREST data layer have not been run against a real Supabase
project. Apply the schema and exercise the API before trusting it.

**Schema exists, UI doesn't:** docs (`docs`, `doc_revisions`), channel messaging
(`channels`, `messages`), email invites (`workspace_invites`), availability
editor (`availability_blocks`).

## Licence

[MIT](LICENSE) © NeuroBrains · Contributions: [CONTRIBUTING.md](CONTRIBUTING.md)
