package api

import (
	"net/http"
	"time"

	"github.com/sprintly/sprintly/backend/internal/auth"
	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
)

// handleMe returns the caller's profile, upserting it from the JWT first.
//
// The auth.users trigger normally creates the row, but a project restored from a
// dump or a user created before the trigger existed would have none. Doing it
// here makes /me the reliable "ensure I exist" call the frontend makes on boot.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := auth.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var p models.Profile
	err = s.db.QueryRow(r.Context(), `
		insert into profiles (id, email, full_name, avatar_url)
		values ($1, $2, nullif($3,''), nullif($4,''))
		on conflict (id) do update
		   set email      = excluded.email,
		       full_name  = coalesce(profiles.full_name, excluded.full_name),
		       avatar_url = coalesce(excluded.avatar_url, profiles.avatar_url),
		       last_seen_at = now(),
		       updated_at = now()
		returning id, email, full_name, avatar_url, timezone, presence, presence_note, last_seen_at`,
		user.ID, user.Email, user.Name, user.AvatarURL,
	).Scan(&p.ID, &p.Email, &p.FullName, &p.AvatarURL, &p.Timezone, &p.Presence, &p.PresenceNote, &p.LastSeenAt)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"profile":  p,
		"provider": user.Provider,
	})
}

type updateMeRequest struct {
	FullName     *string `json:"full_name"`
	Timezone     *string `json:"timezone"`
	Presence     *string `json:"presence"`
	PresenceNote *string `json:"presence_note"`
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	user, err := auth.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req updateMeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.Presence != nil && !validPresence(*req.Presence) {
		httpx.Fail(w, r, httpx.BadRequest("presence must be one of online, away, in_meeting, focus, offline"))
		return
	}

	var p models.Profile
	err = s.db.QueryRow(r.Context(), `
		update profiles set
		  full_name     = coalesce($2, full_name),
		  timezone      = coalesce($3, timezone),
		  presence      = coalesce($4::presence_state, presence),
		  presence_note = case when $5::text is null then presence_note else nullif($5,'') end,
		  last_seen_at  = now(),
		  updated_at    = now()
		where id = $1
		returning id, email, full_name, avatar_url, timezone, presence, presence_note, last_seen_at`,
		user.ID, req.FullName, req.Timezone, req.Presence, req.PresenceNote,
	).Scan(&p.ID, &p.Email, &p.FullName, &p.AvatarURL, &p.Timezone, &p.Presence, &p.PresenceNote, &p.LastSeenAt)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, p)
}

func validPresence(v string) bool {
	switch v {
	case "online", "away", "in_meeting", "focus", "offline":
		return true
	}
	return false
}

// handleListMyWorkspaces drives the post-login routing decision: no workspaces
// means send the user to onboarding, one means jump straight into it.
func (s *Server) handleListMyWorkspaces(w http.ResponseWriter, r *http.Request) {
	user, err := auth.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select w.id, w.name, w.slug,
		       case when m.role in ('owner','admin','manager') then w.join_code else '' end,
		       w.join_policy, w.logo_url, w.created_by, w.created_at,
		       m.role,
		       (select count(*) from workspace_members x
		         where x.workspace_id = w.id and x.status = 'active')
		  from workspaces w
		  join workspace_members m on m.workspace_id = w.id
		 where m.user_id = $1 and m.status = 'active'
		 order by w.created_at`, user.ID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	list := []models.Workspace{}
	for rows.Next() {
		var ws models.Workspace
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.JoinCode, &ws.JoinPolicy,
			&ws.LogoURL, &ws.CreatedBy, &ws.CreatedAt, &ws.Role, &ws.MemberCount); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		list = append(list, ws)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	// Pending join requests let the UI show "waiting for approval" instead of
	// dumping the user back onto an empty onboarding screen.
	pendingRows, err := s.db.Query(r.Context(), `
		select w.name, w.slug, j.created_at
		  from workspace_join_requests j
		  join workspaces w on w.id = j.workspace_id
		 where j.user_id = $1 and j.status = 'pending'
		 order by j.created_at desc`, user.ID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer pendingRows.Close()

	type pending struct {
		Name      string    `json:"name"`
		Slug      string    `json:"slug"`
		CreatedAt time.Time `json:"created_at"`
	}
	pendingList := []pending{}
	for pendingRows.Next() {
		var p pending
		if err := pendingRows.Scan(&p.Name, &p.Slug, &p.CreatedAt); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		pendingList = append(pendingList, p)
	}
	if err := pendingRows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"workspaces":       list,
		"pending_requests": pendingList,
	})
}

// handleMyTasks is the cross-workspace "My Work" inbox.
func (s *Server) handleMyTasks(w http.ResponseWriter, r *http.Request) {
	user, err := auth.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select t.id, t.workspace_id, t.project_id, p.key, t.number, t.title,
		       t.state, t.priority, t.due_date, t.estimate_hours, w.slug, w.name
		  from tasks t
		  join projects   p on p.id = t.project_id
		  join workspaces w on w.id = t.workspace_id
		  join workspace_members m
		    on m.workspace_id = t.workspace_id and m.user_id = $1 and m.status = 'active'
		 where t.assignee_id = $1 and t.state not in ('done','cancelled')
		 order by t.due_date nulls last,
		          array_position(array['urgent','high','medium','low','none'], t.priority::text),
		          t.created_at
		 limit $2`, user.ID, httpx.QueryInt(r, "limit", 100, 300))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	type myTask struct {
		models.Task
		WorkspaceSlug string `json:"workspace_slug"`
		WorkspaceName string `json:"workspace_name"`
	}

	out := []myTask{}
	for rows.Next() {
		var t myTask
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.ProjectID, &t.ProjectKey, &t.Number,
			&t.Title, &t.State, &t.Priority, &t.DueDate, &t.EstimateHours,
			&t.WorkspaceSlug, &t.WorkspaceName); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		t.Ref = taskRef(t.ProjectKey, t.Number)
		t.Labels = []models.Label{}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": out})
}
