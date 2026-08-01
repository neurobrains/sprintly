package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/httpx"
	"github.com/sprintly/sprintly/backend/models"
	"github.com/sprintly/sprintly/backend/realtime"
)

// timeEntryCols embeds the task title through the task_id foreign key, which is
// the one join this resource needs.
const timeEntryCols = "id,task_id,user_id,description,started_at,ended_at," +
	"duration_seconds,is_billable,tasks(title)"

// timeEntryRow is the wire shape PostgREST returns: models.TimeEntry plus the
// embedded task object, which is flattened into TaskTitle.
type timeEntryRow struct {
	models.TimeEntry
	Task *struct {
		Title string `json:"title"`
	} `json:"tasks"`
}

func (r timeEntryRow) entry() models.TimeEntry {
	e := r.TimeEntry
	if r.Task != nil {
		title := r.Task.Title
		e.TaskTitle = &title
	}
	return e
}

func flattenEntries(rows []timeEntryRow) []models.TimeEntry {
	out := make([]models.TimeEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.entry())
	}
	return out
}

// handleListTimeEntries defaults to the caller's own entries. Managers may pass
// ?user_id= to review anyone's, which is what makes the workload report usable.
func (s *Server) handleListTimeEntries(w http.ResponseWriter, r *http.Request) {
	userID, wsID, role, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	target := userID
	if raw := r.URL.Query().Get("user_id"); raw != "" && raw != userID.String() {
		if !canManage(role) {
			httpx.Fail(w, r, httpx.Errorf(http.StatusForbidden, "forbidden",
				"Only managers can view another member's time entries"))
			return
		}
		if target, err = httpx.UUIDParam(raw, "user_id"); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}

	q := s.data.From("time_entries").
		Select(timeEntryCols).
		Eq("workspace_id", wsID).
		Eq("user_id", target).
		Order("started_at", true).
		Limit(httpx.QueryInt(r, "limit", 100, 500))

	if from := strings.TrimSpace(r.URL.Query().Get("from")); from != "" {
		q = q.Gte("started_at", from)
	}
	if to := strings.TrimSpace(r.URL.Query().Get("to")); to != "" {
		q = q.Lt("started_at", to)
	}

	var rows []timeEntryRow
	if err := q.Get(r.Context(), &rows); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := flattenEntries(rows)
	total := 0
	for _, e := range out {
		if e.DurationSeconds != nil {
			total += *e.DurationSeconds
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"entries": out, "total_seconds": total,
	})
}

type logTimeRequest struct {
	TaskID      *string `json:"task_id"`
	Description *string `json:"description"`
	StartedAt   string  `json:"started_at"`
	EndedAt     string  `json:"ended_at"`
	// DurationMinutes is the manual-entry shortcut: log 90 minutes ending now.
	DurationMinutes *int `json:"duration_minutes"`
	IsBillable      bool `json:"is_billable"`
}

// handleLogTime records a completed block of work after the fact.
func (s *Server) handleLogTime(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req logTimeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var started, ended time.Time
	switch {
	case req.DurationMinutes != nil:
		if *req.DurationMinutes <= 0 || *req.DurationMinutes > 24*60 {
			httpx.Fail(w, r, httpx.BadRequest("duration_minutes must be between 1 and 1440"))
			return
		}
		ended = time.Now().UTC()
		if req.EndedAt != "" {
			if ended, err = time.Parse(time.RFC3339, req.EndedAt); err != nil {
				httpx.Fail(w, r, httpx.BadRequest("ended_at must be RFC3339"))
				return
			}
		}
		started = ended.Add(-time.Duration(*req.DurationMinutes) * time.Minute)

	case req.StartedAt != "" && req.EndedAt != "":
		if started, err = time.Parse(time.RFC3339, req.StartedAt); err != nil {
			httpx.Fail(w, r, httpx.BadRequest("started_at must be RFC3339"))
			return
		}
		if ended, err = time.Parse(time.RFC3339, req.EndedAt); err != nil {
			httpx.Fail(w, r, httpx.BadRequest("ended_at must be RFC3339"))
			return
		}
		if !ended.After(started) {
			httpx.Fail(w, r, httpx.BadRequest("ended_at must be after started_at"))
			return
		}

	default:
		httpx.Fail(w, r, httpx.BadRequest("Provide duration_minutes, or both started_at and ended_at"))
		return
	}

	e, err := s.insertTimeEntry(r.Context(), map[string]any{
		"workspace_id": wsID,
		"user_id":      userID,
		"task_id":      nilIfEmpty(derefString(req.TaskID)),
		"description":  nilIfEmpty(derefString(req.Description)),
		"started_at":   started,
		"ended_at":     ended,
		"is_billable":  req.IsBillable,
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, e)
}

// insertTimeEntry writes the row, then re-reads it so the generated
// duration_seconds column and the embedded task title come back populated —
// PostgREST's returned representation does not include embedded resources.
func (s *Server) insertTimeEntry(ctx context.Context, body map[string]any) (models.TimeEntry, error) {
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := s.data.From("time_entries").
		Select("id").
		Single().
		Insert(ctx, body, &created); err != nil {
		return models.TimeEntry{}, err
	}

	var row timeEntryRow
	if err := s.data.From("time_entries").
		Select(timeEntryCols).
		Eq("id", created.ID).
		Single().
		Get(ctx, &row); err != nil {
		return models.TimeEntry{}, err
	}
	return row.entry(), nil
}

type startTimerRequest struct {
	TaskID      *string `json:"task_id"`
	Description *string `json:"description"`
}

// handleStartTimer opens a running entry. The one-running-timer unique index
// turns a double-click into a 409 rather than two overlapping timers.
func (s *Server) handleStartTimer(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req startTimerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	e, err := s.insertTimeEntry(r.Context(), map[string]any{
		"workspace_id": wsID,
		"user_id":      userID,
		"task_id":      nilIfEmpty(derefString(req.TaskID)),
		"description":  nilIfEmpty(derefString(req.Description)),
		"started_at":   time.Now().UTC(),
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	s.hub.PublishTo(wsID, userID, realtime.Event{
		Type: "timer.started", ActorID: userID, Payload: realtime.Marshal(e)})
	httpx.JSON(w, http.StatusCreated, e)
}

func (s *Server) handleStopTimer(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// Not Single(): zero matched rows means "no timer running", which is a 409
	// with its own code, not the 404 that Single() would produce.
	var updated []struct {
		ID uuid.UUID `json:"id"`
	}
	err = s.data.From("time_entries").
		Select("id").
		Eq("user_id", userID).
		Eq("workspace_id", wsID).
		IsNull("ended_at", true).
		Update(r.Context(), map[string]any{"ended_at": time.Now().UTC()}, &updated)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(updated) == 0 {
		httpx.Fail(w, r, httpx.Errorf(http.StatusConflict, "no_running_timer",
			"You do not have a timer running"))
		return
	}

	var row timeEntryRow
	if err := s.data.From("time_entries").
		Select(timeEntryCols).
		Eq("id", updated[0].ID).
		Single().
		Get(r.Context(), &row); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	e := row.entry()

	s.hub.PublishTo(wsID, userID, realtime.Event{
		Type: "timer.stopped", ActorID: userID, Payload: realtime.Marshal(e)})
	httpx.JSON(w, http.StatusOK, e)
}

// handleActiveTimer lets a reloaded tab restore the running timer.
func (s *Server) handleActiveTimer(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var rows []timeEntryRow
	err = s.data.From("time_entries").
		Select(timeEntryCols).
		Eq("user_id", userID).
		Eq("workspace_id", wsID).
		IsNull("ended_at", true).
		Limit(1).
		Get(r.Context(), &rows)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// No running timer is a normal state, not a 404.
	if len(rows) == 0 {
		httpx.JSON(w, http.StatusOK, map[string]any{"entry": nil})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entry": rows[0].entry()})
}

// ---------------------------------------------------------------- workload

// handleWorkload answers "who is overloaded this week?" — the capacity view.
func (s *Server) handleWorkload(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := []models.Workload{}
	err = s.data.From("workload_summary").
		Select("user_id,full_name,avatar_url,weekly_capacity_hours,"+
			"open_hours,open_tasks,overdue_tasks,utilization_pct").
		Eq("workspace_id", wsID).
		Order("utilization_pct", true).
		Get(r.Context(), &out)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// Availability blocks overlapping the next 14 days, so the capacity bars can
	// grey out people who are away. Two half-open comparisons are the same test
	// as the tstzrange && overlap operator, and PostgREST can express them.
	type block struct {
		ID             uuid.UUID `json:"id"`
		UserID         uuid.UUID `json:"user_id"`
		Kind           string    `json:"kind"`
		Note           *string   `json:"note"`
		StartsAt       time.Time `json:"starts_at"`
		EndsAt         time.Time `json:"ends_at"`
		AvailableHours *float64  `json:"available_hours"`
	}

	now := time.Now().UTC()
	blocks := []block{}
	err = s.data.From("availability_blocks").
		Select("id,user_id,kind,note,starts_at,ends_at,available_hours").
		Eq("workspace_id", wsID).
		Lt("starts_at", now.AddDate(0, 0, 14).Format(time.RFC3339)).
		Gt("ends_at", now.Format(time.RFC3339)).
		Order("starts_at", false).
		Get(r.Context(), &blocks)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"workload": out, "availability": blocks,
	})
}

func optionalString(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func optionalUUID(v string) *uuid.UUID {
	id, err := uuid.Parse(strings.TrimSpace(v))
	if err != nil {
		return nil
	}
	return &id
}
