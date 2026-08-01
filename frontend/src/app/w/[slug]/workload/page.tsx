"use client";

import { useQuery } from "@tanstack/react-query";
import { BarChart3 } from "lucide-react";

import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { UserAvatar } from "@/components/ui/avatar";
import { Badge, EmptyState, Skeleton } from "@/components/ui/misc";

/** Utilisation bands: under-used, healthy, at capacity, over. */
function utilisationTone(pct: number) {
  if (pct > 100) return { bar: "bg-red-500", label: "Over capacity", variant: "danger" as const };
  if (pct >= 85) return { bar: "bg-amber-500", label: "At capacity", variant: "warning" as const };
  if (pct >= 40) return { bar: "bg-emerald-500", label: "Healthy", variant: "success" as const };
  return { bar: "bg-sky-500", label: "Has room", variant: "secondary" as const };
}

export default function WorkloadPage() {
  const { slug } = useWorkspace();

  const { data, isLoading } = useQuery({
    queryKey: ["workload", slug],
    queryFn: () => api.workload(slug),
  });

  return (
    <>
      <Topbar title="Workload" />

      <div className="scrollbar-thin flex-1 overflow-y-auto p-4 lg:p-6">
        <p className="mb-6 max-w-2xl text-sm text-muted-foreground">
          Open estimated hours against each member&apos;s weekly capacity. Anyone over 100% is
          carrying more than they can finish this week.
        </p>

        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-20 w-full rounded-xl" />
            ))}
          </div>
        ) : !data?.workload.length ? (
          <EmptyState
            icon={BarChart3}
            title="No workload data yet"
            description="Add estimates to tasks and assign them to see capacity here."
          />
        ) : (
          <div className="space-y-3">
            {data.workload.map((w) => {
              const pct = w.utilization_pct ?? 0;
              const tone = utilisationTone(pct);
              const away = data.availability.filter((a) => a.user_id === w.user_id);

              return (
                <div key={w.user_id} className="rounded-xl border bg-card p-4">
                  <div className="flex items-center gap-3">
                    <UserAvatar name={w.full_name} src={w.avatar_url} />

                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{w.full_name ?? "Unnamed"}</p>
                      <p className="text-xs text-muted-foreground">
                        {w.open_tasks} open · {w.open_hours}h of {w.weekly_capacity_hours}h
                        {w.overdue_tasks > 0 ? (
                          <span className="ml-2 text-red-600 dark:text-red-400">
                            {w.overdue_tasks} overdue
                          </span>
                        ) : null}
                      </p>
                    </div>

                    <Badge variant={tone.variant}>{tone.label}</Badge>
                    <span className="w-12 text-right text-sm font-semibold tabular-nums">
                      {pct}%
                    </span>
                  </div>

                  <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
                    <div
                      className={cn("h-full rounded-full transition-all", tone.bar)}
                      style={{ width: `${Math.min(pct, 100)}%` }}
                    />
                  </div>

                  {away.length > 0 ? (
                    <p className="mt-2 text-xs text-muted-foreground">
                      Away:{" "}
                      {away
                        .map(
                          (a) =>
                            `${a.note ?? a.kind} (${new Date(a.starts_at).toLocaleDateString()} – ${new Date(a.ends_at).toLocaleDateString()})`,
                        )
                        .join(", ")}
                    </p>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </>
  );
}
