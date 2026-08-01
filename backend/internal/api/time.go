package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

const timeEntrySelect = `
	select e.id, e.task_id, t.title, e.user_id, e.description,
	       e.started_at, e.ended_at, e.duration_seconds, e.is_billable
	  from time_entries e
	  left join tasks t on t.id = e.task_id`

func scanTimeEntry(row interface{ Scan(...any) error }) (models.TimeEntry, error) {
	var e models.TimeEntry
	err := row.Scan(&e.ID, &e.TaskID, &e.TaskTitle, &e.UserID, &e.Description,
		&e.StartedAt, &e.EndedAt, &e.DurationSeconds, &e.IsBillable)
	return e, err
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

	rows, err := s.db.Query(r.Context(), timeEntrySelect+`
		 where e.workspace_id = $1 and e.user_id = $2
		   and ($3::timestamptz is null or e.started_at >= $3)
		   and ($4::timestamptz is null or e.started_at < $4)
		 order by e.started_at desc
		 limit $5`,
		wsID, target,
		optionalString(r.URL.Query().Get("from")),
		optionalString(r.URL.Query().Get("to")),
		httpx.QueryInt(r, "limit", 100, 500))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.TimeEntry{}
	total := 0
	for rows.Next() {
		e, err := scanTimeEntry(rows)
		if err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		if e.DurationSeconds != nil {
			total += *e.DurationSeconds
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
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

	e, err := scanTimeEntry(s.db.QueryRow(r.Context(), `
		with inserted as (
		  insert into time_entries (workspace_id, task_id, user_id, description,
		                            started_at, ended_at, is_billable)
		  values ($1, nullif($2,'')::uuid, $3, nullif($4,''), $5, $6, $7)
		  returning *
		)
		select e.id, e.task_id, t.title, e.user_id, e.description,
		       e.started_at, e.ended_at, e.duration_seconds, e.is_billable
		  from inserted e left join tasks t on t.id = e.task_id`,
		wsID, derefString(req.TaskID), userID, derefString(req.Description),
		started, ended, req.IsBillable))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusCreated, e)
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

	e, err := scanTimeEntry(s.db.QueryRow(r.Context(), `
		with inserted as (
		  insert into time_entries (workspace_id, task_id, user_id, description, started_at)
		  values ($1, nullif($2,'')::uuid, $3, nullif($4,''), now())
		  returning *
		)
		select e.id, e.task_id, t.title, e.user_id, e.description,
		       e.started_at, e.ended_at, e.duration_seconds, e.is_billable
		  from inserted e left join tasks t on t.id = e.task_id`,
		wsID, derefString(req.TaskID), userID, derefString(req.Description)))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
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

	e, err := scanTimeEntry(s.db.QueryRow(r.Context(), `
		with stopped as (
		  update time_entries set ended_at = now()
		   where user_id = $1 and workspace_id = $2 and ended_at is null
		  returning *
		)
		select e.id, e.task_id, t.title, e.user_id, e.description,
		       e.started_at, e.ended_at, e.duration_seconds, e.is_billable
		  from stopped e left join tasks t on t.id = e.task_id`, userID, wsID))
	if err != nil {
		mapped := db.MapError(err)
		if mapped == httpx.ErrNotFound {
			httpx.Fail(w, r, httpx.Errorf(http.StatusConflict, "no_running_timer",
				"You do not have a timer running"))
			return
		}
		httpx.Fail(w, r, mapped)
		return
	}

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

	e, err := scanTimeEntry(s.db.QueryRow(r.Context(), timeEntrySelect+`
		 where e.user_id = $1 and e.workspace_id = $2 and e.ended_at is null`, userID, wsID))
	if err != nil {
		if db.MapError(err) == httpx.ErrNotFound {
			httpx.JSON(w, http.StatusOK, map[string]any{"entry": nil})
			return
		}
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"entry": e})
}

// ---------------------------------------------------------------- workload

// handleWorkload answers "who is overloaded this week?" — the capacity view.
func (s *Server) handleWorkload(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select user_id, full_name, avatar_url, weekly_capacity_hours,
		       open_hours, open_tasks, overdue_tasks, utilization_pct
		  from workload_summary
		 where workspace_id = $1
		 order by utilization_pct desc nulls last`, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Workload{}
	for rows.Next() {
		var wl models.Workload
		if err := rows.Scan(&wl.UserID, &wl.FullName, &wl.AvatarURL, &wl.WeeklyCapacityHours,
			&wl.OpenHours, &wl.OpenTasks, &wl.OverdueTasks, &wl.UtilizationPct); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, wl)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	// Availability blocks overlapping the next 14 days, so the capacity bars can
	// grey out people who are away.
	availRows, err := s.db.Query(r.Context(), `
		select id, user_id, kind, note, starts_at, ends_at, available_hours
		  from availability_blocks
		 where workspace_id = $1
		   and tstzrange(starts_at, ends_at) && tstzrange(now(), now() + interval '14 days')
		 order by starts_at`, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer availRows.Close()

	type block struct {
		ID             uuid.UUID `json:"id"`
		UserID         uuid.UUID `json:"user_id"`
		Kind           string    `json:"kind"`
		Note           *string   `json:"note"`
		StartsAt       time.Time `json:"starts_at"`
		EndsAt         time.Time `json:"ends_at"`
		AvailableHours *float64  `json:"available_hours"`
	}
	blocks := []block{}
	for availRows.Next() {
		var b block
		if err := availRows.Scan(&b.ID, &b.UserID, &b.Kind, &b.Note,
			&b.StartsAt, &b.EndsAt, &b.AvailableHours); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		blocks = append(blocks, b)
	}
	if err := availRows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
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
