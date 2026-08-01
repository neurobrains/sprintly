"use client";

import * as React from "react";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { TASK_STATES, type Task } from "@/lib/types";
import { cn, formatDate } from "@/lib/utils";
import { Card, CardContent, Skeleton } from "@/components/ui/misc";
import { UserAvatar } from "@/components/ui/avatar";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { TaskDetailSheet } from "@/components/board/task-detail-sheet";

export default function ProjectPage() {
  const { slug } = useWorkspace();
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const [open, setOpen] = React.useState<Task | null>(null);

  const project = useQuery({
    queryKey: ["project", slug, projectId],
    queryFn: () => api.project(slug, projectId),
  });

  const tasks = useQuery({
    queryKey: ["tasks", slug, { projectId, view: "project" }],
    queryFn: () => api.tasks(slug, { project_id: projectId, include_done: true }),
  });

  const byState = React.useMemo(() => {
    const map = new Map<string, Task[]>();
    for (const t of tasks.data?.tasks ?? []) {
      map.set(t.state, [...(map.get(t.state) ?? []), t]);
    }
    return map;
  }, [tasks.data]);

  const p = project.data;

  return (
    <>
      <Topbar title={p?.name ?? "Project"} />

      <div className="scrollbar-thin flex-1 overflow-y-auto p-4 lg:p-6">
        {project.isLoading ? (
          <Skeleton className="h-32 w-full rounded-xl" />
        ) : p ? (
          <Card className="mb-6">
            <CardContent className="pt-6">
              <div className="flex flex-wrap items-start gap-4">
                <span
                  className="mt-1 h-10 w-10 shrink-0 rounded-lg"
                  style={{ backgroundColor: p.color }}
                />
                <div className="min-w-0 flex-1">
                  <h2 className="flex items-center gap-2 text-lg font-semibold">
                    {p.name}
                    <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
                      {p.key}
                    </span>
                  </h2>
                  {p.description ? (
                    <p className="mt-1 text-sm text-muted-foreground">{p.description}</p>
                  ) : null}
                  {p.target_date ? (
                    <p className="mt-2 text-xs text-muted-foreground">
                      Target: {formatDate(p.target_date)}
                    </p>
                  ) : null}
                </div>

                <div className="text-right">
                  <p className="text-2xl font-semibold tabular-nums">
                    {p.percent_complete ?? 0}%
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {p.done_tasks}/{p.total_tasks} done
                  </p>
                </div>
              </div>

              <div className="mt-4 h-2 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-emerald-500 transition-all"
                  style={{ width: `${p.percent_complete ?? 0}%` }}
                />
              </div>

              {p.overdue_tasks > 0 ? (
                <p className="mt-2 text-xs text-red-600 dark:text-red-400">
                  {p.overdue_tasks} task{p.overdue_tasks === 1 ? "" : "s"} overdue
                </p>
              ) : null}
            </CardContent>
          </Card>
        ) : null}

        {tasks.isLoading ? (
          <Skeleton className="h-64 w-full rounded-xl" />
        ) : (
          <div className="space-y-6">
            {TASK_STATES.map((state) => {
              const list = byState.get(state.id) ?? [];
              if (list.length === 0) return null;

              return (
                <section key={state.id}>
                  <h3 className="mb-2 flex items-center gap-2 text-sm font-semibold">
                    <span className={cn("h-2 w-2 rounded-full", state.tint)} />
                    {state.label}
                    <span className="text-muted-foreground">({list.length})</span>
                  </h3>

                  <ul className="divide-y rounded-xl border">
                    {list.map((task) => (
                      <li key={task.id}>
                        <button
                          onClick={() => setOpen(task)}
                          className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-accent/50"
                        >
                          <span className="shrink-0 font-mono text-xs text-muted-foreground">
                            {task.ref}
                          </span>
                          <span
                            className={cn(
                              "min-w-0 flex-1 truncate text-sm",
                              task.state === "done" && "text-muted-foreground line-through",
                            )}
                          >
                            {task.title}
                          </span>
                          {task.assignee ? (
                            <UserAvatar
                              size="xs"
                              name={task.assignee.full_name}
                              email={task.assignee.email}
                              src={task.assignee.avatar_url}
                            />
                          ) : null}
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              );
            })}
          </div>
        )}
      </div>

      {open ? <TaskDetailSheet task={open} onClose={() => setOpen(null)} /> : null}
    </>
  );
}
