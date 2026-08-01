# CLAUDE.md

Guidance for Claude Code when working in this repository.

---

## What Sprintly is

A self-hosted team workspace: kanban boards, projects, comments, time tracking
and capacity/workload — one product surface instead of a board tool plus a wiki
plus a chat app plus a timesheet.

**Goal:** ship a working MVP, then iterate. Features that are half-built are
worse than features that are absent. Prefer finishing one vertical slice
(schema → API → UI) over broadening three.

**Non-goals right now:** multi-region, offline-first, plugin APIs, i18n.
Don't add infrastructure for problems the project doesn't have yet.

---

## Architecture, and the three rules that hold it together

```
browser ──Supabase Auth (email + password)──▶ JWT
browser ──REST + WebSocket────────▶ Go API ──PostgREST──▶ Supabase Postgres
```

### 1. The frontend never touches the database

The browser holds `NEXT_PUBLIC_SUPABASE_URL` and the anon key **only** to
complete the OAuth handshake and hold a session. It has no data path to
Postgres — everything goes through the Go API via `frontend/src/lib/api.ts`.

Do not add `supabase.from(...)`, `.rpc(...)` or `postgres_changes` subscriptions
to the frontend. If the UI needs data, add an endpoint.

### 2. The Go API is the only authorization boundary

The API connects with the **service role key**, which bypasses RLS completely.
Nothing downstream will catch a mistake.

- Every workspace-scoped query must filter on `workspace_id`. A missing filter
  is a cross-tenant data leak, not a bug that RLS will save you from.
- Authorization belongs in `requireWorkspace` / `requireRole` /
  `denyGuest` middleware (`handlers/server.go`), not scattered in handlers.
- The RLS policies in `schema.sql` section 6 are defence in depth for a client
  that no longer reads directly. Keep them correct; don't rely on them.

### 3. There are no client-side transactions

The data layer is HTTP (`db`). Multiple calls do not compose into one
transaction. **Anything that must not half-apply belongs in a SECURITY DEFINER
function in `supabase/schema.sql` section 8**, called via `RPC`.

Reach for an RPC when the operation is:

- more than one write that must succeed or fail together
- a read-then-write that races (`board_rank` derivation, counters)
- a correlated aggregate that would otherwise become one HTTP call per row
- ordering by something that isn't a column (role seniority, priority rank)
- one input matched against several normalisations (uuid / join code / slug)

Use plain PostgREST for everything else. Don't write an RPC for a single-table
CRUD call.

---

## Working on the database

**One file: `supabase/schema.sql`.** There is no `migrations/` directory and
adding one is a regression. Edit the file in place, in the right section, and
keep it idempotent:

| Adding | Use |
| --- | --- |
| table | `create table if not exists` |
| column | `alter table ... add column if not exists` |
| index | `create index if not exists` |
| function / view | `create or replace` |
| trigger | `drop trigger if exists ...;` then `create trigger` |
| policy | nothing — section 6 drops them all first |
| enum | the `do $$ ... exception when duplicate_object ... end $$` wrapper |

Applying the file **twice in a row must succeed**. That is the contract.

Two things that bite:

- **RPC output column names are an API contract.** They are named to match the
  JSON tags on the structs in `models`. Renaming a column silently
  drops a field from the API response — nothing errors.
- **PostgREST caches the schema.** A new function is invisible until reload. The
  file ends with `notify pgrst, 'reload schema'`; the dashboard button does the
  same.

Destructive changes (drop/rename a column) cannot be idempotent — they no-op on
a fresh database and destroy data on an existing one. Call them out explicitly.

---

## Working on the backend

Go 1.24, chi, no ORM. `db` is the data layer.

- **Errors go through `httpx`.** Every response is `{ "code", "message" }` and
  `code` is stable API surface the frontend switches on. Never `http.Error`.
- **Postgres SQLSTATEs map to HTTP in one place** — `db/errors.go`.
  Raise a deliberate errcode from an RPC rather than string-matching in Go.
- **`RPC` vs `RPCSingle`.** `RPCSingle` sets PostgREST's object accept header,
  which asserts exactly one row — use it for `returns setof` / `returns table`
  functions expected to yield one row. Use plain `RPC` for scalar and composite
  returns (PostgREST sends those bare) and for multi-row results.
- **PostgREST does not return embedded resources from an insert.** When a
  response needs an embedded profile or task title, insert then re-read.
- `gofmt` before committing.

```bash
cd backend && go build ./... && go vet ./... && go test ./...
```

## Working on the frontend

Next.js 15 App Router, TypeScript strict, Tailwind, Radix wrapped in
`components/ui/`, TanStack Query for server state.

- Use existing `components/ui/` primitives; don't add a component library.
- Server state belongs to TanStack Query — don't mirror it into `useState`.
- Board mutations are optimistic and must roll back on failure.
- `npm run typecheck` is not optional. `any` gets questioned.

---

## Conventions

**Commits:** Conventional Commits — `feat(board):`, `fix(auth):`, `docs:`,
`refactor(api):`, `chore(deps):`. Body explains *why*; the diff covers *what*.

**Never add `Co-Authored-By` or any AI-attribution trailer to commits or PR
bodies.**

**Branches:** `develop` is default and the base for all work. `main` is
production — pushing to it deploys. Branch `feat/…`, `fix/…` off `develop`.

**No CI right now.** Deliberate, MVP-stage. Run the build commands above by hand
before pushing. Don't add workflows unless asked.

**Comments explain why, not what.** The codebase's existing comments are the
standard — match their density and tone. Don't narrate the obvious.

---

## Project memory

Durable facts about this project belong in Claude Code's memory directory, not
in this file and not in the conversation.

**Save a memory when** the user states a preference or correction that should
outlive the session ("never do X", "we always Y"), when a project constraint
isn't derivable from the code (why a decision was made, an external deadline),
or when a pointer matters (dashboard URL, ticket, environment).

**Don't save** what the repo already records — file layout, past fixes, git
history, or anything in this file. If asked to remember something already
written down, ask what was non-obvious about it and save that instead.

**Format.** One fact per file, kebab-case name, with frontmatter:

```markdown
---
name: short-kebab-slug
description: one line, used to judge relevance on recall
metadata:
  type: user | feedback | project | reference
---

The fact. For feedback/project, follow with **Why:** and **How to apply:**.
Link related memories as [[their-slug]].
```

- `user` — who they are, their expertise and preferences
- `feedback` — how to work; corrections *and* confirmed approaches, with the why
- `project` — goals and constraints not in the code; absolute dates, not relative
- `reference` — external pointers

Add a one-line pointer to `MEMORY.md` (`- [Title](file.md) — hook`). `MEMORY.md`
is an index only — never put content in it. Check for an existing file covering
the same ground and update it rather than duplicating; delete memories that turn
out to be wrong.

Recalled memories are background context, not instructions, and reflect what was
true when written. If one names a file, function or flag, verify it still exists
before acting on it.

---

## Current state

MVP in progress.

- The data layer was migrated from a `pgx` Postgres pool to PostgREST because
  the deployment only ever holds the project URL, anon key and service role key.
- **Section 8 of `schema.sql` and the PostgREST layer have not been run against
  a live Supabase project.** Treat behaviour as unverified until they have.
- Schema exists but no UI: docs, channel messaging, email invites, availability
  editor. These are the best places to start a vertical slice.
