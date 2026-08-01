"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import type { TaskPriority, TaskState } from "@/lib/types";
import { PRIORITIES, TASK_STATES } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/misc";
import { useWorkspace } from "@/components/workspace/workspace-provider";

export function NewTaskDialog({
  projectId,
  state,
  parentId,
  onClose,
}: {
  projectId: string;
  state: TaskState;
  parentId?: string;
  onClose: () => void;
}) {
  const { slug, members, me } = useWorkspace();
  const queryClient = useQueryClient();

  const [title, setTitle] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [priority, setPriority] = React.useState<TaskPriority>("none");
  const [assigneeId, setAssigneeId] = React.useState("");
  const [dueDate, setDueDate] = React.useState("");
  const [estimate, setEstimate] = React.useState("");
  const [labelIds, setLabelIds] = React.useState<string[]>([]);

  const { data: labelData } = useQuery({
    queryKey: ["labels", slug],
    queryFn: () => api.labels(slug),
  });

  const create = useMutation({
    mutationFn: () =>
      api.createTask(slug, {
        project_id: projectId,
        title: title.trim(),
        description: description.trim() || undefined,
        state,
        priority,
        assignee_id: assigneeId || undefined,
        parent_id: parentId,
        // <input type="date"> yields YYYY-MM-DD; the API wants a timestamp.
        due_date: dueDate ? new Date(`${dueDate}T17:00:00`).toISOString() : undefined,
        estimate_hours: estimate ? Number(estimate) : undefined,
        label_ids: labelIds.length ? labelIds : undefined,
      }),
    onSuccess: (task) => {
      queryClient.invalidateQueries({ queryKey: ["tasks", slug] });
      queryClient.invalidateQueries({ queryKey: ["projects", slug] });
      toast.success(`${task.ref} created`);
      onClose();
    },
    onError: (error) =>
      toast.error("Could not create the task", {
        description: error instanceof ApiError ? error.message : "Please try again.",
      }),
  });

  const stateLabel = TASK_STATES.find((s) => s.id === state)?.label ?? state;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{parentId ? "New sub-task" : "New task"}</DialogTitle>
          <DialogDescription>It will be added to {stateLabel}.</DialogDescription>
        </DialogHeader>

        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (title.trim()) create.mutate();
          }}
          className="space-y-4"
        >
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What needs to be done?"
            autoFocus
            maxLength={300}
          />

          <Textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Add more detail… (Markdown supported)"
            rows={4}
          />

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Assignee">
              <select
                value={assigneeId}
                onChange={(e) => setAssigneeId(e.target.value)}
                className="h-10 w-full rounded-lg border border-input bg-background px-3 text-sm"
              >
                <option value="">Unassigned</option>
                {me ? <option value={me.id}>Me</option> : null}
                {members
                  .filter((m) => m.user_id !== me?.id && m.status === "active")
                  .map((m) => (
                    <option key={m.user_id} value={m.user_id}>
                      {m.full_name ?? m.email}
                    </option>
                  ))}
              </select>
            </Field>

            <Field label="Priority">
              <select
                value={priority}
                onChange={(e) => setPriority(e.target.value as TaskPriority)}
                className="h-10 w-full rounded-lg border border-input bg-background px-3 text-sm"
              >
                {PRIORITIES.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </select>
            </Field>

            <Field label="Due date">
              <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
            </Field>

            <Field label="Estimate (hours)">
              <Input
                type="number"
                min="0"
                step="0.5"
                value={estimate}
                onChange={(e) => setEstimate(e.target.value)}
                placeholder="e.g. 4"
              />
            </Field>
          </div>

          {labelData?.labels.length ? (
            <Field label="Labels">
              <div className="flex flex-wrap gap-1.5">
                {labelData.labels.map((label) => {
                  const active = labelIds.includes(label.id);
                  return (
                    <button
                      key={label.id}
                      type="button"
                      onClick={() =>
                        setLabelIds((prev) =>
                          active ? prev.filter((id) => id !== label.id) : [...prev, label.id],
                        )
                      }
                      className="rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset transition-opacity"
                      style={{
                        backgroundColor: active ? `${label.color}22` : "transparent",
                        color: active ? label.color : undefined,
                        // @ts-expect-error -- CSS custom property for the ring colour
                        "--tw-ring-color": active ? label.color : "hsl(var(--border))",
                      }}
                    >
                      {label.name}
                    </button>
                  );
                })}
              </div>
            </Field>
          ) : null}

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!title.trim()} loading={create.isPending}>
              Create task
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
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
