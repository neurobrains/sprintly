"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { Inbox } from "lucide-react";

import { api } from "@/lib/api";
import { PRIORITIES, type Task } from "@/lib/types";
import { cn, dueDateTone, formatDate } from "@/lib/utils";
import { EmptyState, Skeleton } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { TaskDetailSheet } from "@/components/board/task-detail-sheet";

/** "My Work" across every workspace, sorted by urgency. */
export default function InboxPage() {
  const { slug } = useWorkspace();
  const [open, setOpen] = React.useState<Task | null>(null);

  const { data, isLoading } = useQuery({ queryKey: ["my-tasks"], queryFn: api.myTasks });

  const tasks = data?.tasks ?? [];
  const overdue = tasks.filter((t) => t.due_date && new Date(t.due_date) < new Date());
  const rest = tasks.filter((t) => !overdue.includes(t));

  return (
    <>
      <Topbar title="My work" />

      <div className="scrollbar-thin flex-1 overflow-y-auto p-4 lg:p-6">
        {isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full rounded-xl" />
            ))}
          </div>
        ) : tasks.length === 0 ? (
          <EmptyState
            icon={Inbox}
            title="Nothing assigned to you"
            description="Tasks assigned to you across all your workspaces show up here."
          />
        ) : (
          <div className="space-y-6">
            <Section
              title="Overdue"
              tasks={overdue}
              onOpen={setOpen}
              currentSlug={slug}
              tone="text-red-600 dark:text-red-400"
            />
            <Section title="Up next" tasks={rest} onOpen={setOpen} currentSlug={slug} />
          </div>
        )}
      </div>

      {open ? <TaskDetailSheet task={open} onClose={() => setOpen(null)} /> : null}
    </>
  );
}

function Section({
  title,
  tasks,
  onOpen,
  currentSlug,
  tone,
}: {
  title: string;
  tasks: (Task & { workspace_slug: string; workspace_name: string })[];
  onOpen: (t: Task) => void;
  currentSlug: string;
  tone?: string;
}) {
  if (tasks.length === 0) return null;

  return (
    <section>
      <h2 className={cn("mb-2 text-sm font-semibold", tone)}>
        {title} <span className="text-muted-foreground">({tasks.length})</span>
      </h2>

      <ul className="space-y-2">
        {tasks.map((task) => {
          const priority = PRIORITIES.find((p) => p.id === task.priority);
          // The detail sheet reads the current workspace from context, so a task
          // from a different workspace links out instead of opening inline.
          const foreign = task.workspace_slug !== currentSlug;

          const inner = (
            <div className="flex items-center gap-3 rounded-xl border bg-card p-3 transition-shadow hover:shadow-md">
              <span className="shrink-0 font-mono text-xs text-muted-foreground">{task.ref}</span>
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{task.title}</span>

              {foreign ? (
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                  {task.workspace_name}
                </span>
              ) : null}

              {task.priority !== "none" ? (
                <span className={cn("shrink-0 text-xs font-medium", priority?.className)}>
                  {priority?.label}
                </span>
              ) : null}

              {task.due_date ? (
                <span className={cn("shrink-0 text-xs", dueDateTone(task.due_date, false))}>
                  {formatDate(task.due_date)}
                </span>
              ) : null}
            </div>
          );

          return (
            <li key={task.id}>
              {foreign ? (
                <a href={`/w/${task.workspace_slug}?task=${task.id}`}>{inner}</a>
              ) : (
                <button onClick={() => onOpen(task)} className="w-full text-left">
                  {inner}
                </button>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
