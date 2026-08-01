"use client";

import * as React from "react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { CalendarDays, CheckSquare, Lock, MessageSquare, Timer } from "lucide-react";

import type { Task } from "@/lib/types";
import { PRIORITIES } from "@/lib/types";
import { cn, dueDateTone, formatDate, formatDuration } from "@/lib/utils";
import { UserAvatar } from "@/components/ui/avatar";

/** Presentational card — shared by the sortable card and the drag overlay. */
export function TaskCardContent({ task, dragging }: { task: Task; dragging?: boolean }) {
  const priority = PRIORITIES.find((p) => p.id === task.priority);
  const done = task.state === "done";

  return (
    <div
      className={cn(
        "group rounded-lg border bg-card p-3 shadow-sm transition-shadow",
        dragging ? "rotate-2 shadow-xl ring-2 ring-primary" : "hover:shadow-md",
      )}
    >
      {task.labels.length > 0 ? (
        <div className="mb-2 flex flex-wrap gap-1">
          {task.labels.map((label) => (
            <span
              key={label.id}
              className="rounded px-1.5 py-0.5 text-[10px] font-medium"
              style={{ backgroundColor: `${label.color}22`, color: label.color }}
            >
              {label.name}
            </span>
          ))}
        </div>
      ) : null}

      <p className={cn("text-sm font-medium leading-snug", done && "text-muted-foreground line-through")}>
        {task.title}
      </p>

      <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11px] text-muted-foreground">
        <span className="font-mono">{task.ref}</span>

        {task.priority !== "none" ? (
          <span className={cn("font-medium", priority?.className)}>{priority?.label}</span>
        ) : null}

        {/* A blocked card is the single most useful thing to see on a board. */}
        {task.blocked_by > 0 && !done ? (
          <span className="flex items-center gap-1 font-medium text-red-600 dark:text-red-400">
            <Lock className="h-3 w-3" />
            Blocked
          </span>
        ) : null}

        {task.due_date ? (
          <span className={cn("flex items-center gap-1", dueDateTone(task.due_date, done))}>
            <CalendarDays className="h-3 w-3" />
            {formatDate(task.due_date)}
          </span>
        ) : null}

        {task.subtask_count > 0 ? (
          <span className="flex items-center gap-1">
            <CheckSquare className="h-3 w-3" />
            {task.subtask_done}/{task.subtask_count}
          </span>
        ) : null}

        {task.comment_count > 0 ? (
          <span className="flex items-center gap-1">
            <MessageSquare className="h-3 w-3" />
            {task.comment_count}
          </span>
        ) : null}

        {task.logged_seconds > 0 ? (
          <span className="flex items-center gap-1">
            <Timer className="h-3 w-3" />
            {formatDuration(task.logged_seconds)}
          </span>
        ) : null}

        <span className="ml-auto">
          {task.assignee ? (
            <UserAvatar
              size="xs"
              name={task.assignee.full_name}
              email={task.assignee.email}
              src={task.assignee.avatar_url}
            />
          ) : (
            <span className="block h-5 w-5 rounded-full border border-dashed" />
          )}
        </span>
      </div>
    </div>
  );
}

export function SortableTaskCard({
  task,
  onOpen,
  disabled,
}: {
  task: Task;
  onOpen: (task: Task) => void;
  disabled?: boolean;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: task.id,
    data: { type: "task", task },
    disabled,
  });

  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Translate.toString(transform), transition }}
      {...attributes}
      {...listeners}
      onClick={() => onOpen(task)}
      // The original stays mounted while dragging so the list keeps its height;
      // hiding it avoids showing the card twice alongside the overlay.
      className={cn("cursor-grab active:cursor-grabbing", isDragging && "opacity-0")}
    >
      <TaskCardContent task={task} />
    </div>
  );
}
