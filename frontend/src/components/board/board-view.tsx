"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  KeyboardSensor,
  closestCorners,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { useDroppable } from "@dnd-kit/core";
import { KanbanSquare, Plus, Search } from "lucide-react";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import type { Task, TaskState } from "@/lib/types";
import { TASK_STATES } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { EmptyState, Skeleton } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { SortableTaskCard, TaskCardContent } from "@/components/board/task-card";
import { TaskDetailSheet } from "@/components/board/task-detail-sheet";
import { NewTaskDialog } from "@/components/board/new-task-dialog";

export function BoardView() {
  const { slug, projects, canEdit } = useWorkspace();
  const queryClient = useQueryClient();

  const [projectId, setProjectId] = React.useState<string | undefined>();
  const [search, setSearch] = React.useState("");
  const [activeTask, setActiveTask] = React.useState<Task | null>(null);
  const [openTask, setOpenTask] = React.useState<Task | null>(null);
  const [composeState, setComposeState] = React.useState<TaskState | null>(null);

  // Default to the first project once they load, without clobbering a manual pick.
  React.useEffect(() => {
    if (!projectId && projects.length > 0) setProjectId(projects[0].id);
  }, [projects, projectId]);

  const filters = React.useMemo(
    () => ({ project_id: projectId, search: search.trim() || undefined, include_done: true }),
    [projectId, search],
  );

  const tasksQuery = useQuery({
    queryKey: ["tasks", slug, filters],
    queryFn: () => api.tasks(slug, filters),
    enabled: Boolean(projectId),
  });

  const tasks = tasksQuery.data?.tasks ?? [];

  const columns = React.useMemo(() => {
    const grouped = new Map<TaskState, Task[]>();
    for (const { id } of TASK_STATES) grouped.set(id, []);

    for (const task of tasks) {
      grouped.get(task.state)?.push(task);
    }
    for (const list of grouped.values()) {
      list.sort((a, b) => a.board_rank - b.board_rank);
    }
    return grouped;
  }, [tasks]);

  const move = useMutation({
    mutationFn: (vars: {
      id: string;
      state: TaskState;
      before_rank: number | null;
      after_rank: number | null;
    }) => api.moveTask(slug, vars.id, vars),

    // Optimistic: the card stays where it was dropped instead of snapping back
    // for the length of a round trip.
    onMutate: async (vars) => {
      await queryClient.cancelQueries({ queryKey: ["tasks", slug, filters] });
      const previous = queryClient.getQueryData<{ tasks: Task[] }>(["tasks", slug, filters]);

      queryClient.setQueryData<{ tasks: Task[] }>(["tasks", slug, filters], (old) => {
        if (!old) return old;
        const rank = midpoint(vars.before_rank, vars.after_rank);
        return {
          tasks: old.tasks.map((t) =>
            t.id === vars.id ? { ...t, state: vars.state, board_rank: rank } : t,
          ),
        };
      });

      return { previous };
    },

    onError: (error, _vars, context) => {
      if (context?.previous) {
        queryClient.setQueryData(["tasks", slug, filters], context.previous);
      }
      toast.error("Could not move the task", {
        description: error instanceof ApiError ? error.message : "Please try again.",
      });
    },

    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["tasks", slug] });
      queryClient.invalidateQueries({ queryKey: ["projects", slug] });
    },
  });

  const sensors = useSensors(
    // A small threshold keeps a click-to-open from registering as a drag.
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  function handleDragStart(event: DragStartEvent) {
    setActiveTask((event.active.data.current?.task as Task) ?? null);
  }

  function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event;
    setActiveTask(null);
    if (!over) return;

    const dragged = active.data.current?.task as Task | undefined;
    if (!dragged) return;

    // `over` is either a column (dropped on empty space) or another card.
    const overTask = over.data.current?.task as Task | undefined;
    const targetState = (overTask?.state ?? (over.id as TaskState)) as TaskState;
    if (!TASK_STATES.some((s) => s.id === targetState)) return;

    const column = (columns.get(targetState) ?? []).filter((t) => t.id !== dragged.id);

    let index = column.length;
    if (overTask) {
      const overIndex = column.findIndex((t) => t.id === overTask.id);
      if (overIndex !== -1) index = overIndex;
    }

    const before = index > 0 ? column[index - 1].board_rank : null;
    const after = index < column.length ? column[index].board_rank : null;

    if (dragged.state === targetState && before === null && after === null) return;

    move.mutate({ id: dragged.id, state: targetState, before_rank: before, after_rank: after });
  }

  const currentProject = projects.find((p) => p.id === projectId);

  return (
    <>
      <Topbar title={currentProject ? currentProject.name : "Board"} />

      <div className="flex items-center gap-2 border-b px-4 py-2.5 lg:px-6">
        <select
          value={projectId ?? ""}
          onChange={(e) => setProjectId(e.target.value)}
          className="h-9 rounded-lg border border-input bg-background px-3 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>

        <div className="relative max-w-xs flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search tasks…"
            className="h-9 pl-9"
          />
        </div>

        {currentProject ? (
          <span className="ml-auto hidden text-xs text-muted-foreground sm:block">
            {currentProject.done_tasks}/{currentProject.total_tasks} done
            {currentProject.overdue_tasks > 0 ? (
              <span className="ml-2 text-red-600 dark:text-red-400">
                {currentProject.overdue_tasks} overdue
              </span>
            ) : null}
          </span>
        ) : null}

        {canEdit && projectId ? (
          <Button size="sm" onClick={() => setComposeState("todo")} className="ml-auto sm:ml-2">
            <Plus className="h-4 w-4" /> New task
          </Button>
        ) : null}
      </div>

      <div className="scrollbar-thin flex-1 overflow-x-auto overflow-y-hidden">
        {projects.length === 0 ? (
          <div className="p-6">
            <EmptyState
              icon={KanbanSquare}
              title="No projects yet"
              description="Create a project to start tracking work on the board."
            />
          </div>
        ) : tasksQuery.isLoading ? (
          <BoardSkeleton />
        ) : (
          <DndContext
            sensors={sensors}
            collisionDetection={closestCorners}
            onDragStart={handleDragStart}
            onDragEnd={handleDragEnd}
            onDragCancel={() => setActiveTask(null)}
          >
            <div className="flex h-full gap-4 p-4 lg:p-6">
              {TASK_STATES.map((state) => (
                <Column
                  key={state.id}
                  state={state}
                  tasks={columns.get(state.id) ?? []}
                  canEdit={canEdit}
                  onOpen={setOpenTask}
                  onCompose={() => setComposeState(state.id)}
                />
              ))}
            </div>

            <DragOverlay dropAnimation={{ duration: 180, easing: "cubic-bezier(0.2,0,0,1)" }}>
              {activeTask ? (
                <div className="w-72">
                  <TaskCardContent task={activeTask} dragging />
                </div>
              ) : null}
            </DragOverlay>
          </DndContext>
        )}
      </div>

      {openTask ? (
        <TaskDetailSheet task={openTask} onClose={() => setOpenTask(null)} />
      ) : null}

      {composeState && projectId ? (
        <NewTaskDialog
          projectId={projectId}
          state={composeState}
          onClose={() => setComposeState(null)}
        />
      ) : null}
    </>
  );
}

function Column({
  state,
  tasks,
  canEdit,
  onOpen,
  onCompose,
}: {
  state: (typeof TASK_STATES)[number];
  tasks: Task[];
  canEdit: boolean;
  onOpen: (task: Task) => void;
  onCompose: () => void;
}) {
  // Droppable on the column itself, so a card can be dropped into empty space.
  const { setNodeRef, isOver } = useDroppable({ id: state.id });

  return (
    <div className="flex h-full w-72 shrink-0 flex-col">
      <div className="mb-3 flex items-center gap-2 px-1">
        <span className={cn("h-2 w-2 rounded-full", state.tint)} />
        <h2 className="text-sm font-semibold">{state.label}</h2>
        <span className="rounded bg-muted px-1.5 text-xs font-medium text-muted-foreground">
          {tasks.length}
        </span>
        {canEdit ? (
          <button
            onClick={onCompose}
            className="ml-auto rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            title={`Add to ${state.label}`}
          >
            <Plus className="h-4 w-4" />
          </button>
        ) : null}
      </div>

      <div
        ref={setNodeRef}
        className={cn(
          "scrollbar-thin flex-1 space-y-2 overflow-y-auto rounded-xl border border-dashed p-2 transition-colors",
          isOver ? "border-primary bg-primary/5" : "border-transparent bg-muted/40",
        )}
      >
        <SortableContext items={tasks.map((t) => t.id)} strategy={verticalListSortingStrategy}>
          {tasks.map((task) => (
            <SortableTaskCard key={task.id} task={task} onOpen={onOpen} disabled={!canEdit} />
          ))}
        </SortableContext>

        {tasks.length === 0 ? (
          <p className="px-2 py-8 text-center text-xs text-muted-foreground">Nothing here</p>
        ) : null}
      </div>
    </div>
  );
}

function BoardSkeleton() {
  return (
    <div className="flex gap-4 p-4 lg:p-6">
      {TASK_STATES.map((s) => (
        <div key={s.id} className="w-72 shrink-0 space-y-2">
          <Skeleton className="h-5 w-24" />
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-24 w-full rounded-lg" />
          ))}
        </div>
      ))}
    </div>
  );
}

function midpoint(before: number | null, after: number | null) {
  if (before === null && after === null) return 65536;
  if (before === null) return after! / 2;
  if (after === null) return before + 65536;
  return (before + after) / 2;
}
