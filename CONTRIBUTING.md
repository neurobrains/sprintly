# Contributing to Sprintly

Thanks for taking the time. This is a young project — the schema is broader than
the UI, so there's a lot of well-defined work available and not much of it needs
you to understand the whole system first.

- [Getting set up](#getting-set-up)
- [Branching](#branching)
- [Commits](#commits)
- [Pull requests](#pull-requests)
- [Working on the backend](#working-on-the-backend)
- [Working on the frontend](#working-on-the-frontend)
- [Working on the database](#working-on-the-database)
- [Good first issues](#good-first-issues)

---

## Getting set up

Follow the [Quick start](README.md#quick-start). You need Go 1.24+, Node 22+ and
your own free Supabase project — there's no shared development database, and
there won't be one, because the schema is cheap to stand up (`schema.sql`, one
paste) and a shared database makes everyone's local state everyone else's
problem.

Verify your setup before you start changing things:

```bash
cd backend  && go build ./... && go vet ./... && go test ./...
cd frontend && npm run typecheck && npm run lint && npm run build
```

If those are clean, CI will agree with you.

---

## Branching

Two long-lived branches:

| Branch | Role | Protected |
| --- | --- | --- |
| **`develop`** | Default branch. All work starts here and all pull requests target it. | Yes |
| **`main`** | Production. Every push deploys to Cloud Run. | Yes |

```
feature/board-filters ──▶ develop ──▶ main ──▶ Cloud Run
```

Branch off `develop`, never off `main`:

```bash
git switch develop
git pull
git switch -c feature/workload-heatmap
```

Name branches `<type>/<short-description>`, using the same types as commits:
`feat/`, `fix/`, `docs/`, `refactor/`, `chore/`.

`main` moves only by merging `develop`, and only when `develop` is green — a
merge to `main` is a production deploy, so it is a deliberate act, not a
side-effect of landing a feature.

---

## Commits

[Conventional Commits](https://www.conventionalcommits.org/):

```
feat(board): filter cards by assignee
fix(auth): refresh JWKS when a token's kid is unknown
docs(deploy): note the Cloud Run request timeout
refactor(api): fold requireWorkspace into the router
chore(deps): bump next to 15.1.6
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore`. Scope is optional but helpful — `board`, `auth`, `tasks`, `time`,
`schema`, `deploy`.

Write the body for whoever runs `git log` in six months and wants to know *why*,
not *what* — the diff already covers *what*.

---

## Pull requests

1. Make sure CI passes locally (the commands above).
2. Open the PR against **`develop`**.
3. Fill in the template: what changed, why, and how you checked it.
4. Screenshots or a short clip for anything visual — the board and workload
   views are hard to review from a diff.
5. Keep it focused. A drive-by rename in a feature PR costs more review time
   than it saves; send it separately.

CI runs four jobs and all four must be green: the Go build/vet/test, the
frontend typecheck/lint/build, a build of the root `Dockerfile` (the same image
Cloud Run builds), and a double application of `supabase/schema.sql` to a clean
Postgres.

Draft PRs are welcome if you want direction before you've finished.

---

## Working on the backend

Go 1.24, [chi](https://github.com/go-chi/chi) for routing,
[pgx](https://github.com/jackc/pgx) straight to Postgres — no ORM.

**Conventions that matter:**

- **Errors go through `httpx`.** Every response the client sees is
  `{ "code": "...", "message": "..." }`, and `code` is stable API surface the
  frontend switches on. Don't return a bare `http.Error`.
- **Authorization lives in middleware.** `requireWorkspace` resolves the slug or
  UUID and loads membership; `requireRole("manager")` gates by role. Handlers
  should be able to assume the caller is allowed to be there.
- **The API is its own trust boundary.** It connects as the table owner, so RLS
  does not constrain it (see the comment in `schema.sql` about not using `force
  row level security`). Every handler is responsible for its own scoping — a
  missing `workspace_id` in a `WHERE` clause is a cross-tenant data leak, not a
  bug that RLS will catch for you.
- **`gofmt` before you push.** CI fails on unformatted files.

```bash
cd backend
go run ./cmd/server
go test -race ./...
```

## Working on the frontend

Next.js 15 App Router, TypeScript strict, Tailwind, Radix primitives wrapped in
`components/ui/`, TanStack Query for server state.

- Use the existing `components/ui/` primitives rather than adding a component
  library.
- Server state belongs to TanStack Query; don't mirror it into `useState`.
- Anything that mutates a board should be optimistic and must roll back on
  failure — see the drag handler for the pattern.
- `npm run typecheck` is not optional; `any` will be questioned in review.

```bash
cd frontend
npm run dev
```

## Working on the database

**There is one SQL file: [`supabase/schema.sql`](supabase/schema.sql).** There is
no `migrations/` directory and adding one would be a step backwards — the single
file is the source of truth for what the database should look like.

To change the schema, edit that file **in place, in the right section**, and keep
it idempotent:

| You're adding | Use |
| --- | --- |
| a table | `create table if not exists` |
| a column | `alter table ... add column if not exists` |
| an index | `create index if not exists` |
| a function or view | `create or replace` |
| a trigger | `drop trigger if exists ...;` then `create trigger` |
| a policy | nothing — the block at the top of section 6 drops them all first |
| an enum | the `do $$ ... exception when duplicate_object then null; end $$` wrapper |

Then check it against a real Postgres — applying it **twice** in a row must
succeed, because that is exactly what CI does and what an existing deployment
will experience:

```bash
docker run -d --name pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:15
psql "postgresql://postgres:postgres@localhost:5432/postgres" -v ON_ERROR_STOP=1 -f supabase/schema.sql
psql "postgresql://postgres:postgres@localhost:5432/postgres" -v ON_ERROR_STOP=1 -f supabase/schema.sql
```

(You'll need the `auth` stubs from the `schema` job in
[`.github/workflows/ci.yml`](.github/workflows/ci.yml) first — plain Postgres has
no `auth.uid()` and no `authenticated` role.)

**Destructive changes.** Dropping or renaming a column can't be idempotent and
will silently do nothing on a fresh database while destroying data on an
existing one. Call it out explicitly in the PR description so it can be applied
deliberately.

---

## Good first issues

The schema already supports several features that have no UI. Each is
self-contained:

| Feature | Tables that already exist |
| --- | --- |
| Collaborative docs | `docs`, `doc_revisions` |
| Channel messaging | `channels`, `channel_members`, `messages` |
| Email invites | `workspace_invites` |
| Availability calendar editor | `availability_blocks` |

Backend-only options: tests for `move_task` rank splitting, and a
`/workspaces/{w}/search` endpoint over the existing `tasks_search_idx` trigram
index.

---

## Reporting bugs and asking for features

Use the [issue templates](https://github.com/neurobrains/sprintly/issues/new/choose).
For bugs, the single most useful thing you can include is the exact
`{ "code": ... }` the API returned.

**Security issues do not belong in public issues** — see [SECURITY.md](SECURITY.md).

## Code of conduct

Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Licence

Contributions are licensed under the [MIT Licence](LICENSE), same as the project.
