-- ==========================================================================
-- Sprintly :: complete database schema
--
-- One file, applied top to bottom. Paste it into the Supabase SQL Editor, or:
--
--     psql "$DATABASE_URL" -f supabase/schema.sql
--
-- It is idempotent: every object is created with `if not exists`, `or replace`,
-- or dropped first, so re-running it against an existing database is safe and
-- brings the schema back in line with this file.
--
-- Sections
--   1. Extensions, enums and shared helpers
--   2. Identity      profiles, workspaces, membership, teams, invites
--   3. Work          projects, tasks, labels, dependencies, activity
--   4. Collaboration comments, docs, channels, messages, notifications
--   5. Time          time entries, availability, workload views
--   6. Security      row level security policies + realtime publication
--   7. RPCs          onboarding, join requests, board moves
--
-- It does NOT drop tables or data. To start from scratch, drop the public
-- schema first:  drop schema public cascade; create schema public;
-- ==========================================================================


-- ==========================================================================
-- 1. EXTENSIONS, ENUMS AND SHARED HELPERS
-- ==========================================================================

create extension if not exists "pgcrypto";
create extension if not exists "pg_trgm";
create extension if not exists "btree_gist";

-- ---------------------------------------------------------------- enums
do $$ begin
  create type workspace_role as enum ('owner', 'admin', 'manager', 'contributor', 'guest');
exception when duplicate_object then null; end $$;

do $$ begin
  create type join_policy as enum ('open', 'request', 'invite_only');
exception when duplicate_object then null; end $$;

do $$ begin
  create type member_status as enum ('active', 'pending', 'suspended');
exception when duplicate_object then null; end $$;

do $$ begin
  create type presence_state as enum ('online', 'away', 'in_meeting', 'focus', 'offline');
exception when duplicate_object then null; end $$;

do $$ begin
  create type task_state as enum ('backlog', 'todo', 'in_progress', 'review', 'done', 'cancelled');
exception when duplicate_object then null; end $$;

do $$ begin
  create type task_priority as enum ('none', 'low', 'medium', 'high', 'urgent');
exception when duplicate_object then null; end $$;

do $$ begin
  create type dependency_kind as enum ('blocks', 'relates_to', 'duplicates');
exception when duplicate_object then null; end $$;

do $$ begin
  create type notification_kind as enum (
    'mention', 'assignment', 'comment', 'state_change',
    'due_soon', 'join_request', 'invite'
  );
exception when duplicate_object then null; end $$;

-- ---------------------------------------------------------------- helpers

-- updated_at maintenance
create or replace function set_updated_at() returns trigger
language plpgsql as $$
begin
  new.updated_at = now();
  return new;
end $$;

-- Human-friendly workspace join code, e.g. "SPRNT-7QK2XM".
create or replace function gen_join_code() returns text
language plpgsql as $$
declare
  alphabet text := 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'; -- no I/O/0/1
  code text := '';
  i int;
begin
  for i in 1..8 loop
    code := code || substr(alphabet, 1 + floor(random() * length(alphabet))::int, 1);
  end loop;
  return code;
end $$;

-- Monotonic per-project task number ("SPR-114"), allocated under row lock.
create or replace function next_task_number(p_project uuid) returns int
language plpgsql as $$
declare n int;
begin
  update projects
     set task_counter = task_counter + 1
   where id = p_project
  returning task_counter into n;
  if n is null then
    raise exception 'project % not found', p_project using errcode = 'no_data_found';
  end if;
  return n;
end $$;


-- ==========================================================================
-- 2. IDENTITY - profiles, workspaces, membership, teams, invites
-- ==========================================================================

-- Mirror of auth.users we are allowed to join against and expose.
create table if not exists profiles (
  id            uuid primary key references auth.users(id) on delete cascade,
  email         text        not null,
  full_name     text,
  avatar_url    text,
  timezone      text        not null default 'UTC',
  presence      presence_state not null default 'offline',
  presence_note text,
  last_seen_at  timestamptz not null default now(),
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create index if not exists profiles_email_idx on profiles (lower(email));

-- Populate a profile the moment Supabase Auth creates the user (Google OAuth
-- puts the display name + picture in raw_user_meta_data).
create or replace function handle_new_auth_user() returns trigger
language plpgsql security definer set search_path = public as $$
begin
  insert into public.profiles (id, email, full_name, avatar_url)
  values (
    new.id,
    coalesce(new.email, ''),
    coalesce(new.raw_user_meta_data ->> 'full_name', new.raw_user_meta_data ->> 'name'),
    coalesce(new.raw_user_meta_data ->> 'avatar_url', new.raw_user_meta_data ->> 'picture')
  )
  on conflict (id) do update
     set email      = excluded.email,
         full_name  = coalesce(public.profiles.full_name, excluded.full_name),
         avatar_url = coalesce(excluded.avatar_url, public.profiles.avatar_url),
         updated_at = now();
  return new;
end $$;

drop trigger if exists on_auth_user_created on auth.users;
create trigger on_auth_user_created
  after insert or update of email, raw_user_meta_data on auth.users
  for each row execute function handle_new_auth_user();

-- ---------------------------------------------------------------- workspaces
create table if not exists workspaces (
  id          uuid primary key default gen_random_uuid(),
  name        text not null check (length(btrim(name)) between 2 and 80),
  slug        text not null unique check (slug ~ '^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$'),
  join_code   text not null unique default gen_join_code(),
  join_policy join_policy not null default 'request',
  logo_url    text,
  created_by  uuid not null references profiles(id) on delete restrict,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

drop trigger if exists workspaces_touch on workspaces;
create trigger workspaces_touch before update on workspaces
  for each row execute function set_updated_at();

create table if not exists workspace_members (
  workspace_id uuid not null references workspaces(id) on delete cascade,
  user_id      uuid not null references profiles(id) on delete cascade,
  role         workspace_role not null default 'contributor',
  status       member_status  not null default 'active',
  title        text,
  -- Hours/week this member can absorb; drives the capacity indicators.
  weekly_capacity_hours numeric(5,2) not null default 40 check (weekly_capacity_hours >= 0),
  invited_by   uuid references profiles(id) on delete set null,
  joined_at    timestamptz not null default now(),
  primary key (workspace_id, user_id)
);

create index if not exists workspace_members_user_idx on workspace_members (user_id) where status = 'active';

-- Exactly one owner per workspace.
create unique index if not exists workspace_single_owner_idx
  on workspace_members (workspace_id) where role = 'owner';

-- ---------------------------------------------------------------- teams
create table if not exists teams (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name         text not null check (length(btrim(name)) between 1 and 80),
  description  text,
  color        text not null default '#6366f1',
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (workspace_id, name)
);

drop trigger if exists teams_touch on teams;
create trigger teams_touch before update on teams
  for each row execute function set_updated_at();

create table if not exists team_members (
  team_id   uuid not null references teams(id) on delete cascade,
  user_id   uuid not null references profiles(id) on delete cascade,
  is_lead   boolean not null default false,
  added_at  timestamptz not null default now(),
  primary key (team_id, user_id)
);

-- ---------------------------------------------------------------- joining
-- Raised when join_policy = 'request'; an admin/manager approves or rejects.
create table if not exists workspace_join_requests (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  user_id      uuid not null references profiles(id) on delete cascade,
  message      text,
  status       text not null default 'pending' check (status in ('pending','approved','rejected')),
  decided_by   uuid references profiles(id) on delete set null,
  decided_at   timestamptz,
  created_at   timestamptz not null default now(),
  unique (workspace_id, user_id)
);

create index if not exists join_requests_pending_idx
  on workspace_join_requests (workspace_id) where status = 'pending';

-- Email invites (invite_only workspaces, or targeted role grants).
create table if not exists workspace_invites (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  email        text not null,
  role         workspace_role not null default 'contributor',
  token        text not null unique default encode(gen_random_bytes(24), 'hex'),
  invited_by   uuid not null references profiles(id) on delete cascade,
  accepted_by  uuid references profiles(id) on delete set null,
  accepted_at  timestamptz,
  expires_at   timestamptz not null default now() + interval '14 days',
  created_at   timestamptz not null default now()
);

create unique index if not exists workspace_invites_open_idx
  on workspace_invites (workspace_id, lower(email)) where accepted_at is null;


-- ==========================================================================
-- 3. WORK - projects, tasks, labels, dependencies, activity
-- ==========================================================================

create table if not exists projects (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  team_id      uuid references teams(id) on delete set null,
  name         text not null check (length(btrim(name)) between 1 and 120),
  -- Ticket prefix: "SPR" -> SPR-1, SPR-2 ...
  key          text not null check (key ~ '^[A-Z][A-Z0-9]{1,7}$'),
  description  text,
  color        text not null default '#6366f1',
  icon         text,
  start_date   date,
  target_date  date,
  archived_at  timestamptz,
  task_counter int  not null default 0,
  created_by   uuid not null references profiles(id) on delete restrict,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (workspace_id, key),
  check (target_date is null or start_date is null or target_date >= start_date)
);

drop trigger if exists projects_touch on projects;
create trigger projects_touch before update on projects
  for each row execute function set_updated_at();

create index if not exists projects_workspace_idx
  on projects (workspace_id) where archived_at is null;

-- ---------------------------------------------------------------- tasks
create table if not exists tasks (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  project_id   uuid not null references projects(id) on delete cascade,
  parent_id    uuid references tasks(id) on delete cascade,
  number       int  not null,
  title        text not null check (length(btrim(title)) between 1 and 300),
  description  text,
  state        task_state    not null default 'backlog',
  priority     task_priority not null default 'none',
  -- Fractional rank so a drag between two cards is one UPDATE, not a reindex.
  board_rank   double precision not null default 65536,
  assignee_id  uuid references profiles(id) on delete set null,
  reporter_id  uuid references profiles(id) on delete set null,
  start_date   timestamptz,
  due_date     timestamptz,
  estimate_hours numeric(6,2) check (estimate_hours is null or estimate_hours >= 0),
  completed_at timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now(),
  unique (project_id, number),
  check (parent_id is null or parent_id <> id),
  check (due_date is null or start_date is null or due_date >= start_date)
);

drop trigger if exists tasks_touch on tasks;
create trigger tasks_touch before update on tasks
  for each row execute function set_updated_at();

create index if not exists tasks_board_idx    on tasks (project_id, state, board_rank);
create index if not exists tasks_assignee_idx on tasks (assignee_id, state) where completed_at is null;
create index if not exists tasks_due_idx      on tasks (workspace_id, due_date) where completed_at is null;
create index if not exists tasks_parent_idx   on tasks (parent_id);
create index if not exists tasks_search_idx   on tasks using gin (title gin_trgm_ops);

-- Auto-assign the per-project number and keep completed_at in step with state.
create or replace function tasks_before_write() returns trigger
language plpgsql as $$
begin
  if tg_op = 'INSERT' and (new.number is null or new.number = 0) then
    new.number := next_task_number(new.project_id);
  end if;

  if new.state = 'done' and new.completed_at is null then
    new.completed_at := now();
  elsif new.state <> 'done' then
    new.completed_at := null;
  end if;

  return new;
end $$;

drop trigger if exists tasks_before_write_trg on tasks;
create trigger tasks_before_write_trg before insert or update on tasks
  for each row execute function tasks_before_write();

-- Reject sub-task cycles (A -> B -> A) on the parent tree.
create or replace function tasks_guard_cycle() returns trigger
language plpgsql as $$
declare cur uuid := new.parent_id; hops int := 0;
begin
  while cur is not null loop
    if cur = new.id then
      raise exception 'sub-task cycle detected for task %', new.id using errcode = 'check_violation';
    end if;
    hops := hops + 1;
    if hops > 20 then
      raise exception 'sub-task nesting too deep' using errcode = 'check_violation';
    end if;
    select parent_id into cur from tasks where id = cur;
  end loop;
  return new;
end $$;

drop trigger if exists tasks_guard_cycle_trg on tasks;
create trigger tasks_guard_cycle_trg after insert or update of parent_id on tasks
  for each row when (new.parent_id is not null) execute function tasks_guard_cycle();

-- ---------------------------------------------------------------- labels
create table if not exists labels (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name         text not null check (length(btrim(name)) between 1 and 40),
  color        text not null default '#64748b',
  created_at   timestamptz not null default now(),
  unique (workspace_id, name)
);

create table if not exists task_labels (
  task_id  uuid not null references tasks(id)  on delete cascade,
  label_id uuid not null references labels(id) on delete cascade,
  primary key (task_id, label_id)
);

-- Extra assignees beyond the primary owner.
create table if not exists task_watchers (
  task_id uuid not null references tasks(id) on delete cascade,
  user_id uuid not null references profiles(id) on delete cascade,
  primary key (task_id, user_id)
);

-- ---------------------------------------------------------------- dependencies
create table if not exists task_dependencies (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  source_id    uuid not null references tasks(id) on delete cascade,
  target_id    uuid not null references tasks(id) on delete cascade,
  kind         dependency_kind not null default 'blocks',
  created_at   timestamptz not null default now(),
  unique (source_id, target_id, kind),
  check (source_id <> target_id)
);

create index if not exists task_dependencies_target_idx on task_dependencies (target_id);

-- Blocking edges form the Gantt critical path, so they must stay acyclic.
create or replace function deps_guard_cycle() returns trigger
language plpgsql as $$
declare found boolean;
begin
  if new.kind <> 'blocks' then
    return new;
  end if;

  with recursive downstream as (
    select target_id as id from task_dependencies
      where source_id = new.target_id and kind = 'blocks'
    union
    select d.target_id from task_dependencies d
      join downstream w on w.id = d.source_id
     where d.kind = 'blocks'
  )
  select exists (select 1 from downstream where id = new.source_id) into found;

  if found then
    raise exception 'dependency cycle: % already depends on %', new.target_id, new.source_id
      using errcode = 'check_violation';
  end if;
  return new;
end $$;

drop trigger if exists deps_guard_cycle_trg on task_dependencies;
create trigger deps_guard_cycle_trg after insert or update on task_dependencies
  for each row execute function deps_guard_cycle();

-- ---------------------------------------------------------------- activity
create table if not exists activities (
  id           bigserial primary key,
  workspace_id uuid not null references workspaces(id) on delete cascade,
  task_id      uuid references tasks(id) on delete cascade,
  project_id   uuid references projects(id) on delete cascade,
  actor_id     uuid references profiles(id) on delete set null,
  verb         text not null,          -- 'created' | 'state_changed' | 'assigned' | ...
  field        text,
  old_value    text,
  new_value    text,
  meta         jsonb not null default '{}'::jsonb,
  created_at   timestamptz not null default now()
);

create index if not exists activities_task_idx      on activities (task_id, created_at desc);
create index if not exists activities_workspace_idx on activities (workspace_id, created_at desc);


-- ==========================================================================
-- 4. COLLABORATION - comments, docs, channels, messages, notifications
-- ==========================================================================

create table if not exists comments (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  task_id      uuid references tasks(id) on delete cascade,
  doc_id       uuid,                                   -- FK added after docs
  parent_id    uuid references comments(id) on delete cascade,
  author_id    uuid not null references profiles(id) on delete cascade,
  body         text not null check (length(btrim(body)) > 0),
  -- Extracted at write time so notification fan-out never re-parses markdown.
  mentions     uuid[] not null default '{}',
  edited_at    timestamptz,
  created_at   timestamptz not null default now(),
  check (task_id is not null or doc_id is not null)
);

create index if not exists comments_task_idx    on comments (task_id, created_at);
create index if not exists comments_mention_idx on comments using gin (mentions);

-- ---------------------------------------------------------------- docs
create table if not exists docs (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  project_id   uuid references projects(id) on delete set null,
  parent_id    uuid references docs(id) on delete cascade,
  title        text not null default 'Untitled',
  -- Markdown source is the durable copy; `content` holds the editor's JSON tree.
  body_md      text not null default '',
  content      jsonb not null default '{}'::jsonb,
  icon         text,
  is_public    boolean not null default false,
  created_by   uuid not null references profiles(id) on delete restrict,
  updated_by   uuid references profiles(id) on delete set null,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

drop trigger if exists docs_touch on docs;
create trigger docs_touch before update on docs
  for each row execute function set_updated_at();

create index if not exists docs_workspace_idx on docs (workspace_id, updated_at desc);

alter table comments
  drop constraint if exists comments_doc_id_fkey,
  add  constraint comments_doc_id_fkey foreign key (doc_id) references docs(id) on delete cascade;

-- Append-only revision log; powers "restore version" without a CRDT server.
create table if not exists doc_revisions (
  id         bigserial primary key,
  doc_id     uuid not null references docs(id) on delete cascade,
  author_id  uuid references profiles(id) on delete set null,
  body_md    text not null,
  created_at timestamptz not null default now()
);

create index if not exists doc_revisions_doc_idx on doc_revisions (doc_id, created_at desc);

-- ---------------------------------------------------------------- channels
create table if not exists channels (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name         text not null check (name ~ '^[a-z0-9][a-z0-9-]{0,38}$'),
  topic        text,
  is_private   boolean not null default false,
  created_by   uuid not null references profiles(id) on delete restrict,
  created_at   timestamptz not null default now(),
  unique (workspace_id, name)
);

create table if not exists channel_members (
  channel_id   uuid not null references channels(id) on delete cascade,
  user_id      uuid not null references profiles(id) on delete cascade,
  last_read_at timestamptz not null default now(),
  primary key (channel_id, user_id)
);

create table if not exists messages (
  id          uuid primary key default gen_random_uuid(),
  channel_id  uuid not null references channels(id) on delete cascade,
  author_id   uuid not null references profiles(id) on delete cascade,
  -- Non-null makes this message a reply in `thread_root_id`'s thread.
  thread_root_id uuid references messages(id) on delete cascade,
  body        text not null check (length(btrim(body)) > 0),
  mentions    uuid[] not null default '{}',
  edited_at   timestamptz,
  created_at  timestamptz not null default now()
);

create index if not exists messages_channel_idx on messages (channel_id, created_at desc);
create index if not exists messages_thread_idx  on messages (thread_root_id, created_at);

-- ---------------------------------------------------------------- notifications
create table if not exists notifications (
  id           bigserial primary key,
  workspace_id uuid not null references workspaces(id) on delete cascade,
  user_id      uuid not null references profiles(id) on delete cascade,
  actor_id     uuid references profiles(id) on delete set null,
  kind         notification_kind not null,
  title        text not null,
  body         text,
  task_id      uuid references tasks(id) on delete cascade,
  doc_id       uuid references docs(id) on delete cascade,
  message_id   uuid references messages(id) on delete cascade,
  url          text,
  read_at      timestamptz,
  created_at   timestamptz not null default now()
);

create index if not exists notifications_inbox_idx
  on notifications (user_id, created_at desc) where read_at is null;

-- Fan out @mentions on a task comment into the notification inbox.
create or replace function comments_fanout_mentions() returns trigger
language plpgsql as $$
begin
  insert into notifications (workspace_id, user_id, actor_id, kind, title, body, task_id, doc_id)
  select new.workspace_id, m, new.author_id, 'mention',
         'You were mentioned', left(new.body, 280), new.task_id, new.doc_id
    from unnest(new.mentions) as m
   where m <> new.author_id
     and exists (
       select 1 from workspace_members wm
        where wm.workspace_id = new.workspace_id and wm.user_id = m and wm.status = 'active'
     );
  return new;
end $$;

drop trigger if exists comments_fanout_trg on comments;
create trigger comments_fanout_trg after insert on comments
  for each row execute function comments_fanout_mentions();


-- ==========================================================================
-- 5. TIME AND CAPACITY - time entries, availability, workload views
-- ==========================================================================

create table if not exists time_entries (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  task_id      uuid references tasks(id) on delete set null,
  user_id      uuid not null references profiles(id) on delete cascade,
  description  text,
  started_at   timestamptz not null default now(),
  -- NULL = timer still running. See the one-running-timer index below.
  ended_at     timestamptz,
  duration_seconds int generated always as (
    case when ended_at is null then null
         else greatest(0, extract(epoch from (ended_at - started_at))::int) end
  ) stored,
  is_billable  boolean not null default false,
  created_at   timestamptz not null default now(),
  check (ended_at is null or ended_at >= started_at)
);

create index if not exists time_entries_user_idx on time_entries (user_id, started_at desc);
create index if not exists time_entries_task_idx on time_entries (task_id, started_at desc);

-- A person may only have one timer running at a time.
create unique index if not exists time_entries_one_running_idx
  on time_entries (user_id) where ended_at is null;

-- ---------------------------------------------------------------- availability
create table if not exists availability_blocks (
  id           uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  user_id      uuid not null references profiles(id) on delete cascade,
  kind         text not null default 'off' check (kind in ('off','holiday','partial','focus')),
  note         text,
  starts_at    timestamptz not null,
  ends_at      timestamptz not null,
  -- Hours still available per day during a 'partial' block.
  available_hours numeric(4,2),
  created_at   timestamptz not null default now(),
  check (ends_at > starts_at)
);

create index if not exists availability_range_idx
  on availability_blocks using gist (user_id with =, tstzrange(starts_at, ends_at) with &&);

-- ---------------------------------------------------------------- views

-- Open estimated hours per assignee vs. their weekly capacity.
create or replace view workload_summary as
select
  wm.workspace_id,
  wm.user_id,
  p.full_name,
  p.avatar_url,
  wm.weekly_capacity_hours,
  coalesce(sum(t.estimate_hours) filter (where t.state not in ('done','cancelled')), 0) as open_hours,
  count(t.id) filter (where t.state not in ('done','cancelled'))                        as open_tasks,
  count(t.id) filter (where t.due_date < now() and t.state not in ('done','cancelled')) as overdue_tasks,
  round(
    coalesce(sum(t.estimate_hours) filter (where t.state not in ('done','cancelled')), 0)
      / nullif(wm.weekly_capacity_hours, 0) * 100
  , 1) as utilization_pct
from workspace_members wm
join profiles p on p.id = wm.user_id
left join tasks t on t.assignee_id = wm.user_id and t.workspace_id = wm.workspace_id
where wm.status = 'active'
group by wm.workspace_id, wm.user_id, p.full_name, p.avatar_url, wm.weekly_capacity_hours;

-- Board counts per state, for column headers and project health.
create or replace view project_progress as
select
  p.id as project_id,
  p.workspace_id,
  count(t.id)                                        as total_tasks,
  count(t.id) filter (where t.state = 'done')        as done_tasks,
  count(t.id) filter (where t.state = 'in_progress') as in_progress_tasks,
  count(t.id) filter (where t.due_date < now() and t.state not in ('done','cancelled')) as overdue_tasks,
  round(
    count(t.id) filter (where t.state = 'done')::numeric / nullif(count(t.id), 0) * 100
  , 1) as percent_complete
from projects p
left join tasks t on t.project_id = p.id
group by p.id, p.workspace_id;


-- ==========================================================================
-- 6. ROW LEVEL SECURITY
-- ==========================================================================

--
-- The Go API connects with the service role and enforces authorization itself.
-- These policies exist so the Next.js client can subscribe to Supabase Realtime
-- and read its own workspace directly without a second trust boundary.
--
-- Every membership lookup goes through a SECURITY DEFINER function. Querying
-- workspace_members from inside a workspace_members policy would recurse.

create or replace function auth_workspace_ids() returns setof uuid
language sql stable security definer set search_path = public as $$
  select workspace_id from workspace_members
   where user_id = auth.uid() and status = 'active'
$$;

create or replace function is_workspace_member(ws uuid) returns boolean
language sql stable security definer set search_path = public as $$
  select exists (
    select 1 from workspace_members
     where workspace_id = ws and user_id = auth.uid() and status = 'active'
  )
$$;

create or replace function has_workspace_role(ws uuid, roles workspace_role[]) returns boolean
language sql stable security definer set search_path = public as $$
  select exists (
    select 1 from workspace_members
     where workspace_id = ws and user_id = auth.uid()
       and status = 'active' and role = any(roles)
  )
$$;

-- Managers and above: everything except guest/contributor-level access.
create or replace function can_manage_workspace(ws uuid) returns boolean
language sql stable security definer set search_path = public as $$
  select has_workspace_role(ws, array['owner','admin','manager']::workspace_role[])
$$;

do $$
declare t text;
begin
  foreach t in array array[
    'profiles','workspaces','workspace_members','teams','team_members',
    'workspace_join_requests','workspace_invites','projects','tasks','labels',
    'task_labels','task_watchers','task_dependencies','activities','comments',
    'docs','doc_revisions','channels','channel_members','messages',
    'notifications','time_entries','availability_blocks'
  ] loop
    execute format('alter table %I enable row level security', t);
    -- Deliberately NOT "force row level security". FORCE also applies the
    -- policies to the table owner, and the Go API connects as that owner with
    -- no auth.uid() â€” every one of its queries would return zero rows. The API
    -- is its own authorization boundary (see requireWorkspace/requireRole);
    -- these policies exist for the anon and authenticated roles the browser
    -- uses, and ENABLE already covers those.
  end loop;
end $$;

-- Policies are recreated from scratch on every run of this file. CREATE POLICY
-- has no `if not exists` and no `or replace`, so without this the second run
-- would fail on the first policy — and a stale policy left over from an earlier
-- version of the schema would silently keep granting access.
do $$
declare r record;
begin
  for r in
    select tablename, policyname from pg_policies
     where schemaname = 'public'
       and tablename = any (array[
         'profiles','workspaces','workspace_members','teams','team_members',
         'workspace_join_requests','workspace_invites','projects','tasks','labels',
         'task_labels','task_watchers','task_dependencies','activities','comments',
         'docs','doc_revisions','channels','channel_members','messages',
         'notifications','time_entries','availability_blocks'
       ])
  loop
    execute format('drop policy if exists %I on public.%I', r.policyname, r.tablename);
  end loop;
end $$;

-- ---------------------------------------------------------------- profiles
create policy profiles_self_rw on profiles
  for all to authenticated
  using (id = auth.uid()) with check (id = auth.uid());

-- You can see anyone who shares a workspace with you.
create policy profiles_read_coworkers on profiles
  for select to authenticated
  using (exists (
    select 1 from workspace_members wm
     where wm.user_id = profiles.id and wm.workspace_id in (select auth_workspace_ids())
  ));

-- ---------------------------------------------------------------- workspaces
create policy workspaces_read on workspaces
  for select to authenticated using (id in (select auth_workspace_ids()));

create policy workspaces_update on workspaces
  for update to authenticated
  using (has_workspace_role(id, array['owner','admin']::workspace_role[]))
  with check (has_workspace_role(id, array['owner','admin']::workspace_role[]));

create policy workspaces_insert on workspaces
  for insert to authenticated with check (created_by = auth.uid());

create policy workspaces_delete on workspaces
  for delete to authenticated
  using (has_workspace_role(id, array['owner']::workspace_role[]));

-- ---------------------------------------------------------------- membership
create policy members_read on workspace_members
  for select to authenticated using (workspace_id in (select auth_workspace_ids()));

create policy members_write on workspace_members
  for all to authenticated
  using (can_manage_workspace(workspace_id))
  with check (can_manage_workspace(workspace_id));

-- Leaving a workspace is always your own call.
create policy members_leave on workspace_members
  for delete to authenticated using (user_id = auth.uid());

-- ---------------------------------------------------------------- joining
create policy join_requests_own on workspace_join_requests
  for select to authenticated
  using (user_id = auth.uid() or can_manage_workspace(workspace_id));

create policy join_requests_create on workspace_join_requests
  for insert to authenticated with check (user_id = auth.uid());

create policy join_requests_decide on workspace_join_requests
  for update to authenticated
  using (can_manage_workspace(workspace_id))
  with check (can_manage_workspace(workspace_id));

create policy invites_manage on workspace_invites
  for all to authenticated
  using (can_manage_workspace(workspace_id)
         or lower(email) = lower(coalesce(auth.jwt() ->> 'email', '')))
  with check (can_manage_workspace(workspace_id));

-- ---------------------------------------------------------------- workspace-scoped tables
-- Read for any member; write for any non-guest member.
do $$
declare t text;
begin
  foreach t in array array[
    'teams','projects','tasks','labels','task_dependencies','activities',
    'comments','docs','channels'
  ] loop
    execute format('drop policy if exists %1$s_read on %1$I', t);
    execute format('drop policy if exists %1$s_write on %1$I', t);

    execute format($f$
      create policy %1$s_read on %1$I for select to authenticated
        using (workspace_id in (select auth_workspace_ids()))
    $f$, t);

    execute format($f$
      create policy %1$s_write on %1$I for all to authenticated
        using (workspace_id in (select auth_workspace_ids())
               and not has_workspace_role(workspace_id, array['guest']::workspace_role[]))
        with check (workspace_id in (select auth_workspace_ids())
               and not has_workspace_role(workspace_id, array['guest']::workspace_role[]))
    $f$, t);
  end loop;
end $$;

-- ---------------------------------------------------------------- join tables (no workspace_id column)
create policy team_members_access on team_members
  for all to authenticated
  using (exists (select 1 from teams t
                  where t.id = team_members.team_id
                    and t.workspace_id in (select auth_workspace_ids())))
  with check (exists (select 1 from teams t
                  where t.id = team_members.team_id
                    and can_manage_workspace(t.workspace_id)));

do $$
declare t text;
begin
  foreach t in array array['task_labels','task_watchers'] loop
    execute format('drop policy if exists %1$s_access on %1$I', t);
    execute format($f$
      create policy %1$s_access on %1$I for all to authenticated
        using (exists (select 1 from tasks tk
                        where tk.id = %1$I.task_id
                          and tk.workspace_id in (select auth_workspace_ids())))
        with check (exists (select 1 from tasks tk
                        where tk.id = %1$I.task_id
                          and tk.workspace_id in (select auth_workspace_ids())))
    $f$, t);
  end loop;
end $$;

create policy doc_revisions_access on doc_revisions
  for all to authenticated
  using (exists (select 1 from docs d
                  where d.id = doc_revisions.doc_id
                    and d.workspace_id in (select auth_workspace_ids())))
  with check (author_id = auth.uid());

create policy channel_members_access on channel_members
  for all to authenticated
  using (exists (select 1 from channels c
                  where c.id = channel_members.channel_id
                    and c.workspace_id in (select auth_workspace_ids())))
  with check (user_id = auth.uid() or exists (
    select 1 from channels c
     where c.id = channel_members.channel_id and can_manage_workspace(c.workspace_id)));

-- Private channels are visible only to their members.
create policy messages_read on messages
  for select to authenticated
  using (exists (
    select 1 from channels c
     where c.id = messages.channel_id
       and c.workspace_id in (select auth_workspace_ids())
       and (not c.is_private or exists (
            select 1 from channel_members cm
             where cm.channel_id = c.id and cm.user_id = auth.uid()))
  ));

create policy messages_write on messages
  for insert to authenticated
  with check (author_id = auth.uid() and exists (
    select 1 from channels c
     where c.id = messages.channel_id and c.workspace_id in (select auth_workspace_ids())));

create policy messages_edit on messages
  for update to authenticated
  using (author_id = auth.uid()) with check (author_id = auth.uid());

create policy messages_delete on messages
  for delete to authenticated
  using (author_id = auth.uid() or exists (
    select 1 from channels c
     where c.id = messages.channel_id and can_manage_workspace(c.workspace_id)));

-- ---------------------------------------------------------------- personal data
create policy notifications_own on notifications
  for all to authenticated
  using (user_id = auth.uid()) with check (user_id = auth.uid());

-- You manage your own entries; managers can read the team's for reporting.
create policy time_entries_own on time_entries
  for all to authenticated
  using (user_id = auth.uid()) with check (user_id = auth.uid());

create policy time_entries_manager_read on time_entries
  for select to authenticated using (can_manage_workspace(workspace_id));

create policy availability_read on availability_blocks
  for select to authenticated using (workspace_id in (select auth_workspace_ids()));

create policy availability_own on availability_blocks
  for all to authenticated
  using (user_id = auth.uid() or can_manage_workspace(workspace_id))
  with check (user_id = auth.uid() or can_manage_workspace(workspace_id));

-- ---------------------------------------------------------------- realtime
-- Skipped silently on a plain Postgres, where the publication does not exist,
-- and on re-run, where the table is already a member.
do $$
declare t text;
begin
  if not exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    raise notice 'supabase_realtime publication not found â€” skipping realtime setup';
    return;
  end if;

  foreach t in array array['tasks','comments','messages','notifications','activities','profiles'] loop
    if not exists (
      select 1 from pg_publication_tables
       where pubname = 'supabase_realtime' and schemaname = 'public' and tablename = t
    ) then
      execute format('alter publication supabase_realtime add table %I', t);
    end if;
  end loop;
end $$;


-- ==========================================================================
-- 7. RPCs - onboarding, join requests, board moves
-- ==========================================================================

--
-- Workspace creation and joining are multi-table writes that must not half-apply
-- (a workspace with no owner is unrecoverable). They live here as SECURITY
-- DEFINER functions so the Go API and the Next.js client share one implementation
-- and one transaction.

-- Turn "Acme Product Team" into a free "acme-product-team" / "acme-product-team-2".
create or replace function slugify_unique(raw text) returns text
language plpgsql as $$
declare base text; candidate text; n int := 1;
begin
  base := lower(regexp_replace(btrim(raw), '[^a-zA-Z0-9]+', '-', 'g'));
  base := btrim(regexp_replace(base, '-+', '-', 'g'), '-');
  if length(base) < 3 then
    base := 'ws-' || substr(encode(gen_random_bytes(4), 'hex'), 1, 6);
  end if;
  base := left(base, 40);

  candidate := base;
  while exists (select 1 from workspaces where slug = candidate) loop
    n := n + 1;
    candidate := base || '-' || n;
  end loop;
  return candidate;
end $$;

-- ---------------------------------------------------------------- create
create or replace function create_workspace(
  p_user uuid,
  p_name text,
  p_slug text default null,
  p_policy join_policy default 'request'
) returns workspaces
language plpgsql security definer set search_path = public as $$
declare
  ws        workspaces;
  team_id   uuid;
  proj_id   uuid;
  proj_key  text;
begin
  insert into workspaces (name, slug, join_policy, created_by)
  values (btrim(p_name), coalesce(nullif(btrim(p_slug), ''), slugify_unique(p_name)), p_policy, p_user)
  returning * into ws;

  insert into workspace_members (workspace_id, user_id, role, status)
  values (ws.id, p_user, 'owner', 'active');

  insert into teams (workspace_id, name, description)
  values (ws.id, 'General', 'Default team for everyone in the workspace')
  returning id into team_id;

  insert into team_members (team_id, user_id, is_lead) values (team_id, p_user, true);

  -- Seed a starter project so the board is never an empty void on first login.
  proj_key := upper(left(regexp_replace(ws.slug, '[^a-zA-Z]', '', 'g') || 'PRJ', 3));

  insert into projects (workspace_id, team_id, name, key, description, created_by)
  values (ws.id, team_id, 'Getting Started', proj_key,
          'Your first project. Rename it or create your own.', p_user)
  returning id into proj_id;

  insert into tasks (workspace_id, project_id, title, state, priority, reporter_id, assignee_id, board_rank)
  values
    (ws.id, proj_id, 'Invite your teammates',            'todo',        'high',   p_user, p_user, 65536),
    (ws.id, proj_id, 'Create your first real project',   'todo',        'medium', p_user, p_user, 131072),
    (ws.id, proj_id, 'Drag this card to In Progress',    'backlog',     'low',    p_user, null,   65536),
    (ws.id, proj_id, 'Sprintly is set up',               'done',        'none',   p_user, p_user, 65536);

  insert into channels (workspace_id, name, topic, created_by)
  values (ws.id, 'general', 'Workspace-wide announcements', p_user);

  insert into labels (workspace_id, name, color) values
    (ws.id, 'bug', '#ef4444'), (ws.id, 'feature', '#6366f1'),
    (ws.id, 'chore', '#64748b'), (ws.id, 'design', '#ec4899');

  return ws;
end $$;

-- ---------------------------------------------------------------- join
-- Accepts a workspace UUID *or* a join code. Returns the outcome so the client
-- can route to the board ('joined') or a waiting screen ('pending').
do $$ begin
  create type join_result as (
    workspace_id uuid,
    name         text,
    slug         text,
    status       text   -- 'joined' | 'pending' | 'already_member'
  );
exception when duplicate_object then null; end $$;

create or replace function join_workspace(
  p_user      uuid,
  p_reference text,
  p_message   text default null
) returns join_result
language plpgsql security definer set search_path = public as $$
declare
  ws       workspaces;
  existing workspace_members;
  ref      text := btrim(p_reference);
  out_row  join_result;
begin
  -- UUID form first, then the human join code (case-insensitive, dashes ignored).
  begin
    select * into ws from workspaces where id = ref::uuid;
  exception when invalid_text_representation then
    ws := null;
  end;

  if ws.id is null then
    select * into ws from workspaces
     where join_code = upper(replace(ref, '-', ''));
  end if;

  if ws.id is null then
    raise exception 'no workspace matches "%"', ref using errcode = 'no_data_found';
  end if;

  select * into existing from workspace_members
   where workspace_id = ws.id and user_id = p_user;

  if existing.user_id is not null then
    if existing.status = 'active' then
      return (ws.id, ws.name, ws.slug, 'already_member')::join_result;
    end if;
    -- A suspended member cannot re-join themselves.
    raise exception 'membership is %', existing.status using errcode = 'insufficient_privilege';
  end if;

  if ws.join_policy = 'invite_only' then
    raise exception 'workspace "%" is invite only', ws.name using errcode = 'insufficient_privilege';
  end if;

  if ws.join_policy = 'open' then
    insert into workspace_members (workspace_id, user_id, role, status)
    values (ws.id, p_user, 'contributor', 'active');

    insert into activities (workspace_id, actor_id, verb, meta)
    values (ws.id, p_user, 'member_joined', jsonb_build_object('via', 'join_code'));

    out_row := (ws.id, ws.name, ws.slug, 'joined')::join_result;
  else
    insert into workspace_join_requests (workspace_id, user_id, message)
    values (ws.id, p_user, p_message)
    on conflict (workspace_id, user_id) do update
      set status = 'pending', message = excluded.message, created_at = now();

    -- Ping everyone who can approve it.
    insert into notifications (workspace_id, user_id, actor_id, kind, title, body, url)
    select ws.id, wm.user_id, p_user, 'join_request',
           'New request to join ' || ws.name, p_message, '/w/' || ws.slug || '/settings/members'
      from workspace_members wm
     where wm.workspace_id = ws.id
       and wm.status = 'active'
       and wm.role in ('owner','admin','manager');

    out_row := (ws.id, ws.name, ws.slug, 'pending')::join_result;
  end if;

  return out_row;
end $$;

-- ---------------------------------------------------------------- approve / reject
create or replace function decide_join_request(
  p_decider uuid,
  p_request uuid,
  p_approve boolean,
  p_role    workspace_role default 'contributor'
) returns void
language plpgsql security definer set search_path = public as $$
declare req workspace_join_requests; ws workspaces;
begin
  select * into req from workspace_join_requests where id = p_request for update;
  if req.id is null then
    raise exception 'join request not found' using errcode = 'no_data_found';
  end if;
  if req.status <> 'pending' then
    raise exception 'request already %', req.status using errcode = 'check_violation';
  end if;

  if not exists (
    select 1 from workspace_members
     where workspace_id = req.workspace_id and user_id = p_decider
       and status = 'active' and role in ('owner','admin','manager')
  ) then
    raise exception 'not allowed to decide join requests' using errcode = 'insufficient_privilege';
  end if;

  select * into ws from workspaces where id = req.workspace_id;

  update workspace_join_requests
     set status = case when p_approve then 'approved' else 'rejected' end,
         decided_by = p_decider, decided_at = now()
   where id = p_request;

  if p_approve then
    insert into workspace_members (workspace_id, user_id, role, status, invited_by)
    values (req.workspace_id, req.user_id, p_role, 'active', p_decider)
    on conflict (workspace_id, user_id) do update
      set status = 'active', role = excluded.role;
  end if;

  insert into notifications (workspace_id, user_id, actor_id, kind, title, url)
  values (req.workspace_id, req.user_id, p_decider, 'join_request',
          case when p_approve then 'You joined ' || ws.name
               else 'Your request to join ' || ws.name || ' was declined' end,
          case when p_approve then '/w/' || ws.slug else '/onboarding' end);
end $$;

-- ---------------------------------------------------------------- board move
-- Drop a task between two neighbours using fractional ranks; only re-spreads the
-- column when adjacent ranks get too close to split (~50 moves in one gap).
create or replace function move_task(
  p_task   uuid,
  p_state  task_state,
  p_before double precision default null,  -- rank of the card above
  p_after  double precision default null,  -- rank of the card below
  -- The Go API connects as the service role, where auth.uid() is null, so the
  -- actor is passed in explicitly to keep the activity stream attributable.
  p_actor  uuid default null
) returns double precision
language plpgsql security definer set search_path = public as $$
declare
  new_rank double precision;
  t        tasks;
  i        int;
  r        record;
begin
  select * into t from tasks where id = p_task for update;
  if t.id is null then
    raise exception 'task not found' using errcode = 'no_data_found';
  end if;

  if p_before is null and p_after is null then
    select coalesce(max(board_rank), 0) + 65536 into new_rank
      from tasks where project_id = t.project_id and state = p_state;
  elsif p_before is null then
    new_rank := p_after / 2;
  elsif p_after is null then
    new_rank := p_before + 65536;
  else
    new_rank := (p_before + p_after) / 2;
  end if;

  update tasks set state = p_state, board_rank = new_rank where id = p_task;

  if p_before is not null and p_after is not null and abs(p_after - p_before) < 0.0001 then
    i := 0;
    for r in select id from tasks
              where project_id = t.project_id and state = p_state
              order by board_rank, created_at loop
      i := i + 1;
      update tasks set board_rank = i * 65536 where id = r.id;
      if r.id = p_task then new_rank := i * 65536; end if;
    end loop;
  end if;

  if t.state <> p_state then
    insert into activities (workspace_id, task_id, project_id, actor_id, verb, field, old_value, new_value)
    values (t.workspace_id, t.id, t.project_id, coalesce(p_actor, auth.uid()),
            'state_changed', 'state', t.state::text, p_state::text);
  end if;

  return new_rank;
end $$;
