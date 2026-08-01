# Security Policy

## Reporting a vulnerability

**Please don't open a public issue.** Sprintly is self-hosted, so a public report
is a working exploit against every deployment that hasn't updated yet.

Report privately through GitHub:
**[Security → Report a vulnerability](https://github.com/neurobrains/sprintly/security/advisories/new)**

Include what you have — a rough report is far better than none:

- What the flaw lets an attacker do (read another workspace's tasks, escalate
  from `guest` to `admin`, and so on).
- The endpoint, SQL function or component involved.
- Steps to reproduce, or a request/response pair.
- Which version or commit you tested.

**What to expect:** acknowledgement within 3 business days, an assessment within
7, and a fix released as soon as it's ready and verified. We'll credit you in
the advisory unless you'd rather stay anonymous.

Please give us a reasonable window to ship a fix before disclosing publicly.

## Supported versions

Pre-1.0, so only the tip of `main` receives fixes. Deployments track `main`
through Cloud Run, so updating means pulling and redeploying.

## Scope

**In scope**

- Cross-workspace data access — any path where a member of workspace A can read
  or write workspace B's rows.
- Role escalation past what [the role table](README.md#roles) permits.
- Authentication bypass: forged, expired or wrong-audience JWTs being accepted.
- SQL injection, or an RLS policy that doesn't hold up.
- Secrets leaking into logs, error responses or the client bundle.

**Out of scope**

- Anything requiring the operator's own `DATABASE_URL` or Supabase service key.
  Whoever holds those already owns the deployment.
- Misconfiguration of a self-hosted instance (`ALLOWED_ORIGINS` set to `*`,
  public database port, RLS disabled by hand).
- Vulnerabilities in Supabase, Google Cloud or Google OAuth — report those to
  the respective vendor.
- Denial of service through sheer volume against an unrated instance.
- Missing hardening headers with no demonstrated impact.

## A note on the trust model

Two things look like vulnerabilities but are deliberate, and are documented where
they're implemented:

**The Go API is not constrained by RLS.** It connects as the table owner, and
`schema.sql` deliberately uses `enable row level security` rather than `force`.
Authorization is enforced in Go, by `requireWorkspace` and `requireRole`. The RLS
policies exist for the browser's direct Supabase Realtime reads. A handler that
forgets to scope a query by `workspace_id` **is** a real vulnerability — please
report it — but "the service role bypasses RLS" on its own is by design.

**The Supabase `anon` key is public.** It ships in the client bundle and is meant
to. It grants nothing on its own; RLS governs what a session can reach. Finding
it in the JavaScript is not a finding.
