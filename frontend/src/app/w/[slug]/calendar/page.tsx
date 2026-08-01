"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { api } from "@/lib/api";
import { PRIORITIES, type Task } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { TaskDetailSheet } from "@/components/board/task-detail-sheet";

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

export default function CalendarPage() {
  const { slug } = useWorkspace();
  const [month, setMonth] = React.useState(() => startOfMonth(new Date()));
  const [open, setOpen] = React.useState<Task | null>(null);

  // Fetch the whole visible grid, which spills into the neighbouring months.
  const gridStart = startOfWeek(month);
  const gridEnd = new Date(gridStart);
  gridEnd.setDate(gridEnd.getDate() + 42);

  const { data, isLoading } = useQuery({
    queryKey: ["tasks", slug, { view: "calendar", month: month.toISOString() }],
    queryFn: () =>
      api.tasks(slug, {
        due_after: gridStart.toISOString(),
        due_before: gridEnd.toISOString(),
        include_done: true,
        limit: 1000,
      }),
  });

  const byDay = React.useMemo(() => {
    const map = new Map<string, Task[]>();
    for (const task of data?.tasks ?? []) {
      if (!task.due_date) continue;
      const key = dayKey(new Date(task.due_date));
      map.set(key, [...(map.get(key) ?? []), task]);
    }
    return map;
  }, [data]);

  const days = React.useMemo(
    () =>
      Array.from({ length: 42 }, (_, i) => {
        const d = new Date(gridStart);
        d.setDate(d.getDate() + i);
        return d;
      }),
    [gridStart],
  );

  const todayKey = dayKey(new Date());

  return (
    <>
      <Topbar title="Calendar" />

      <div className="flex items-center gap-2 border-b px-4 py-2.5 lg:px-6">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setMonth(addMonths(month, -1))}
          aria-label="Previous month"
        >
          <ChevronLeft className="h-4 w-4" />
        </Button>
        <span className="min-w-40 text-center text-sm font-semibold">
          {month.toLocaleDateString(undefined, { month: "long", year: "numeric" })}
        </span>
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setMonth(addMonths(month, 1))}
          aria-label="Next month"
        >
          <ChevronRight className="h-4 w-4" />
        </Button>
        <Button variant="outline" size="sm" onClick={() => setMonth(startOfMonth(new Date()))}>
          Today
        </Button>
      </div>

      <div className="scrollbar-thin flex-1 overflow-auto p-4 lg:p-6">
        {isLoading ? (
          <Skeleton className="h-[640px] w-full rounded-xl" />
        ) : (
          <div className="overflow-hidden rounded-xl border">
            <div className="grid grid-cols-7 border-b bg-muted/40">
              {WEEKDAYS.map((d) => (
                <div
                  key={d}
                  className="px-2 py-2 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground"
                >
                  {d}
                </div>
              ))}
            </div>

            <div className="grid grid-cols-7">
              {days.map((day) => {
                const key = dayKey(day);
                const tasks = byDay.get(key) ?? [];
                const outside = day.getMonth() !== month.getMonth();

                return (
                  <div
                    key={key}
                    className={cn(
                      "min-h-28 border-b border-r p-1.5 last:border-r-0",
                      outside && "bg-muted/30",
                      key === todayKey && "bg-primary/5",
                    )}
                  >
                    <span
                      className={cn(
                        "inline-flex h-6 w-6 items-center justify-center rounded-full text-xs",
                        key === todayKey
                          ? "bg-primary font-semibold text-primary-foreground"
                          : outside
                            ? "text-muted-foreground/60"
                            : "text-muted-foreground",
                      )}
                    >
                      {day.getDate()}
                    </span>

                    <div className="mt-1 space-y-1">
                      {tasks.slice(0, 3).map((task) => {
                        const priority = PRIORITIES.find((p) => p.id === task.priority);
                        return (
                          <button
                            key={task.id}
                            onClick={() => setOpen(task)}
                            className={cn(
                              "block w-full truncate rounded px-1.5 py-0.5 text-left text-[11px] transition-colors hover:bg-accent",
                              task.state === "done"
                                ? "text-muted-foreground line-through"
                                : priority?.className,
                            )}
                            title={task.title}
                          >
                            {task.title}
                          </button>
                        );
                      })}
                      {tasks.length > 3 ? (
                        <span className="px-1.5 text-[10px] text-muted-foreground">
                          +{tasks.length - 3} more
                        </span>
                      ) : null}
                    </div>
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

function startOfMonth(d: Date) {
  return new Date(d.getFullYear(), d.getMonth(), 1);
}

/** Weeks start on Monday. */
function startOfWeek(d: Date) {
  const out = new Date(d);
  const day = (out.getDay() + 6) % 7;
  out.setDate(out.getDate() - day);
  out.setHours(0, 0, 0, 0);
  return out;
}

function addMonths(d: Date, delta: number) {
  return new Date(d.getFullYear(), d.getMonth() + delta, 1);
}

function dayKey(d: Date) {
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}
