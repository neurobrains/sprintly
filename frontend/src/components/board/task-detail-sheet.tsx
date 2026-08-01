"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Lock, Play, Send, Square, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import type { Task, TaskPriority, TaskState } from "@/lib/types";
import { PRIORITIES, TASK_STATES } from "@/lib/types";
import { cn, formatDuration, relativeTime } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Badge, Skeleton } from "@/components/ui/misc";
import { UserAvatar } from "@/components/ui/avatar";
import { useWorkspace } from "@/components/workspace/workspace-provider";

/**
 * Right-hand task panel: edit fields, discuss, see the activity trail and the
 * dependency tree. Field edits save on blur/change rather than behind a Save
 * button — the board is a live surface, and a modal save step loses changes.
 */
export function TaskDetailSheet({ task, onClose }: { task: Task; onClose: () => void }) {
  const { slug, members, me, canEdit } = useWorkspace();
  const queryClient = useQueryClient();
  const [tab, setTab] = React.useState<"comments" | "activity" | "links">("comments");

  const detail = useQuery({
    queryKey: ["task", slug, task.id],
    queryFn: () => api.task(slug, task.id),
    initialData: task,
  });

  const current = detail.data ?? task;

  const update = useMutation({
    mutationFn: (patch: Record<string, unknown>) => api.updateTask(slug, task.id, patch),
    onSuccess: (updated) => {
      queryClient.setQueryData(["task", slug, task.id], updated);
      queryClient.invalidateQueries({ queryKey: ["tasks", slug] });
    },
    onError: (error) =>
      toast.error("Could not save", {
        description: error instanceof ApiError ? error.message : "Please try again.",
      }),
  });

  const remove = useMutation({
    mutationFn: () => api.deleteTask(slug, task.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tasks", slug] });
      toast.success(`${current.ref} deleted`);
      onClose();
    },
  });

  // Escape closes the panel — expected of anything that behaves like a dialog.
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-40 flex justify-end">
      <div className="flex-1 bg-black/40 backdrop-blur-sm" onClick={onClose} />

      <div className="scrollbar-thin flex w-full max-w-xl flex-col overflow-y-auto border-l bg-background shadow-2xl">
        <div className="sticky top-0 z-10 flex items-center gap-3 border-b bg-background/95 px-5 py-3 backdrop-blur">
          <span className="font-mono text-sm text-muted-foreground">{current.ref}</span>
          {current.blocked_by > 0 && current.state !== "done" ? (
            <Badge variant="danger">
              <Lock className="h-3 w-3" /> Blocked by {current.blocked_by}
            </Badge>
          ) : null}
          <div className="ml-auto flex items-center gap-1">
            <TimerButton taskId={current.id} />
            {canEdit ? (
              <Button
                variant="ghost"
                size="icon"
                onClick={() => {
                  if (confirm(`Delete ${current.ref}? This cannot be undone.`)) remove.mutate();
                }}
                title="Delete task"
              >
                <Trash2 className="h-4 w-4 text-destructive" />
              </Button>
            ) : null}
            <Button variant="ghost" size="icon" onClick={onClose}>
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>

        <div className="space-y-6 px-5 py-5">
          <input
            defaultValue={current.title}
            disabled={!canEdit}
            onBlur={(e) => {
              const value = e.target.value.trim();
              if (value && value !== current.title) update.mutate({ title: value });
            }}
            className="w-full bg-transparent text-xl font-semibold leading-snug outline-none focus:ring-0 disabled:opacity-70"
          />

          <div className="grid grid-cols-2 gap-4">
            <Field label="Status">
              <select
                value={current.state}
                disabled={!canEdit}
                onChange={(e) => update.mutate({ state: e.target.value as TaskState })}
                className="h-9 w-full rounded-lg border border-input bg-background px-2 text-sm"
              >
                {TASK_STATES.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.label}
                  </option>
                ))}
                <option value="cancelled">Cancelled</option>
              </select>
            </Field>

            <Field label="Priority">
              <select
                value={current.priority}
                disabled={!canEdit}
                onChange={(e) => update.mutate({ priority: e.target.value as TaskPriority })}
                className="h-9 w-full rounded-lg border border-input bg-background px-2 text-sm"
              >
                {PRIORITIES.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </select>
            </Field>

            <Field label="Assignee">
              <select
                value={current.assignee_id ?? ""}
                disabled={!canEdit}
                onChange={(e) => update.mutate({ assignee_id: e.target.value })}
                className="h-9 w-full rounded-lg border border-input bg-background px-2 text-sm"
              >
                <option value="">Unassigned</option>
                {members
                  .filter((m) => m.status === "active")
                  .map((m) => (
                    <option key={m.user_id} value={m.user_id}>
                      {m.user_id === me?.id ? "Me" : (m.full_name ?? m.email)}
                    </option>
                  ))}
              </select>
            </Field>

            <Field label="Due date">
              <Input
                type="date"
                disabled={!canEdit}
                defaultValue={current.due_date ? current.due_date.slice(0, 10) : ""}
                onChange={(e) =>
                  update.mutate({
                    due_date: e.target.value
                      ? new Date(`${e.target.value}T17:00:00`).toISOString()
                      : "",
                  })
                }
                className="h-9"
              />
            </Field>
          </div>

          <Field label="Description">
            <Textarea
              defaultValue={current.description ?? ""}
              disabled={!canEdit}
              rows={5}
              placeholder="Add context, acceptance criteria, links…"
              onBlur={(e) => {
                if (e.target.value !== (current.description ?? "")) {
                  update.mutate({ description: e.target.value });
                }
              }}
            />
          </Field>

          {current.logged_seconds > 0 ? (
            <p className="text-xs text-muted-foreground">
              {formatDuration(current.logged_seconds)} logged
              {current.estimate_hours ? ` of ${current.estimate_hours}h estimated` : ""}
            </p>
          ) : null}

          <div>
            <div className="flex gap-1 border-b">
              {(["comments", "activity", "links"] as const).map((t) => (
                <button
                  key={t}
                  onClick={() => setTab(t)}
                  className={cn(
                    "-mb-px border-b-2 px-3 py-2 text-sm font-medium capitalize transition-colors",
                    tab === t
                      ? "border-primary text-foreground"
                      : "border-transparent text-muted-foreground hover:text-foreground",
                  )}
                >
                  {t}
                </button>
              ))}
            </div>

            <div className="pt-4">
              {tab === "comments" ? <Comments taskId={current.id} /> : null}
              {tab === "activity" ? <ActivityTrail taskId={current.id} /> : null}
              {tab === "links" ? <Dependencies taskId={current.id} /> : null}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}

function TimerButton({ taskId }: { taskId: string }) {
  const { slug, canEdit } = useWorkspace();
  const queryClient = useQueryClient();

  const { data } = useQuery({
    queryKey: ["timer", slug],
    queryFn: () => api.activeTimer(slug),
    refetchInterval: 30_000,
  });

  const running = data?.entry;
  const onThisTask = running?.task_id === taskId;

  const start = useMutation({
    mutationFn: () => api.startTimer(slug, { task_id: taskId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["timer", slug] });
      toast.success("Timer started");
    },
    onError: (error) =>
      toast.error("Could not start the timer", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  const stop = useMutation({
    mutationFn: () => api.stopTimer(slug),
    onSuccess: (entry) => {
      queryClient.invalidateQueries({ queryKey: ["timer", slug] });
      queryClient.invalidateQueries({ queryKey: ["tasks", slug] });
      queryClient.invalidateQueries({ queryKey: ["task", slug, taskId] });
      toast.success(`Logged ${formatDuration(entry.duration_seconds ?? 0)}`);
    },
  });

  if (!canEdit) return null;

  return onThisTask ? (
    <Button variant="ghost" size="icon" onClick={() => stop.mutate()} title="Stop timer">
      <Square className="h-4 w-4 fill-red-500 text-red-500" />
    </Button>
  ) : (
    <Button
      variant="ghost"
      size="icon"
      onClick={() => start.mutate()}
      // Starting a second timer would violate the one-running-timer constraint.
      disabled={Boolean(running)}
      title={running ? "Stop your other timer first" : "Start timer"}
    >
      <Play className="h-4 w-4" />
    </Button>
  );
}

function Comments({ taskId }: { taskId: string }) {
  const { slug, canEdit } = useWorkspace();
  const queryClient = useQueryClient();
  const [body, setBody] = React.useState("");

  const { data, isLoading } = useQuery({
    queryKey: ["comments", slug, taskId],
    queryFn: () => api.comments(slug, taskId),
  });

  const post = useMutation({
    mutationFn: () => api.createComment(slug, taskId, { body: body.trim() }),
    onSuccess: () => {
      setBody("");
      queryClient.invalidateQueries({ queryKey: ["comments", slug, taskId] });
      queryClient.invalidateQueries({ queryKey: ["tasks", slug] });
    },
    onError: (error) =>
      toast.error("Could not post the comment", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  if (isLoading) return <Skeleton className="h-24 w-full" />;

  return (
    <div className="space-y-4">
      {data?.comments.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted-foreground">No comments yet.</p>
      ) : (
        data?.comments.map((c) => (
          <div key={c.id} className="flex gap-3">
            <UserAvatar
              size="sm"
              name={c.author.full_name}
              email={c.author.email}
              src={c.author.avatar_url}
            />
            <div className="min-w-0 flex-1">
              <p className="text-sm">
                <span className="font-medium">{c.author.full_name ?? c.author.email}</span>{" "}
                <span className="text-xs text-muted-foreground">{relativeTime(c.created_at)}</span>
              </p>
              <p className="mt-1 whitespace-pre-wrap break-words text-sm text-muted-foreground">
                {c.body}
              </p>
            </div>
          </div>
        ))
      )}

      {canEdit ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (body.trim()) post.mutate();
          }}
          className="flex gap-2"
        >
          <Textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Write a comment…  Use @[Name](user-id) to mention someone."
            rows={2}
            className="min-h-0"
            onKeyDown={(e) => {
              // Cmd/Ctrl+Enter submits; plain Enter keeps its newline.
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && body.trim()) {
                e.preventDefault();
                post.mutate();
              }
            }}
          />
          <Button type="submit" size="icon" disabled={!body.trim()} loading={post.isPending}>
            <Send className="h-4 w-4" />
          </Button>
        </form>
      ) : null}
    </div>
  );
}

function ActivityTrail({ taskId }: { taskId: string }) {
  const { slug } = useWorkspace();
  const { data, isLoading } = useQuery({
    queryKey: ["activity", slug, taskId],
    queryFn: () => api.taskActivity(slug, taskId),
  });

  if (isLoading) return <Skeleton className="h-24 w-full" />;
  if (!data?.activity.length)
    return <p className="py-4 text-center text-sm text-muted-foreground">No activity yet.</p>;

  return (
    <ol className="space-y-3">
      {data.activity.map((a) => (
        <li key={a.id} className="flex gap-3 text-sm">
          <UserAvatar
            size="xs"
            name={a.actor?.full_name}
            email={a.actor?.email}
            src={a.actor?.avatar_url}
            className="mt-0.5"
          />
          <p className="text-muted-foreground">
            <span className="font-medium text-foreground">
              {a.actor?.full_name ?? a.actor?.email ?? "Someone"}
            </span>{" "}
            {describeActivity(a.verb, a.field, a.old_value, a.new_value)}
            <span className="ml-1.5 text-xs">{relativeTime(a.created_at)}</span>
          </p>
        </li>
      ))}
    </ol>
  );
}

function describeActivity(
  verb: string,
  field: string | null,
  oldValue: string | null,
  newValue: string | null,
) {
  switch (verb) {
    case "created":
      return "created this task";
    case "state_changed":
      return `moved it from ${label(oldValue)} to ${label(newValue)}`;
    case "assigned":
      return newValue ? "changed the assignee" : "unassigned it";
    default:
      return `updated ${field ?? "the task"}`;
  }
}

function label(state: string | null) {
  return TASK_STATES.find((s) => s.id === state)?.label ?? state ?? "unknown";
}

function Dependencies({ taskId }: { taskId: string }) {
  const { slug } = useWorkspace();
  const { data, isLoading } = useQuery({
    queryKey: ["dependencies", slug, taskId],
    queryFn: () => api.dependencies(slug, taskId),
  });

  if (isLoading) return <Skeleton className="h-16 w-full" />;

  const incoming = data?.dependencies.filter((d) => d.direction === "incoming") ?? [];
  const outgoing = data?.dependencies.filter((d) => d.direction === "outgoing") ?? [];

  if (incoming.length === 0 && outgoing.length === 0) {
    return <p className="py-4 text-center text-sm text-muted-foreground">No linked tasks.</p>;
  }

  return (
    <div className="space-y-4">
      <DepList title="Blocked by" items={incoming} />
      <DepList title="Blocks" items={outgoing} />
    </div>
  );
}

function DepList({
  title,
  items,
}: {
  title: string;
  items: { id: string; ref: string; title: string; state: string }[];
}) {
  if (items.length === 0) return null;

  return (
    <div>
      <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {title}
      </p>
      <ul className="space-y-1.5">
        {items.map((d) => (
          <li key={d.id} className="flex items-center gap-2 rounded-lg border px-3 py-2 text-sm">
            <span className="font-mono text-xs text-muted-foreground">{d.ref}</span>
            <span className={cn("truncate", d.state === "done" && "text-muted-foreground line-through")}>
              {d.title}
            </span>
            {d.state === "done" ? (
              <Badge variant="success" className="ml-auto shrink-0">
                Done
              </Badge>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}
