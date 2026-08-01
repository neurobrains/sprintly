"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { GanttChartSquare } from "lucide-react";

import { api } from "@/lib/api";
import { TASK_STATES, type Task } from "@/lib/types";
import { cn } from "@/lib/utils";
import { EmptyState, Skeleton } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { TaskDetailSheet } from "@/components/board/task-detail-sheet";

const DAY_WIDTH = 32;
const DAY_MS = 86_400_000;

/**
 * Gantt-style timeline. Each task spans start_date → due_date; tasks with only
 * a due date get a three-day lead-in so they are still visible as a bar.
 */
export default function TimelinePage() {
  const { slug, projects } = useWorkspace();
  const [projectId, setProjectId] = React.useState<string | undefined>();
  const [open, setOpen] = React.useState<Task | null>(null);

  React.useEffect(() => {
    if (!projectId && projects.length > 0) setProjectId(projects[0].id);
  }, [projects, projectId]);

  const { data, isLoading } = useQuery({
    queryKey: ["tasks", slug, { view: "timeline", projectId }],
    queryFn: () => api.tasks(slug, { project_id: projectId, include_done: true, limit: 500 }),
    enabled: Boolean(projectId),
  });

  const scheduled = React.useMemo(
    () => (data?.tasks ?? []).filter((t) => t.due_date),
    [data],
  );

  const { origin, totalDays } = React.useMemo(() => {
    if (scheduled.length === 0) {
      const today = midnight(new Date());
      return { origin: today, totalDays: 30 };
    }

    let min = Infinity;
    let max = -Infinity;

    for (const t of scheduled) {
      const end = midnight(new Date(t.due_date!)).getTime();
      const start = t.start_date ? midnight(new Date(t.start_date)).getTime() : end - 3 * DAY_MS;
      min = Math.min(min, start);
      max = Math.max(max, end);
    }

    // A few days of padding on each side keeps bars off the edges.
    const originDate = new Date(min - 2 * DAY_MS);
    return {
      origin: originDate,
      totalDays: Math.ceil((max - originDate.getTime()) / DAY_MS) + 3,
    };
  }, [scheduled]);

  const days = React.useMemo(
    () =>
      Array.from({ length: totalDays }, (_, i) => new Date(origin.getTime() + i * DAY_MS)),
    [origin, totalDays],
  );

  const todayOffset = Math.floor((midnight(new Date()).getTime() - origin.getTime()) / DAY_MS);

  return (
    <>
      <Topbar title="Timeline" />

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
      </div>

      <div className="scrollbar-thin flex-1 overflow-auto">
        {isLoading ? (
          <div className="space-y-2 p-4 lg:p-6">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className="h-9 w-full" />
            ))}
          </div>
        ) : scheduled.length === 0 ? (
          <div className="p-6">
            <EmptyState
              icon={GanttChartSquare}
              title="Nothing scheduled"
              description="Give tasks a due date and they'll appear on the timeline."
            />
          </div>
        ) : (
          <div className="flex min-w-max">
            {/* Sticky task-name gutter so rows stay identifiable while scrolling. */}
            <div className="sticky left-0 z-10 w-64 shrink-0 border-r bg-background">
              <div className="h-10 border-b" />
              {scheduled.map((t) => (
                <div
                  key={t.id}
                  className="flex h-9 items-center gap-2 border-b px-3 text-xs"
                  title={t.title}
                >
                  <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                    {t.ref}
                  </span>
                  <span className="truncate">{t.title}</span>
                </div>
              ))}
            </div>

            <div className="relative">
              <div className="flex h-10 border-b">
                {days.map((d, i) => (
                  <div
                    key={i}
                    style={{ width: DAY_WIDTH }}
                    className={cn(
                      "shrink-0 border-r text-center text-[10px] leading-tight text-muted-foreground",
                      isWeekend(d) && "bg-muted/40",
                    )}
                  >
                    <div className="pt-1">{d.getDate()}</div>
                    <div>{d.toLocaleDateString(undefined, { weekday: "narrow" })}</div>
                  </div>
                ))}
              </div>

              {todayOffset >= 0 && todayOffset < totalDays ? (
                <div
                  className="pointer-events-none absolute bottom-0 top-0 w-px bg-primary/60"
                  style={{ left: todayOffset * DAY_WIDTH + DAY_WIDTH / 2 }}
                />
              ) : null}

              {scheduled.map((task) => {
                const end = midnight(new Date(task.due_date!)).getTime();
                const start = task.start_date
                  ? midnight(new Date(task.start_date)).getTime()
                  : end - 3 * DAY_MS;

                const offset = Math.floor((start - origin.getTime()) / DAY_MS);
                const span = Math.max(1, Math.round((end - start) / DAY_MS) + 1);
                const state = TASK_STATES.find((s) => s.id === task.state);
                const overdue = end < Date.now() && task.state !== "done";

                return (
                  <div key={task.id} className="relative h-9 border-b">
                    {days.map((d, i) => (
                      <div
                        key={i}
                        style={{ width: DAY_WIDTH, left: i * DAY_WIDTH }}
                        className={cn(
                          "absolute bottom-0 top-0 border-r",
                          isWeekend(d) && "bg-muted/40",
                        )}
                      />
                    ))}

                    <button
                      onClick={() => setOpen(task)}
                      style={{ left: offset * DAY_WIDTH + 2, width: span * DAY_WIDTH - 4 }}
                      className={cn(
                        "absolute top-1.5 h-6 truncate rounded px-2 text-left text-[10px] font-medium text-white shadow-sm transition-opacity hover:opacity-90",
                        overdue ? "bg-red-500" : (state?.tint ?? "bg-slate-400"),
                        task.state === "done" && "opacity-60",
                      )}
                      title={`${task.ref} · ${task.title}`}
                    >
                      {task.title}
                    </button>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>

      {open ? <TaskDetailSheet task={open} onClose={() => setOpen(null)} /> : null}
    </>
  );
}

function midnight(d: Date) {
  const out = new Date(d);
  out.setHours(0, 0, 0, 0);
  return out;
}

function isWeekend(d: Date) {
  return d.getDay() === 0 || d.getDay() === 6;
}
