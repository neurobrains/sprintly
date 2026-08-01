<!--
Target branch should be `develop`. PRs into `main` are production deploys and
are opened by maintainers only.
-->

## What

<!-- One or two sentences. What does this change do? -->

## Why

<!-- The problem or issue behind it. "Closes #123" if there is one. -->

## How it was verified

<!-- What you actually ran or clicked. Not the CI badge — CI is a floor. -->

- [ ] `cd backend && go build ./... && go vet ./... && go test ./...`
- [ ] `cd frontend && npm run typecheck && npm run lint && npm run build`
- [ ] Exercised the change in a running app

## Screenshots

<!-- Required for anything visual. Before/after if you changed existing UI. -->

## Notes for the reviewer

<!--
Anything worth flagging. In particular:

- Schema changes: which section of supabase/schema.sql, and confirm applying it
  twice in a row still succeeds.
- Destructive schema changes (dropped/renamed columns) — these cannot be made
  idempotent. Say so explicitly.
- New environment variables — they also need adding to .env.example and to
  docs/DEPLOYMENT.md.
- New API error `code` values — the frontend switches on these.
-->
