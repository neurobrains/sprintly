"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { ListTodo } from "lucide-react";

import { api } from "@/lib/api";
import { PRIORITIES, TASK_STATES, type Task } from "@/lib/types";
import { cn, dueDateTone, formatDate } from "@/lib/utils";
import { Input } from "@/components/ui/input";
import { EmptyState, Skeleton } from "@/components/ui/misc";
import { UserAvatar } from "@/components/ui/avatar";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { TaskDetailSheet } from "@/components/board/task-detail-sheet";

export default function ListPage() {
  const { slug, projects } = useWorkspace();
  const [projectId, setProjectId] = React.useState<string | undefined>();
  const [search, setSearch] = React.useState("");
  const [open, setOpen] = React.useState<Task | null>(null);

  React.useEffect(() => {
    if (!projectId && projects.length > 0) setProjectId(projects[0].id);
  }, [projects, projectId]);

  const { data, isLoading } = useQuery({
    queryKey: ["tasks", slug, { projectId, search, view: "list" }],
    queryFn: () =>
      api.tasks(slug, {
        project_id: projectId,
        search: search.trim() || undefined,
        include_done: true,
      }),
    enabled: Boolean(projectId),
  });

  const tasks = data?.tasks ?? [];

  return (
    <>
      <Topbar title="List" />

      <div className="flex items-center gap-2 border-b px-4 py-2.5 lg:px-6">
        <select
          value={projectId ?? ""}
          onChange={(e) => setProjectId(e.target.value)}
          className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
        >
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search tasks…"
          className="h-9 max-w-xs"
        />
      </div>

      <div className="scrollbar-thin flex-1 overflow-auto">
        {isLoading ? (
          <div className="space-y-2 p-4 lg:p-6">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className="h-11 w-full" />
            ))}
          </div>
        ) : tasks.length === 0 ? (
          <div className="p-6">
            <EmptyState icon={ListTodo} title="No tasks match" />
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="sticky top-0 border-b bg-background/95 backdrop-blur">
              <tr className="text-left text-xs uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-2.5 font-medium lg:px-6">Task</th>
                <th className="px-3 py-2.5 font-medium">Status</th>
                <th className="px-3 py-2.5 font-medium">Priority</th>
                <th className="px-3 py-2.5 font-medium">Assignee</th>
                <th className="px-3 py-2.5 font-medium">Due</th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((task) => {
                const priority = PRIORITIES.find((p) => p.id === task.priority);
                const state = TASK_STATES.find((s) => s.id === task.state);
                const done = task.state === "done";

                return (
                  <tr
                    key={task.id}
                    onClick={() => setOpen(task)}
                    className="cursor-pointer border-b transition-colors last:border-0 hover:bg-accent/50"
                  >
                    <td className="px-4 py-2.5 lg:px-6">
                      <div className="flex items-center gap-2">
                        <span className="shrink-0 font-mono text-xs text-muted-foreground">
                          {task.ref}
                        </span>
                        <span className={cn("truncate", done && "text-muted-foreground line-through")}>
                          {task.title}
                        </span>
                      </div>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2.5">
                      <span className="flex items-center gap-1.5 text-xs">
                        <span className={cn("h-2 w-2 rounded-full", state?.tint ?? "bg-slate-400")} />
                        {state?.label ?? task.state}
                      </span>
                    </td>
                    <td className={cn("px-3 py-2.5 text-xs font-medium", priority?.className)}>
                      {task.priority === "none" ? "—" : priority?.label}
                    </td>
                    <td className="px-3 py-2.5">
                      {task.assignee ? (
                        <UserAvatar
                          size="xs"
                          name={task.assignee.full_name}
                          email={task.assignee.email}
                          src={task.assignee.avatar_url}
                        />
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </td>
                    <td className={cn("whitespace-nowrap px-3 py-2.5 text-xs", dueDateTone(task.due_date, done))}>
                      {task.due_date ? formatDate(task.due_date) : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {open ? <TaskDetailSheet task={open} onClose={() => setOpen(null)} /> : null}
    </>
  );
}
