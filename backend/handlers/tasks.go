package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/httpx"
	"github.com/sprintly/sprintly/backend/models"
	"github.com/sprintly/sprintly/backend/realtime"
)

// Task reads go through the task_list / task_detail RPCs rather than PostgREST
// selects.
//
// A board card carries five aggregates (sub-task counts, comment count, blocking
// count, logged seconds) and its label set. In SQL those are correlated
// sub-queries over covered indexes — one round trip for the whole board. Over
// PostgREST they would be one request per card per aggregate, so a 200-card
// board becomes a thousand HTTP calls. The RPCs return rows whose column names
// match models.Task's JSON tags, so they decode straight through.

func taskRef(key string, number int) string { return key + "-" + strconv.Itoa(number) }

// finishTask fills the derived fields the RPC does not compute.
func finishTask(t *models.Task) {
	t.Ref = taskRef(t.ProjectKey, t.Number)
	if t.Labels == nil {
		t.Labels = []models.Label{}
	}
}

// ---------------------------------------------------------------- list

// handleListTasks powers every view (board, list, calendar, Gantt). Unset
// filters are passed as null and cost nothing in the RPC's WHERE clause.
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	q := r.URL.Query()
	args := map[string]any{
		"p_workspace":    wsID,
		"p_project":      nil,
		"p_assignee":     nil,
		"p_unassigned":   false,
		"p_states":       nil,
		"p_priorities":   nil,
		"p_search":       nil,
		"p_parent":       nil,
		"p_top_level":    false,
		"p_due_before":   nil,
		"p_due_after":    nil,
		"p_include_done": q.Get("include_done") == "true",
		"p_limit":        httpx.QueryInt(r, "limit", 500, 1000),
	}

	if v := q.Get("project_id"); v != "" {
		id, err := httpx.UUIDParam(v, "project_id")
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		args["p_project"] = id
	}
	if v := q.Get("assignee_id"); v != "" {
		if v == "unassigned" {
			args["p_unassigned"] = true
		} else {
			id, err := httpx.UUIDParam(v, "assignee_id")
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
			args["p_assignee"] = id
		}
	}
	if v := q.Get("state"); v != "" {
		states := strings.Split(v, ",")
		for _, st := range states {
			if !validTaskState(st) {
				httpx.Fail(w, r, httpx.BadRequest("unknown state %q", st))
				return
			}
		}
		args["p_states"] = states
	}
	if v := q.Get("priority"); v != "" {
		priorities := strings.Split(v, ",")
		for _, p := range priorities {
			if !validPriority(p) {
				httpx.Fail(w, r, httpx.BadRequest("unknown priority %q", p))
				return
			}
		}
		args["p_priorities"] = priorities
	}
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		args["p_search"] = v
	}
	if v := q.Get("parent_id"); v != "" {
		if v == "none" {
			args["p_top_level"] = true
		} else {
			id, err := httpx.UUIDParam(v, "parent_id")
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
			args["p_parent"] = id
		}
	}
	if v := q.Get("due_before"); v != "" {
		args["p_due_before"] = v
	}
	if v := q.Get("due_after"); v != "" {
		args["p_due_after"] = v
	}

	out := []models.Task{}
	if err := s.data.RPC(r.Context(), "task_list", args, &out); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	for i := range out {
		finishTask(&out[i])
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	taskID, err := httpx.UUIDParam(chi.URLParam(r, "taskID"), "taskID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	t, err := s.loadTask(r.Context(), wsID, taskID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (s *Server) loadTask(ctx context.Context, wsID, taskID uuid.UUID) (models.Task, error) {
	var t models.Task
	err := s.data.RPCSingle(ctx, "task_detail", map[string]any{
		"p_workspace": wsID,
		"p_task":      taskID,
	}, &t)
	if err != nil {
		return t, err
	}
	finishTask(&t)
	return t, nil
}

// ---------------------------------------------------------------- create

type createTaskRequest struct {
	ProjectID     string      `json:"project_id"`
	ParentID      *string     `json:"parent_id"`
	Title         string      `json:"title"`
	Description   *string     `json:"description"`
	State         string      `json:"state,omitempty"`
	Priority      string      `json:"priority,omitempty"`
	AssigneeID    *string     `json:"assignee_id"`
	StartDate     *string     `json:"start_date"`
	DueDate       *string     `json:"due_date"`
	EstimateHours *float64    `json:"estimate_hours"`
	LabelIDs      []uuid.UUID `json:"label_ids,omitempty"`
}

// handleCreateTask delegates the write to an RPC: the row, its labels and the
// "created" activity are one transaction, and the new card's board_rank is
// derived from the current minimum in its column, which has to be read and
// written atomically or two simultaneous creates collide.
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req createTaskRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		httpx.Fail(w, r, httpx.BadRequest("Task title is required"))
		return
	}
	projectID, err := httpx.UUIDParam(req.ProjectID, "project_id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.State == "" {
		req.State = "backlog"
	}
	if !validTaskState(req.State) {
		httpx.Fail(w, r, httpx.BadRequest("unknown state %q", req.State))
		return
	}
	if req.Priority == "" {
		req.Priority = "none"
	}
	if !validPriority(req.Priority) {
		httpx.Fail(w, r, httpx.BadRequest("unknown priority %q", req.Priority))
		return
	}

	labels := make([]string, 0, len(req.LabelIDs))
	for _, id := range req.LabelIDs {
		labels = append(labels, id.String())
	}

	var taskID uuid.UUID
	err = s.data.RPC(r.Context(), "create_task", map[string]any{
		"p_workspace": wsID,
		"p_project":   projectID,
		"p_parent":    nilIfEmpty(derefString(req.ParentID)),
		"p_title":     req.Title,
		"p_desc":      req.Description,
		"p_state":     req.State,
		"p_priority":  req.Priority,
		"p_assignee":  nilIfEmpty(derefString(req.AssigneeID)),
		"p_reporter":  userID,
		"p_start":     nilIfEmpty(derefString(req.StartDate)),
		"p_due":       nilIfEmpty(derefString(req.DueDate)),
		"p_estimate":  req.EstimateHours,
		"p_labels":    labels,
	}, &taskID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	t, err := s.loadTask(r.Context(), wsID, taskID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	s.notifyAssignment(r.Context(), wsID, userID, t, nil)
	s.hub.Publish(realtime.Event{Type: "task.created", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(t)})
	httpx.JSON(w, http.StatusCreated, t)
}

// ---------------------------------------------------------------- update

type updateTaskRequest struct {
	Title         *string      `json:"title"`
	Description   *string      `json:"description"`
	State         *string      `json:"state"`
	Priority      *string      `json:"priority"`
	AssigneeID    *string      `json:"assignee_id"` // "" clears the assignee
	ParentID      *string      `json:"parent_id"`
	StartDate     *string      `json:"start_date"`
	DueDate       *string      `json:"due_date"`
	EstimateHours *float64     `json:"estimate_hours"`
	LabelIDs      *[]uuid.UUID `json:"label_ids"`
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	taskID, err := httpx.UUIDParam(chi.URLParam(r, "taskID"), "taskID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req updateTaskRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.State != nil && !validTaskState(*req.State) {
		httpx.Fail(w, r, httpx.BadRequest("unknown state %q", *req.State))
		return
	}
	if req.Priority != nil && !validPriority(*req.Priority) {
		httpx.Fail(w, r, httpx.BadRequest("unknown priority %q", *req.Priority))
		return
	}

	before, err := s.loadTask(r.Context(), wsID, taskID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	patch := map[string]any{}
	if req.Title != nil {
		patch["title"] = *req.Title
	}
	if req.Description != nil {
		patch["description"] = nilIfEmpty(*req.Description)
	}
	if req.State != nil {
		patch["state"] = *req.State
	}
	if req.Priority != nil {
		patch["priority"] = *req.Priority
	}
	if req.AssigneeID != nil {
		patch["assignee_id"] = nilIfEmpty(strings.TrimSpace(*req.AssigneeID))
	}
	if req.ParentID != nil {
		patch["parent_id"] = nilIfEmpty(strings.TrimSpace(*req.ParentID))
	}
	if req.StartDate != nil {
		patch["start_date"] = nilIfEmpty(strings.TrimSpace(*req.StartDate))
	}
	if req.DueDate != nil {
		patch["due_date"] = nilIfEmpty(strings.TrimSpace(*req.DueDate))
	}
	if req.EstimateHours != nil {
		patch["estimate_hours"] = *req.EstimateHours
	}

	if len(patch) > 0 {
		if err := s.data.From("tasks").
			Eq("id", taskID).
			Eq("workspace_id", wsID).
			Update(r.Context(), patch, nil); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}

	// A present label_ids array replaces the whole set; absent leaves it alone.
	// The RPC does the delete and the insert together and filters the ids down to
	// labels that belong to this workspace.
	if req.LabelIDs != nil {
		ids := make([]string, 0, len(*req.LabelIDs))
		for _, id := range *req.LabelIDs {
			ids = append(ids, id.String())
		}
		if err := s.data.RPC(r.Context(), "set_task_labels", map[string]any{
			"p_task":      taskID,
			"p_workspace": wsID,
			"p_labels":    ids,
		}, nil); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}

	if err := s.recordChanges(r.Context(), wsID, userID, before, req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	after, err := s.loadTask(r.Context(), wsID, taskID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	s.notifyAssignment(r.Context(), wsID, userID, after, before.AssigneeID)
	s.hub.Publish(realtime.Event{Type: "task.updated", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(after)})
	httpx.JSON(w, http.StatusOK, after)
}

// recordChanges writes one activity row per changed field, which is what the
// task's activity stream renders. All the rows go in one insert.
func (s *Server) recordChanges(ctx context.Context, wsID, actorID uuid.UUID,
	before models.Task, req updateTaskRequest) error {

	type change struct{ field, old, new string }
	var changes []change

	if req.State != nil && *req.State != before.State {
		changes = append(changes, change{"state", before.State, *req.State})
	}
	if req.Priority != nil && *req.Priority != before.Priority {
		changes = append(changes, change{"priority", before.Priority, *req.Priority})
	}
	if req.Title != nil && *req.Title != before.Title {
		changes = append(changes, change{"title", before.Title, *req.Title})
	}
	if req.AssigneeID != nil {
		old := ""
		if before.AssigneeID != nil {
			old = before.AssigneeID.String()
		}
		if *req.AssigneeID != old {
			changes = append(changes, change{"assignee", old, *req.AssigneeID})
		}
	}
	if req.DueDate != nil {
		old := ""
		if before.DueDate != nil {
			old = before.DueDate.Format("2006-01-02")
		}
		if *req.DueDate != old {
			changes = append(changes, change{"due_date", old, *req.DueDate})
		}
	}
	if len(changes) == 0 {
		return nil
	}

	rows := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		verb := "updated"
		switch c.field {
		case "state":
			verb = "state_changed"
		case "assignee":
			verb = "assigned"
		}
		rows = append(rows, map[string]any{
			"workspace_id": wsID,
			"task_id":      before.ID,
			"project_id":   before.ProjectID,
			"actor_id":     actorID,
			"verb":         verb,
			"field":        c.field,
			"old_value":    nilIfEmpty(c.old),
			"new_value":    nilIfEmpty(c.new),
		})
	}
	return s.data.From("activities").Insert(ctx, rows, nil)
}

// ---------------------------------------------------------------- move (drag & drop)

type moveTaskRequest struct {
	State string `json:"state"`
	// Ranks of the neighbours the card was dropped between; null means the edge
	// of the column. The client sends what it can see, the server picks the rank.
	BeforeRank *float64 `json:"before_rank"`
	AfterRank  *float64 `json:"after_rank"`
}

func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	taskID, err := httpx.UUIDParam(chi.URLParam(r, "taskID"), "taskID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req moveTaskRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if !validTaskState(req.State) {
		httpx.Fail(w, r, httpx.BadRequest("unknown state %q", req.State))
		return
	}

	// Confirm the task is in this workspace before the RPC, which takes a bare
	// task id and is not workspace-scoped on its own.
	if err := s.assertTaskInWorkspace(r.Context(), taskID, wsID); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var rank float64
	if err := s.data.RPC(r.Context(), "move_task", map[string]any{
		"p_task":   taskID,
		"p_state":  req.State,
		"p_before": req.BeforeRank,
		"p_after":  req.AfterRank,
		"p_actor":  userID,
	}, &rank); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	t, err := s.loadTask(r.Context(), wsID, taskID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	s.hub.Publish(realtime.Event{Type: "task.moved", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(t)})
	httpx.JSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	taskID, err := httpx.UUIDParam(chi.URLParam(r, "taskID"), "taskID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var deleted []struct {
		ID uuid.UUID `json:"id"`
	}
	err = s.data.From("tasks").
		Select("id").
		Eq("id", taskID).
		Eq("workspace_id", wsID).
		Delete(r.Context(), &deleted)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(deleted) == 0 {
		httpx.Fail(w, r, httpx.ErrNotFound)
		return
	}

	s.hub.Publish(realtime.Event{Type: "task.deleted", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(map[string]any{"id": taskID})})
	httpx.JSON(w, http.StatusNoContent, nil)
}

// notifyAssignment tells someone they picked up work — but never notifies you
// about your own action.
func (s *Server) notifyAssignment(ctx context.Context, wsID, actorID uuid.UUID,
	t models.Task, previous *uuid.UUID) {

	if t.AssigneeID == nil || *t.AssigneeID == actorID {
		return
	}
	if previous != nil && *previous == *t.AssigneeID {
		return
	}

	// Through an RPC, not a plain insert: this writes a row owned by *another*
	// user, which the notifications_own policy forbids whenever the API is
	// running under RLS rather than as the service role.
	//
	// A missed notification must never fail the write that caused it.
	_ = s.data.RPC(ctx, "notify_assignment", map[string]any{
		"p_workspace": wsID,
		"p_user":      *t.AssigneeID,
		"p_actor":     actorID,
		"p_title":     "You were assigned " + t.Ref,
		"p_body":      t.Title,
		"p_task":      t.ID,
	}, nil)

	s.hub.PublishTo(wsID, *t.AssigneeID, realtime.Event{
		Type:    "notification.new",
		ActorID: actorID,
		Payload: realtime.Marshal(map[string]any{
			"kind": "assignment", "task_id": t.ID, "ref": t.Ref, "title": t.Title,
		}),
	})
}

func validTaskState(v string) bool {
	switch v {
	case "backlog", "todo", "in_progress", "review", "done", "cancelled":
		return true
	}
	return false
}

func validPriority(v string) bool {
	switch v {
	case "none", "low", "medium", "high", "urgent":
		return true
	}
	return false
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
