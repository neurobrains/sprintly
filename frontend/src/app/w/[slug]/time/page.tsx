"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Clock, Play, Square } from "lucide-react";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import { formatDuration, relativeTime } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardHeader, CardTitle, EmptyState, Skeleton } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";

export default function TimePage() {
  const { slug, canEdit } = useWorkspace();
  const queryClient = useQueryClient();

  const entries = useQuery({
    queryKey: ["time-entries", slug],
    queryFn: () => api.timeEntries(slug),
  });

  const active = useQuery({
    queryKey: ["timer", slug],
    queryFn: () => api.activeTimer(slug),
    refetchInterval: 30_000,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["timer", slug] });
    queryClient.invalidateQueries({ queryKey: ["time-entries", slug] });
  };

  const start = useMutation({
    mutationFn: (description: string) => api.startTimer(slug, { description }),
    onSuccess: invalidate,
    onError: (error) =>
      toast.error("Could not start the timer", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  const stop = useMutation({
    mutationFn: () => api.stopTimer(slug),
    onSuccess: (entry) => {
      invalidate();
      toast.success(`Logged ${formatDuration(entry.duration_seconds ?? 0)}`);
    },
  });

  const running = active.data?.entry ?? null;

  return (
    <>
      <Topbar title="Time" />

      <div className="scrollbar-thin flex-1 space-y-6 overflow-y-auto p-4 lg:p-6">
        {canEdit ? <TimerPanel running={running} onStart={start} onStop={stop} /> : null}

        <Card>
          <CardHeader>
            <CardTitle className="text-base">
              Recent entries
              {entries.data ? (
                <span className="ml-2 text-sm font-normal text-muted-foreground">
                  {formatDuration(entries.data.total_seconds)} total
                </span>
              ) : null}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {entries.isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 5 }).map((_, i) => (
                  <Skeleton key={i} className="h-12 w-full" />
                ))}
              </div>
            ) : !entries.data?.entries.length ? (
              <EmptyState
                icon={Clock}
                title="No time logged yet"
                description="Start a timer above, or log time directly on a task."
              />
            ) : (
              <ul className="divide-y">
                {entries.data.entries.map((e) => (
                  <li key={e.id} className="flex items-center gap-3 py-2.5">
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">
                        {e.task_title ?? e.description ?? "Untitled entry"}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {relativeTime(e.started_at)}
                        {e.ended_at ? "" : " · running"}
                      </p>
                    </div>
                    <span className="shrink-0 font-mono text-sm tabular-nums">
                      {e.duration_seconds === null ? "—" : formatDuration(e.duration_seconds)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}

function TimerPanel({
  running,
  onStart,
  onStop,
}: {
  running: { started_at: string; task_title: string | null; description: string | null } | null;
  onStart: { mutate: (d: string) => void; isPending: boolean };
  onStop: { mutate: () => void; isPending: boolean };
}) {
  const [description, setDescription] = React.useState("");
  const [elapsed, setElapsed] = React.useState(0);

  // Tick locally so the running total moves without polling the server.
  React.useEffect(() => {
    if (!running) {
      setElapsed(0);
      return;
    }
    const started = new Date(running.started_at).getTime();
    const tick = () => setElapsed(Math.floor((Date.now() - started) / 1000));

    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [running]);

  return (
    <Card>
      <CardContent className="flex flex-wrap items-center gap-3 pt-6">
        {running ? (
          <>
            <span className="font-mono text-2xl font-semibold tabular-nums">
              {formatClock(elapsed)}
            </span>
            <span className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
              {running.task_title ?? running.description ?? "Untitled"}
            </span>
            <Button variant="destructive" onClick={() => onStop.mutate()} loading={onStop.isPending}>
              <Square className="h-4 w-4" /> Stop
            </Button>
          </>
        ) : (
          <>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What are you working on?"
              className="max-w-md flex-1"
              onKeyDown={(e) => e.key === "Enter" && onStart.mutate(description)}
            />
            <Button onClick={() => onStart.mutate(description)} loading={onStart.isPending}>
              <Play className="h-4 w-4" /> Start
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function formatClock(seconds: number) {
  const h = String(Math.floor(seconds / 3600)).padStart(2, "0");
  const m = String(Math.floor((seconds % 3600) / 60)).padStart(2, "0");
  const s = String(seconds % 60).padStart(2, "0");
  return `${h}:${m}:${s}`;
}
