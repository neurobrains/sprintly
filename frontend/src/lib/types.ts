/** Mirrors the Go models in `backend/internal/models`. */

export type WorkspaceRole = "owner" | "admin" | "manager" | "contributor" | "guest";
export type JoinPolicy = "open" | "request" | "invite_only";
export type MemberStatus = "active" | "pending" | "suspended";
export type Presence = "online" | "away" | "in_meeting" | "focus" | "offline";
export type TaskState = "backlog" | "todo" | "in_progress" | "review" | "done" | "cancelled";
export type TaskPriority = "none" | "low" | "medium" | "high" | "urgent";

export interface Profile {
  id: string;
  email: string;
  full_name: string | null;
  avatar_url: string | null;
  timezone: string;
  presence: Presence;
  presence_note: string | null;
  last_seen_at: string;
}

export interface Workspace {
  id: string;
  name: string;
  slug: string;
  join_code?: string;
  join_policy: JoinPolicy;
  logo_url: string | null;
  created_by: string;
  created_at: string;
  role?: WorkspaceRole;
  member_count?: number;
}

export interface Member {
  user_id: string;
  email: string;
  full_name: string | null;
  avatar_url: string | null;
  role: WorkspaceRole;
  status: MemberStatus;
  title: string | null;
  weekly_capacity_hours: number;
  presence: Presence;
  joined_at: string;
}

export interface Team {
  id: string;
  workspace_id: string;
  name: string;
  description: string | null;
  color: string;
  member_count: number;
}

export interface Project {
  id: string;
  workspace_id: string;
  team_id: string | null;
  name: string;
  key: string;
  description: string | null;
  color: string;
  icon: string | null;
  start_date: string | null;
  target_date: string | null;
  archived_at: string | null;
  created_at: string;
  total_tasks: number;
  done_tasks: number;
  overdue_tasks: number;
  percent_complete: number | null;
}

export interface Label {
  id: string;
  name: string;
  color: string;
}

export interface Task {
  id: string;
  workspace_id: string;
  project_id: string;
  project_key: string;
  parent_id: string | null;
  number: number;
  /** Display reference, e.g. "SPR-114". */
  ref: string;
  title: string;
  description: string | null;
  state: TaskState;
  priority: TaskPriority;
  board_rank: number;
  assignee_id: string | null;
  assignee?: Profile | null;
  reporter_id: string | null;
  start_date: string | null;
  due_date: string | null;
  estimate_hours: number | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
  labels: Label[];
  subtask_count: number;
  subtask_done: number;
  comment_count: number;
  /** Count of unfinished tasks blocking this one. */
  blocked_by: number;
  logged_seconds: number;
}

export interface Comment {
  id: string;
  task_id: string | null;
  doc_id: string | null;
  parent_id: string | null;
  author: Profile;
  body: string;
  mentions: string[];
  edited_at: string | null;
  created_at: string;
}

export interface Activity {
  id: number;
  task_id: string | null;
  actor: Profile | null;
  verb: string;
  field: string | null;
  old_value: string | null;
  new_value: string | null;
  created_at: string;
}

export interface Dependency {
  id: string;
  source_id: string;
  target_id: string;
  kind: "blocks" | "relates_to" | "duplicates";
  title: string;
  ref: string;
  state: TaskState;
  direction: "incoming" | "outgoing";
}

export interface TimeEntry {
  id: string;
  task_id: string | null;
  task_title: string | null;
  user_id: string;
  description: string | null;
  started_at: string;
  ended_at: string | null;
  duration_seconds: number | null;
  is_billable: boolean;
}

export interface Workload {
  user_id: string;
  full_name: string | null;
  avatar_url: string | null;
  weekly_capacity_hours: number;
  open_hours: number;
  open_tasks: number;
  overdue_tasks: number;
  utilization_pct: number | null;
}

export interface AvailabilityBlock {
  id: string;
  user_id: string;
  kind: "off" | "holiday" | "partial" | "focus";
  note: string | null;
  starts_at: string;
  ends_at: string;
  available_hours: number | null;
}

export interface Notification {
  id: number;
  kind: string;
  title: string;
  body: string | null;
  task_id: string | null;
  url: string | null;
  actor: Profile | null;
  read_at: string | null;
  created_at: string;
}

export interface JoinRequest {
  id: string;
  user_id: string;
  email: string;
  full_name: string | null;
  avatar_url: string | null;
  message: string | null;
  status: "pending" | "approved" | "rejected";
  created_at: string;
}

export interface WorkspacePreview {
  id: string;
  name: string;
  slug: string;
  join_policy: JoinPolicy;
  logo_url: string | null;
  member_count: number;
}

export interface JoinResult {
  workspace_id: string;
  name: string;
  slug: string;
  status: "joined" | "pending" | "already_member";
}

/** Envelope pushed over the workspace WebSocket. */
export interface RealtimeEvent<T = unknown> {
  type: string;
  workspace_id: string;
  actor_id?: string;
  payload?: T;
  at: string;
}

export const TASK_STATES: { id: TaskState; label: string; tint: string }[] = [
  { id: "backlog", label: "Backlog", tint: "bg-slate-400" },
  { id: "todo", label: "Todo", tint: "bg-blue-400" },
  { id: "in_progress", label: "In Progress", tint: "bg-amber-400" },
  { id: "review", label: "Review", tint: "bg-violet-400" },
  { id: "done", label: "Done", tint: "bg-emerald-500" },
];

export const PRIORITIES: { id: TaskPriority; label: string; className: string }[] = [
  { id: "urgent", label: "Urgent", className: "text-red-600 dark:text-red-400" },
  { id: "high", label: "High", className: "text-orange-600 dark:text-orange-400" },
  { id: "medium", label: "Medium", className: "text-amber-600 dark:text-amber-400" },
  { id: "low", label: "Low", className: "text-sky-600 dark:text-sky-400" },
  { id: "none", label: "None", className: "text-muted-foreground" },
];
