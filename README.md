<div align="center">

# Sprintly

**One workspace for the whole team.** Boards, docs, discussion, time tracking
and workload — together, so work stops scattering across five tools.

[![CI](https://github.com/neurobrains/sprintly/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/neurobrains/sprintly/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white)](backend/go.mod)
[![Next.js](https://img.shields.io/badge/next.js-15-black?logo=nextdotjs)](frontend/package.json)
[![Postgres](https://img.shields.io/badge/postgres-15-4169E1?logo=postgresql&logoColor=white)](supabase/schema.sql)

Go API · Next.js 15 frontend · Supabase (Postgres + Auth) · Google sign-in

[Quick start](#quick-start) ·
[Architecture](#architecture) ·
[Deployment](docs/DEPLOYMENT.md) ·
[Contributing](CONTRIBUTING.md)

</div>

---

## The problem this solves

Research into what teams actually struggle with in 2026 points at four things,
and each one shapes a part of the schema:

| Problem | What teams report | Sprintly's answer |
| --- | --- | --- |
| **Information silos** | Work, decisions and context live in different apps; nobody can find why something was decided. | Tasks, docs, comments, channels and activity share one workspace and one `workspace_id`. |
| **Tool proliferation** | Teams ran 5–7 collaboration apps in 2023 and are consolidating to 2–3. | A single product surface instead of a board tool + a wiki + a chat app + a timesheet. |
| **No visibility into who's doing what** | Leaders can't see status without asking; remote teams drift into micromanagement. | Every task has an owner, state and due date; presence shows online / in a meeting / focus mode. |
| **Capacity is guesswork → burnout** | Overload is invisible until someone breaks. | Time tracking, `weekly_capacity_hours` per member, and a workload view that flags anyone over 100%. |

There's also a timing angle: Atlassian stops selling new self-hosted licences in
March 2026 and ends them entirely in 2029, which is pushing teams toward
self-hostable alternatives. Sprintly is built to be self-hosted from day one.

---

## Quick start

You need **Go 1.24+**, **Node 22+**, and a free Supabase project.

```bash
git clone https://github.com/neurobrains/sprintly.git
cd sprintly
```

**1. Database.** In your Supabase project's SQL Editor, paste and run
[`supabase/schema.sql`](supabase/schema.sql). That's the entire schema — one
file, and it's safe to re-run.

**2. Google sign-in.** Create an OAuth client in the Google Cloud Console with
redirect URI `https://<project-ref>.supabase.co/auth/v1/callback`, paste the ID
and secret into *Supabase → Authentication → Providers → Google*, then add
`http://localhost:3000/auth/callback` under *URL Configuration → Redirect URLs*.
Full walkthrough in [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#1-supabase).

**3. API.**

```bash
cd backend
cp .env.example .env      # fill in DATABASE_URL and SUPABASE_URL
go mod download
go run ./cmd/server       # http://localhost:8080
```

**4. Frontend.**

```bash
cd frontend
cp .env.local.example .env.local   # fill in the two NEXT_PUBLIC_SUPABASE_* values
npm install
npm run dev                        # http://localhost:3000
```

Or bring both up at once against a hosted Supabase project:

```bash
cp .env.example .env && $EDITOR .env
docker compose up --build
```

`DATABASE_URL` comes from *Supabase → Project Settings → Database → Connection
string*. Use port **6543** (the transaction pooler) — the connection pool is
already sized for it.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  frontend/   Next.js 15 (App Router) · TypeScript            │
│              Tailwind · Radix · dnd-kit · TanStack Query     │
└───────────────┬──────────────────────────┬───────────────────┘
                │ REST + WebSocket         │ Supabase Auth (Google OAuth)
┌───────────────▼──────────────────────────┼───────────────────┐
│  backend/    Go 1.24 · chi · pgx         │                   │
│              JWT verification via JWKS   │                   │
│              WebSocket hub for live events                   │
└───────────────┬──────────────────────────┴───────────────────┘
                │ Postgres wire protocol
┌───────────────▼──────────────────────────────────────────────┐
│  supabase/   Postgres 15 · RLS · triggers · RPCs             │
└──────────────────────────────────────────────────────────────┘
```

**Why the API verifies its own JWTs.** Supabase Auth issues the token, but
authorization (who may edit this workspace's tasks) lives in Go, so the frontend
never needs a second trust boundary. RLS policies still exist so the browser can
read its own workspace directly for Supabase Realtime subscriptions.

**Why the schema is one file.** `supabase/schema.sql` is idempotent — every
object is `if not exists`, `or replace`, or dropped first. There is no migration
ordering to reason about and no drift between "what the migrations produce" and
"what the database looks like"; CI applies it twice on a clean Postgres to prove
both properties hold.

### Layout

```
sprintly/
├── Dockerfile                        API image — the one Cloud Run builds
├── .dockerignore                     keeps frontend/ out of the build context
├── docker-compose.yml                API + web against hosted Supabase
├── backend/
│   ├── cmd/server/main.go            entrypoint, graceful shutdown
│   └── internal/
│       ├── api/                      routing, middleware, handlers
│       │   ├── server.go             routes + requireWorkspace/requireRole
│       │   ├── account.go            /me, /me/workspaces, /me/tasks
│       │   ├── workspaces.go         create, join, members, join requests
│       │   ├── projects.go           projects + progress
│       │   ├── tasks.go              CRUD, board move, activity trail
│       │   ├── comments.go           comments, @mentions, dependencies
│       │   ├── time.go               timers, time entries, workload
│       │   └── misc.go               teams, labels, notifications, WebSocket
│       ├── auth/                     Supabase JWT verification (JWKS + HS256)
│       ├── config/                   env loading
│       ├── db/                       pgx pool + Postgres error mapping
│       ├── httpx/                    JSON helpers, typed errors
│       ├── models/                   wire types
│       └── realtime/                 WebSocket hub
├── frontend/
│   └── src/
│       ├── app/
│       │   ├── page.tsx              landing
│       │   ├── login/                Google sign-in
│       │   ├── auth/callback/        OAuth code exchange
│       │   ├── onboarding/           create OR join workspace
│       │   ├── app/                  post-login router
│       │   └── w/[slug]/             board, list, calendar, timeline,
│       │                             inbox, workload, time, activity, settings
│       ├── components/
│       │   ├── board/                kanban, task card, detail sheet
│       │   ├── onboarding/           the create/join panel
│       │   ├── workspace/            provider, sidebar, topbar
│       │   └── ui/                   button, input, avatar, dialog…
│       ├── hooks/use-realtime.ts     WebSocket with backoff reconnect
│       ├── lib/                      api client, types, supabase clients
│       └── middleware.ts             session refresh + route gating
├── supabase/
│   ├── schema.sql                    the entire database, one idempotent file
│   └── config.toml                   Supabase CLI config for `supabase start`
└── docs/DEPLOYMENT.md                Supabase + Cloud Run + frontend
```

---

## Onboarding flow

After Google sign-in, `/app` looks at what the user has:

- **No workspaces** → `/onboarding`, which offers exactly two paths.
- **Has workspaces** → straight into the last one they used.

**Create new** asks for a name and a join policy, then one transaction
(`create_workspace`) provisions the workspace, makes you owner, and seeds a
default team, a starter project with four tasks, a `#general` channel and four
labels — so the board is never an empty void on first login.

**Join existing** takes a **workspace UUID or the short join code**
(`SPRNT-7QK2XM`). It previews the workspace before you commit — name, member
count, whether approval is needed — so a mistyped code fails at the preview
rather than after you hit Join. What happens next depends on the workspace's
policy:

| Policy | Result |
| --- | --- |
| `open` | Joined instantly as a contributor. |
| `request` | A join request is filed; owners/admins/managers get a notification and approve or decline in *Settings → Members*. |
| `invite_only` | Rejected — the join code doesn't work. |

Admins can rotate the join code at any time, which invalidates the old one.

---

## Roles

| Role | Can |
| --- | --- |
| **owner** | Everything, including deleting the workspace. Exactly one per workspace (enforced by a partial unique index). |
| **admin** | Workspace settings, join code, add/remove members. |
| **manager** | Projects, teams, approve join requests, view anyone's time. |
| **contributor** | Create and edit tasks, comment, track time. |
| **guest** | Read-only. |

Enforced in three places: `requireRole` middleware in Go, `has_workspace_role()`
in the RLS policies, and `canManage` / `canEdit` in the workspace provider for
UI affordances.

---

## Notable implementation details

**Board ordering.** Cards use a fractional `board_rank` (doubles), so dragging a
card between two others is a single `UPDATE` rather than renumbering the column.
The client sends the ranks of the neighbours it dropped between and the server
picks the midpoint — two people dragging at once can't corrupt the order. When
adjacent ranks get too close to split, `move_task` re-spreads that column.

**Dependency cycles.** `blocks` edges form the Gantt critical path, so a trigger
runs a recursive CTE on insert and rejects any edge that would close a loop.
Sub-task parenting is guarded the same way.

**Live updates.** Two channels, deliberately: Supabase Realtime streams raw row
changes for anything the browser reads directly, and the Go WebSocket hub carries
semantic events (`task.moved` with the actor, presence, typing) that a row diff
can't express. The client reconnects with exponential backoff and re-reads the
access token each attempt, so an expired JWT doesn't wedge the socket.

**One timer per person.** A partial unique index on `time_entries (user_id) WHERE
ended_at IS NULL` turns a double-click into a 409 instead of two overlapping
timers.

**Optimistic drags.** The board applies the move locally, then reconciles with
the server response and rolls back on failure.

---

## API

All routes are under `/api/v1` and need `Authorization: Bearer <supabase-jwt>`.
Workspace-scoped routes accept the slug or the UUID interchangeably.

```
GET    /healthz                              liveness (unauthenticated)

GET    /me                                   profile (upserts from the JWT)
PATCH  /me                                   name, timezone, presence
GET    /me/workspaces                        memberships + pending join requests
GET    /me/tasks                             cross-workspace "My Work"

POST   /workspaces                           create
POST   /workspaces/join                      join by UUID or code
GET    /workspaces/lookup?reference=         preview before joining

GET    /workspaces/{w}                       workspace
PATCH  /workspaces/{w}                       admin+
POST   /workspaces/{w}/rotate-join-code      admin+
GET    /workspaces/{w}/members
PATCH  /workspaces/{w}/members/{userID}      manager+
DELETE /workspaces/{w}/members/{userID}      admin+
GET    /workspaces/{w}/join-requests         manager+
POST   /workspaces/{w}/join-requests/{id}    approve / decline

GET    /workspaces/{w}/projects
POST   /workspaces/{w}/projects              manager+
GET    /workspaces/{w}/projects/{id}
PATCH  /workspaces/{w}/projects/{id}         manager+

GET    /workspaces/{w}/tasks                 filters: project_id, assignee_id,
                                             state, priority, search, parent_id,
                                             due_before, due_after, include_done
POST   /workspaces/{w}/tasks
GET    /workspaces/{w}/tasks/{id}
PATCH  /workspaces/{w}/tasks/{id}
POST   /workspaces/{w}/tasks/{id}/move       drag & drop
DELETE /workspaces/{w}/tasks/{id}

GET    /workspaces/{w}/tasks/{id}/comments
POST   /workspaces/{w}/tasks/{id}/comments   @mentions fan out to notifications
GET    /workspaces/{w}/tasks/{id}/activity
GET    /workspaces/{w}/tasks/{id}/dependencies
POST   /workspaces/{w}/tasks/{id}/dependencies
DELETE /workspaces/{w}/dependencies/{id}

GET    /workspaces/{w}/time-entries
POST   /workspaces/{w}/time-entries          manual log
POST   /workspaces/{w}/timer/start
POST   /workspaces/{w}/timer/stop
GET    /workspaces/{w}/timer/active
GET    /workspaces/{w}/workload              capacity + availability

GET    /workspaces/{w}/teams  ·  POST (manager+)
GET    /workspaces/{w}/labels ·  POST
GET    /workspaces/{w}/activity
GET    /workspaces/{w}/notifications
POST   /workspaces/{w}/notifications/read
GET    /workspaces/{w}/events                WebSocket (?access_token=)
```

Errors are always `{ "code": "...", "message": "..." }` with a stable `code`.

---

## Deployment

Push to `main` and Cloud Build builds the root `Dockerfile` and rolls out a new
Cloud Run revision. Three settings are load-bearing for the WebSocket hub —
request timeout `3600s`, session affinity on, CPU always allocated — and are
easy to miss. See **[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)** for the full
walkthrough including Secret Manager, `ALLOWED_ORIGINS`, and the frontend.

---

## Branches

| Branch | Role |
| --- | --- |
| `develop` | Default. All work branches off it and all PRs target it. |
| `main` | Production. Every push here deploys to Cloud Run. |

`main` only ever moves by merging `develop`. See
[CONTRIBUTING.md](CONTRIBUTING.md#branching).

---

## Project status

- `go build ./...` and `go vet ./...` — clean.
- `tsc --noEmit` and `next build` — clean, 17 routes.
- **`supabase/schema.sql` has not been run against a live Supabase project.** It
  was written without a Postgres available. CI now applies it twice to a clean
  Postgres 15 with stubbed `auth` objects, which catches syntax and ordering
  errors, but not anything specific to Supabase's own roles and extensions.
  Watch the SQL Editor output the first time you apply it.

**Schema exists, UI doesn't yet:** collaborative docs (`docs`, `doc_revisions`),
channel messaging (`channels`, `messages`), email invites
(`workspace_invites`), and the availability calendar editor. These are the most
useful places to start contributing.

---

## Contributing

Issues and pull requests are welcome — start with
[CONTRIBUTING.md](CONTRIBUTING.md). Please report security issues privately via
[SECURITY.md](SECURITY.md) rather than in a public issue.

## Licence

[MIT](LICENSE) © NeuroBrains
