"use client";

import { useQuery } from "@tanstack/react-query";
import { Activity as ActivityIcon } from "lucide-react";

import { api } from "@/lib/api";
import { TASK_STATES } from "@/lib/types";
import { relativeTime } from "@/lib/utils";
import { UserAvatar } from "@/components/ui/avatar";
import { EmptyState, Skeleton } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";

export default function ActivityPage() {
  const { slug } = useWorkspace();

  const { data, isLoading } = useQuery({
    queryKey: ["activity", slug],
    queryFn: () => api.workspaceActivity(slug),
    refetchInterval: 60_000,
  });

  return (
    <>
      <Topbar title="Activity" />

      <div className="scrollbar-thin flex-1 overflow-y-auto p-4 lg:p-6">
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 10 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : !data?.activity.length ? (
          <EmptyState
            icon={ActivityIcon}
            title="No activity yet"
            description="Every change to a task shows up here as it happens."
          />
        ) : (
          <ol className="relative space-y-4 border-l pl-6">
            {data.activity.map((a) => (
              <li key={a.id} className="relative">
                <span className="absolute -left-[31px] top-1 flex h-2.5 w-2.5 items-center justify-center rounded-full bg-primary ring-4 ring-background" />
                <div className="flex items-start gap-2.5">
                  <UserAvatar
                    size="xs"
                    name={a.actor?.full_name}
                    email={a.actor?.email}
                    src={a.actor?.avatar_url}
                    className="mt-0.5"
                  />
                  <p className="text-sm text-muted-foreground">
                    <span className="font-medium text-foreground">
                      {a.actor?.full_name ?? a.actor?.email ?? "Someone"}
                    </span>{" "}
                    {describe(a.verb, a.field, a.old_value, a.new_value)}
                    <span className="ml-2 text-xs">{relativeTime(a.created_at)}</span>
                  </p>
                </div>
              </li>
            ))}
          </ol>
        )}
      </div>
    </>
  );
}

function describe(verb: string, field: string | null, oldValue: string | null, newValue: string | null) {
  const label = (s: string | null) => TASK_STATES.find((t) => t.id === s)?.label ?? s ?? "unknown";

  switch (verb) {
    case "created":
      return "created a task";
    case "state_changed":
      return `moved a task from ${label(oldValue)} to ${label(newValue)}`;
    case "assigned":
      return newValue ? "reassigned a task" : "unassigned a task";
    case "member_joined":
      return "joined the workspace";
    default:
      return `updated ${field ?? "a task"}`;
  }
}
