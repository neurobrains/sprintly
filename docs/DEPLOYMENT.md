# Deploying Sprintly

Sprintly is three deployables: a Postgres schema on Supabase, the Go API on
Cloud Run, and the Next.js frontend anywhere that runs Node. Do them in that
order — the API refuses to start without a reachable database, and the frontend
needs the API's URL.

| Piece | Where it runs | How it ships |
| --- | --- | --- |
| `supabase/schema.sql` | Supabase Postgres | Applied by hand (or `psql -f`) |
| `Dockerfile` → Go API | Cloud Run | Auto-built on every push to `main` |
| `frontend/` | Vercel / Cloud Run / anywhere | Separate build |

---

## 1. Supabase

Create a project at [supabase.com](https://supabase.com), open the **SQL
Editor**, paste the whole of [`supabase/schema.sql`](../supabase/schema.sql) and
run it. It is one file and it is idempotent — re-running it is how you apply
later schema changes too.

From the CLI instead:

```bash
psql "$DATABASE_URL" -f supabase/schema.sql
```

Then wire up Google sign-in:

1. **Google Cloud Console** → *APIs & Services* → *Credentials* → *Create OAuth
   client ID* → *Web application*.
2. Authorised redirect URI: `https://<project-ref>.supabase.co/auth/v1/callback`
3. **Supabase** → *Authentication* → *Providers* → *Google*: paste the client ID
   and secret, enable.
4. **Supabase** → *Authentication* → *URL Configuration* → *Redirect URLs*: add
   `http://localhost:3000/auth/callback` and `https://<your-domain>/auth/callback`.

Collect these from *Project Settings*, you need them in step 2:

| Value | Where |
| --- | --- |
| `DATABASE_URL` | *Database* → *Connection string* → *URI*. Use port **6543** (transaction pooler). |
| `SUPABASE_URL` | *API* → *Project URL* |
| `SUPABASE_ANON_KEY` | *API* → *Project API keys* → `anon` `public` |

---

## 2. The API on Cloud Run

### One-time setup

In the Google Cloud console: **Cloud Run** → **Deploy container** → **Continuous
deployment from a repository** → **Set up with Cloud Build**.

| Field | Value | Why |
| --- | --- | --- |
| Repository | `neurobrains/sprintly` | Authorise the Cloud Build GitHub App first |
| Branch | `^main$` | `main` is the production branch; `develop` never deploys |
| Build type | **Dockerfile** | Not Buildpacks — the repo is a monorepo and buildpacks would guess wrong |
| Dockerfile path | `/Dockerfile` | The root one, which builds only the Go API |

The [`Dockerfile`](../Dockerfile) sits at the root *because* of this screen:
Cloud Run's GitHub wiring only looks at the root of the build context and gives
you no way to select one nested under `backend/`. It is the only Dockerfile for
the API — a second copy would drift from the one that ships.
[`.dockerignore`](../.dockerignore) strips
`frontend/` and `supabase/` from the upload, so the context stays small and a
frontend-only commit doesn't invalidate the build cache.

### Service settings

| Setting | Value | Why |
| --- | --- | --- |
| Authentication | Allow unauthenticated | The browser calls it directly; the API verifies Supabase JWTs itself |
| Port | `8080` | Cloud Run injects `PORT`; the server already reads it |
| Request timeout | **3600s** | The WebSocket hub holds long-lived connections. At the 300s default every live session drops every five minutes. |
| Session affinity | **On** | Keeps a reconnecting socket on the instance that holds its presence state |
| CPU allocation | **Always allocated** | Idle-throttled CPU stalls the hub's background broadcast goroutine between requests |
| Min instances | `1` | Avoids a cold start on the first request after idle; set `0` if you'd rather pay nothing |
| Max instances | `10` | Cap it — the Supabase pooler has a connection ceiling |

### Environment variables

Set these under *Variables & Secrets*.

| Variable | Value | Kind |
| --- | --- | --- |
| `APP_ENV` | `production` | plain |
| `ALLOWED_ORIGINS` | `https://<your-frontend-domain>` | plain |
| `SUPABASE_URL` | `https://<project-ref>.supabase.co` | plain |
| `SUPABASE_ANON_KEY` | the `anon` key | plain |
| `DATABASE_URL` | the pooler URI, password included | **Secret Manager** |
| `SUPABASE_JWT_SECRET` | only on legacy HS256 projects | **Secret Manager** |

`DATABASE_URL` contains your database password. Put it in Secret Manager and
reference it, rather than pasting it as a plain environment variable where it
shows up in the service YAML and in every deploy log:

```bash
printf '%s' 'postgresql://postgres.<ref>:<password>@...:6543/postgres' \
  | gcloud secrets create sprintly-database-url --data-file=-

gcloud run services update sprintly-api \
  --region europe-west1 \
  --set-secrets DATABASE_URL=sprintly-database-url:latest
```

Grant the service's runtime service account `roles/secretmanager.secretAccessor`
or the container will crash-loop on boot with a permission error.

`ALLOWED_ORIGINS` must be the exact scheme + host of the frontend, no trailing
slash, comma-separated for more than one. Getting it wrong shows up as CORS
failures in the browser, not as an error in the Cloud Run logs.

### What happens on a push

Push to `main` → Cloud Build builds `/Dockerfile` → pushes to Artifact Registry
→ deploys a new Cloud Run revision → shifts 100% of traffic to it. Failed builds
leave the previous revision serving, so a broken commit degrades to "no deploy"
rather than an outage.

CI (`.github/workflows/ci.yml`) runs on the pull request, before the merge that
triggers the build. It builds the same root `Dockerfile`, so a Dockerfile error
surfaces on the PR instead of halfway through a production deploy.

### Manual deploy

```bash
gcloud run deploy sprintly-api \
  --source . \
  --region europe-west1 \
  --allow-unauthenticated \
  --timeout 3600 \
  --session-affinity \
  --no-cpu-throttling
```

### Checking it

```bash
curl https://<service-url>/healthz
gcloud run services logs tail sprintly-api --region europe-west1
```

---

## 3. The frontend

`NEXT_PUBLIC_*` values are inlined at build time, not read at runtime — they
must be present when the bundle is built, and changing one means rebuilding.

| Variable | Value |
| --- | --- |
| `NEXT_PUBLIC_SUPABASE_URL` | `https://<project-ref>.supabase.co` |
| `NEXT_PUBLIC_SUPABASE_ANON_KEY` | the `anon` key |
| `NEXT_PUBLIC_API_URL` | the Cloud Run service URL, no trailing slash |

**Vercel:** import the repo, set root directory to `frontend`, add the three
variables, deploy.

**Cloud Run:** [`frontend/Dockerfile`](../frontend/Dockerfile) takes them as
build args:

```bash
gcloud builds submit frontend \
  --tag gcr.io/$PROJECT/sprintly-web \
  --substitutions _API_URL=https://<api-url>
```

Once the frontend has a URL, go back and add it to `ALLOWED_ORIGINS` on the API
and to Supabase's *Redirect URLs*. Both are easy to forget and both fail at
sign-in rather than at deploy.

---

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Container fails to start, exit before any log | `DATABASE_URL` missing or unreadable — `config.Load()` returns an error and the process exits |
| `401` on every API call | `SUPABASE_URL` wrong, so JWKS can't be fetched and no token verifies |
| CORS errors in the browser | `ALLOWED_ORIGINS` doesn't exactly match the frontend origin |
| WebSocket drops every ~5 minutes | Request timeout still at the 300s default |
| Presence goes stale between requests | CPU is idle-throttled; switch to always-allocated |
| `too many connections` | Using port 5432 (direct) instead of 6543 (pooler), or max instances too high |
