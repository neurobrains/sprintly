package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

// taskSelect returns everything a board card needs in one round trip. The
// correlated counts are cheap because each is a covered index lookup, and it
// avoids the N+1 that would otherwise fire once per card.
const taskSelect = `
	select t.id, t.workspace_id, t.project_id, pr.key, t.parent_id, t.number, t.title,
	       t.description, t.state, t.priority, t.board_rank,
	       t.assignee_id, t.reporter_id, t.start_date, t.due_date, t.estimate_hours,
	       t.completed_at, t.created_at, t.updated_at,
	       a.id, a.email, a.full_name, a.avatar_url, a.presence,
	       (select count(*) from tasks c where c.parent_id = t.id),
	       (select count(*) from tasks c where c.parent_id = t.id and c.state = 'done'),
	       (select count(*) from comments c where c.task_id = t.id),
	       (select count(*) from task_dependencies d
	          join tasks bt on bt.id = d.source_id
	         where d.target_id = t.id and d.kind = 'blocks' and bt.state <> 'done'),
	       (select coalesce(sum(te.duration_seconds), 0)::int
	          from time_entries te where te.task_id = t.id),
	       coalesce(
	         (select json_agg(json_build_object('id', l.id, 'name', l.name, 'color', l.color)
	                          order by l.name)
	            from task_labels tl join labels l on l.id = tl.label_id
	           where tl.task_id = t.id), '[]'::json)::text
	  from tasks t
	  join projects pr on pr.id = t.project_id
	  left join profiles a on a.id = t.assignee_id`

func scanTask(row interface{ Scan(...any) error }) (models.Task, error) {
	var (
		t          models.Task
		labelsJSON string

		assigneeID     *uuid.UUID
		assigneeEmail  *string
		assigneeName   *string
		assigneeAvatar *string
		assigneePres   *string
	)

	err := row.Scan(&t.ID, &t.WorkspaceID, &t.ProjectID, &t.ProjectKey, &t.ParentID, &t.Number,
		&t.Title, &t.Description, &t.State, &t.Priority, &t.BoardRank,
		&t.AssigneeID, &t.ReporterID, &t.StartDate, &t.DueDate, &t.EstimateHours,
		&t.CompletedAt, &t.CreatedAt, &t.UpdatedAt,
		&assigneeID, &assigneeEmail, &assigneeName, &assigneeAvatar, &assigneePres,
		&t.SubtaskCount, &t.SubtaskDone, &t.CommentCount, &t.BlockedBy, &t.LoggedSecs,
		&labelsJSON)
	if err != nil {
		return t, err
	}

	t.Ref = taskRef(t.ProjectKey, t.Number)
	t.Labels = parseLabels(labelsJSON)

	if assigneeID != nil {
		t.Assignee = &models.Profile{
			ID:        *assigneeID,
			Email:     deref(assigneeEmail),
			FullName:  assigneeName,
			AvatarURL: assigneeAvatar,
			Presence:  deref(assigneePres),
		}
	}
	return t, nil
}

func taskRef(key string, number int) string { return key + "-" + strconv.Itoa(number) }

// ---------------------------------------------------------------- list

// handleListTasks powers every view (board, list, calendar, Gantt). Filters are
// composed into one WHERE clause; unset filters cost nothing.
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	q := r.URL.Query()
	where := []string{"t.workspace_id = $1"}
	args := []any{wsID}

	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if v := q.Get("project_id"); v != "" {
		id, err := httpx.UUIDParam(v, "project_id")
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		add("t.project_id = $%d", id)
	}
	if v := q.Get("assignee_id"); v != "" {
		if v == "unassigned" {
			where = append(where, "t.assignee_id is null")
		} else {
			id, err := httpx.UUIDParam(v, "assignee_id")
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
			add("t.assignee_id = $%d", id)
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
		add("t.state = any($%d::task_state[])", states)
	}
	if v := q.Get("priority"); v != "" {
		add("t.priority = any($%d::task_priority[])", strings.Split(v, ","))
	}
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		add("t.title ilike '%%' || $%d || '%%'", v)
	}
	if v := q.Get("parent_id"); v != "" {
		if v == "none" {
			where = append(where, "t.parent_id is null")
		} else {
			id, err := httpx.UUIDParam(v, "parent_id")
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
			add("t.parent_id = $%d", id)
		}
	}
	if v := q.Get("due_before"); v != "" {
		add("t.due_date < $%d::timestamptz", v)
	}
	if v := q.Get("due_after"); v != "" {
		add("t.due_date >= $%d::timestamptz", v)
	}
	if q.Get("include_done") != "true" && q.Get("state") == "" {
		where = append(where, "t.state <> 'cancelled'")
	}

	limit := httpx.QueryInt(r, "limit", 500, 1000)
	args = append(args, limit)

	query := taskSelect + " where " + strings.Join(where, " and ") +
		" order by t.state, t.board_rank, t.created_at" +
		fmt.Sprintf(" limit $%d", len(args))

	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
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
	t, err := scanTask(s.db.QueryRow(ctx,
		taskSelect+` where t.id = $1 and t.workspace_id = $2`, taskID, wsID))
	if err != nil {
		return t, db.MapError(err)
	}
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

	var taskID uuid.UUID
	err = s.db.InTx(r.Context(), func(tx pgxTx) error {
		// New cards land at the top of their column.
		err := tx.QueryRow(r.Context(), `
			insert into tasks (workspace_id, project_id, parent_id, title, description, state,
			                   priority, assignee_id, reporter_id, start_date, due_date,
			                   estimate_hours, board_rank)
			select $1, $2, nullif($3,'')::uuid, $4, $5, $6::task_state, $7::task_priority,
			       nullif($8,'')::uuid, $9, nullif($10,'')::timestamptz, nullif($11,'')::timestamptz,
			       $12,
			       coalesce((select min(board_rank) from tasks
			                  where project_id = $2 and state = $6::task_state), 131072) / 2
			 where exists (select 1 from projects p where p.id = $2 and p.workspace_id = $1)
			returning id`,
			wsID, projectID, derefString(req.ParentID), req.Title, req.Description, req.State,
			req.Priority, derefString(req.AssigneeID), userID,
			derefString(req.StartDate), derefString(req.DueDate), req.EstimateHours,
		).Scan(&taskID)
		if err != nil {
			return err
		}

		if len(req.LabelIDs) > 0 {
			if _, err := tx.Exec(r.Context(), `
				insert into task_labels (task_id, label_id)
				select $1, l.id from labels l
				 where l.id = any($2::uuid[]) and l.workspace_id = $3
				on conflict do nothing`, taskID, req.LabelIDs, wsID); err != nil {
				return err
			}
		}

		_, err = tx.Exec(r.Context(), `
			insert into activities (workspace_id, task_id, project_id, actor_id, verb)
			values ($1, $2, $3, $4, 'created')`, wsID, taskID, projectID, userID)
		return err
	})
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
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

	err = s.db.InTx(r.Context(), func(tx pgxTx) error {
		if _, err := tx.Exec(r.Context(), `
			update tasks set
			  title         = coalesce($3, title),
			  description   = case when $4::text is null then description else nullif($4,'') end,
			  state         = coalesce($5::task_state, state),
			  priority      = coalesce($6::task_priority, priority),
			  assignee_id   = case when $7::text is null then assignee_id else nullif($7,'')::uuid end,
			  parent_id     = case when $8::text is null then parent_id else nullif($8,'')::uuid end,
			  start_date    = case when $9::text is null then start_date else nullif($9,'')::timestamptz end,
			  due_date      = case when $10::text is null then due_date else nullif($10,'')::timestamptz end,
			  estimate_hours = coalesce($11, estimate_hours)
			where id = $1 and workspace_id = $2`,
			taskID, wsID, req.Title, req.Description, req.State, req.Priority,
			req.AssigneeID, req.ParentID, req.StartDate, req.DueDate, req.EstimateHours,
		); err != nil {
			return err
		}

		// A present label_ids array replaces the whole set; absent leaves it alone.
		if req.LabelIDs != nil {
			if _, err := tx.Exec(r.Context(),
				`delete from task_labels where task_id = $1`, taskID); err != nil {
				return err
			}
			if len(*req.LabelIDs) > 0 {
				if _, err := tx.Exec(r.Context(), `
					insert into task_labels (task_id, label_id)
					select $1, l.id from labels l
					 where l.id = any($2::uuid[]) and l.workspace_id = $3`,
					taskID, *req.LabelIDs, wsID); err != nil {
					return err
				}
			}
		}

		return s.recordChanges(r.Context(), tx, wsID, userID, before, req)
	})
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
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
// task's activity stream renders.
func (s *Server) recordChanges(ctx context.Context, tx pgxTx, wsID, actorID uuid.UUID,
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

	for _, c := range changes {
		verb := "updated"
		if c.field == "state" {
			verb = "state_changed"
		} else if c.field == "assignee" {
			verb = "assigned"
		}
		if _, err := tx.Exec(ctx, `
			insert into activities (workspace_id, task_id, project_id, actor_id,
			                        verb, field, old_value, new_value)
			values ($1, $2, $3, $4, $5, $6, nullif($7,''), nullif($8,''))`,
			wsID, before.ID, before.ProjectID, actorID, verb, c.field, c.old, c.new); err != nil {
			return err
		}
	}
	return nil
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

	// Confirm the task is in this workspace before the RPC, which is not
	// workspace-scoped on its own.
	var exists bool
	if err := s.db.QueryRow(r.Context(),
		`select exists (select 1 from tasks where id = $1 and workspace_id = $2)`,
		taskID, wsID).Scan(&exists); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	if !exists {
		httpx.Fail(w, r, httpx.ErrNotFound)
		return
	}

	var rank float64
	if err := s.db.QueryRow(r.Context(),
		`select move_task($1, $2::task_state, $3, $4, $5)`,
		taskID, req.State, req.BeforeRank, req.AfterRank, userID).Scan(&rank); err != nil {
		httpx.Fail(w, r, db.MapError(err))
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

	tag, err := s.db.Exec(r.Context(),
		`delete from tasks where id = $1 and workspace_id = $2`, taskID, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	if tag.RowsAffected() == 0 {
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

	_, err := s.db.Exec(ctx, `
		insert into notifications (workspace_id, user_id, actor_id, kind, title, body, task_id)
		values ($1, $2, $3, 'assignment', $4, $5, $6)`,
		wsID, *t.AssigneeID, actorID, "You were assigned "+t.Ref, t.Title, t.ID)
	if err != nil {
		return // a missed notification must never fail the write that caused it
	}

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
