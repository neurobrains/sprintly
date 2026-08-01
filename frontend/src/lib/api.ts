"use client";

import { supabase } from "@/lib/supabase/client";
import type {
  Activity,
  Comment,
  Dependency,
  JoinRequest,
  JoinResult,
  Label,
  Member,
  Notification,
  Profile,
  Project,
  Task,
  TaskState,
  Team,
  TimeEntry,
  Workload,
  AvailabilityBlock,
  Workspace,
  WorkspacePreview,
} from "@/lib/types";

const BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

/** Error shape the Go API returns (`httpx.Error`). */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const {
    data: { session },
  } = await supabase().auth.getSession();

  if (!session) throw new ApiError(401, "unauthorized", "Your session expired. Sign in again.");

  const res = await fetch(`${BASE}/api/v1${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${session.access_token}`,
      ...init.headers,
    },
  });

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const body = text ? JSON.parse(text) : {};

  if (!res.ok) {
    throw new ApiError(
      res.status,
      body.code ?? "error",
      body.message ?? `Request failed with status ${res.status}`,
    );
  }
  return body as T;
}

const get = <T,>(path: string) => request<T>(path);
const post = <T,>(path: string, body?: unknown) =>
  request<T>(path, { method: "POST", body: JSON.stringify(body ?? {}) });
const patch = <T,>(path: string, body: unknown) =>
  request<T>(path, { method: "PATCH", body: JSON.stringify(body) });
const del = <T,>(path: string) => request<T>(path, { method: "DELETE" });

const qs = (params: Record<string, string | number | boolean | undefined | null>) => {
  const search = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") search.set(k, String(v));
  }
  const s = search.toString();
  return s ? `?${s}` : "";
};

/** Workspace-scoped routes take the slug or the UUID interchangeably. */
const ws = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}`;

export const api = {
  // ------------------------------------------------------------- account
  me: () => get<{ profile: Profile; provider: string }>("/me"),

  updateMe: (body: Partial<Pick<Profile, "full_name" | "timezone" | "presence" | "presence_note">>) =>
    patch<Profile>("/me", body),

  myWorkspaces: () =>
    get<{
      workspaces: Workspace[];
      pending_requests: { name: string; slug: string; created_at: string }[];
    }>("/me/workspaces"),

  myTasks: () =>
    get<{ tasks: (Task & { workspace_slug: string; workspace_name: string })[] }>("/me/tasks"),

  // ------------------------------------------------------------- onboarding
  createWorkspace: (body: { name: string; slug?: string; join_policy?: string }) =>
    post<Workspace>("/workspaces", body),

  /** `reference` is a workspace UUID or its short join code. */
  joinWorkspace: (body: { reference: string; message?: string }) =>
    post<JoinResult>("/workspaces/join", body),

  lookupWorkspace: (reference: string) =>
    get<WorkspacePreview>(`/workspaces/lookup${qs({ reference })}`),

  // ------------------------------------------------------------- workspace
  workspace: (w: string) => get<Workspace>(`${ws(w)}/`),

  updateWorkspace: (w: string, body: Partial<Pick<Workspace, "name" | "join_policy" | "logo_url">>) =>
    patch<Workspace>(`${ws(w)}/`, body),

  rotateJoinCode: (w: string) => post<{ join_code: string }>(`${ws(w)}/rotate-join-code`),

  members: (w: string) => get<{ members: Member[]; online: string[] }>(`${ws(w)}/members`),

  updateMember: (
    w: string,
    userId: string,
    body: Partial<Pick<Member, "role" | "title" | "weekly_capacity_hours" | "status">>,
  ) => patch<Member>(`${ws(w)}/members/${userId}`, body),

  removeMember: (w: string, userId: string) => del<void>(`${ws(w)}/members/${userId}`),

  joinRequests: (w: string) => get<{ requests: JoinRequest[] }>(`${ws(w)}/join-requests`),

  decideJoinRequest: (w: string, id: string, approve: boolean, role = "contributor") =>
    post<{ approved: boolean }>(`${ws(w)}/join-requests/${id}`, { approve, role }),

  // ------------------------------------------------------------- teams & labels
  teams: (w: string) => get<{ teams: Team[] }>(`${ws(w)}/teams`),
  createTeam: (w: string, body: { name: string; description?: string; color?: string }) =>
    post<Team>(`${ws(w)}/teams`, body),

  labels: (w: string) => get<{ labels: Label[] }>(`${ws(w)}/labels`),
  createLabel: (w: string, body: { name: string; color?: string }) =>
    post<Label>(`${ws(w)}/labels`, body),

  // ------------------------------------------------------------- projects
  projects: (w: string, archived = false) =>
    get<{ projects: Project[] }>(`${ws(w)}/projects${qs({ archived })}`),

  project: (w: string, id: string) => get<Project>(`${ws(w)}/projects/${id}`),

  createProject: (
    w: string,
    body: { name: string; key?: string; description?: string; color?: string; team_id?: string },
  ) => post<Project>(`${ws(w)}/projects`, body),

  updateProject: (w: string, id: string, body: Record<string, unknown>) =>
    patch<Project>(`${ws(w)}/projects/${id}`, body),

  // ------------------------------------------------------------- tasks
  tasks: (
    w: string,
    filters: {
      project_id?: string;
      assignee_id?: string;
      state?: string;
      priority?: string;
      search?: string;
      parent_id?: string;
      due_before?: string;
      due_after?: string;
      include_done?: boolean;
      limit?: number;
    } = {},
  ) => get<{ tasks: Task[] }>(`${ws(w)}/tasks${qs(filters)}`),

  task: (w: string, id: string) => get<Task>(`${ws(w)}/tasks/${id}`),

  createTask: (
    w: string,
    body: {
      project_id: string;
      title: string;
      description?: string;
      state?: TaskState;
      priority?: string;
      assignee_id?: string;
      parent_id?: string;
      due_date?: string;
      estimate_hours?: number;
      label_ids?: string[];
    },
  ) => post<Task>(`${ws(w)}/tasks`, body),

  updateTask: (w: string, id: string, body: Record<string, unknown>) =>
    patch<Task>(`${ws(w)}/tasks/${id}`, body),

  /**
   * Commit a drag. The client sends the ranks of the cards the task landed
   * between; the server picks the midpoint, so two people dragging at once
   * cannot corrupt the ordering.
   */
  moveTask: (
    w: string,
    id: string,
    body: { state: TaskState; before_rank: number | null; after_rank: number | null },
  ) => post<Task>(`${ws(w)}/tasks/${id}/move`, body),

  deleteTask: (w: string, id: string) => del<void>(`${ws(w)}/tasks/${id}`),

  // ------------------------------------------------------------- collaboration
  comments: (w: string, taskId: string) =>
    get<{ comments: Comment[] }>(`${ws(w)}/tasks/${taskId}/comments`),

  createComment: (w: string, taskId: string, body: { body: string; parent_id?: string }) =>
    post<Comment>(`${ws(w)}/tasks/${taskId}/comments`, body),

  taskActivity: (w: string, taskId: string) =>
    get<{ activity: Activity[] }>(`${ws(w)}/tasks/${taskId}/activity`),

  workspaceActivity: (w: string, projectId?: string) =>
    get<{ activity: Activity[] }>(`${ws(w)}/activity${qs({ project_id: projectId })}`),

  dependencies: (w: string, taskId: string) =>
    get<{ dependencies: Dependency[] }>(`${ws(w)}/tasks/${taskId}/dependencies`),

  createDependency: (
    w: string,
    taskId: string,
    body: { other_id: string; direction: "incoming" | "outgoing"; kind?: string },
  ) => post<Dependency>(`${ws(w)}/tasks/${taskId}/dependencies`, body),

  deleteDependency: (w: string, depId: string) => del<void>(`${ws(w)}/dependencies/${depId}`),

  // ------------------------------------------------------------- time & workload
  timeEntries: (w: string, params: { user_id?: string; from?: string; to?: string } = {}) =>
    get<{ entries: TimeEntry[]; total_seconds: number }>(`${ws(w)}/time-entries${qs(params)}`),

  logTime: (
    w: string,
    body: {
      task_id?: string;
      description?: string;
      duration_minutes?: number;
      started_at?: string;
      ended_at?: string;
      is_billable?: boolean;
    },
  ) => post<TimeEntry>(`${ws(w)}/time-entries`, body),

  startTimer: (w: string, body: { task_id?: string; description?: string }) =>
    post<TimeEntry>(`${ws(w)}/timer/start`, body),

  stopTimer: (w: string) => post<TimeEntry>(`${ws(w)}/timer/stop`),

  activeTimer: (w: string) => get<{ entry: TimeEntry | null }>(`${ws(w)}/timer/active`),

  workload: (w: string) =>
    get<{ workload: Workload[]; availability: AvailabilityBlock[] }>(`${ws(w)}/workload`),

  // ------------------------------------------------------------- notifications
  notifications: (w: string, unreadOnly = false) =>
    get<{ notifications: Notification[]; unread_count: number }>(
      `${ws(w)}/notifications${qs({ unread: unreadOnly })}`,
    ),

  markNotificationsRead: (w: string, ids?: number[]) =>
    post<{ marked: number }>(`${ws(w)}/notifications/read`, { ids: ids ?? [] }),
};

/** WebSocket URL for a workspace's live event stream. */
export function eventsUrl(workspace: string, accessToken: string) {
  const url = new URL(`${BASE}/api/v1${ws(workspace)}/events`);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("access_token", accessToken);
  return url.toString();
}
