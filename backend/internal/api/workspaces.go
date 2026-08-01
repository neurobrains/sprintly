package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sprintly/sprintly/backend/internal/auth"
	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

// ---------------------------------------------------------------- create

type createWorkspaceRequest struct {
	Name       string `json:"name"`
	Slug       string `json:"slug,omitempty"`
	JoinPolicy string `json:"join_policy,omitempty"` // open | request | invite_only
}

// handleCreateWorkspace is onboarding option 1: "Create a new workspace".
//
// The whole setup (workspace, owner membership, default team, starter project,
// seed tasks, #general, labels) happens in the create_workspace RPC so it is one
// transaction — a half-created workspace with no owner cannot be recovered.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	user, err := auth.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req createWorkspaceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) < 2 || len(req.Name) > 80 {
		httpx.Fail(w, r, httpx.BadRequest("Workspace name must be between 2 and 80 characters"))
		return
	}
	if req.JoinPolicy == "" {
		req.JoinPolicy = "request"
	}
	if !validJoinPolicy(req.JoinPolicy) {
		httpx.Fail(w, r, httpx.BadRequest("join_policy must be open, request, or invite_only"))
		return
	}

	// The RPC references profiles(id), so make sure ours exists first — a user
	// can hit this endpoint before ever calling /me.
	if err := s.ensureProfile(r, user); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var ws models.Workspace
	err = s.db.QueryRow(r.Context(), `
		select id, name, slug, join_code, join_policy, logo_url, created_by, created_at
		  from create_workspace($1, $2, nullif($3,''), $4::join_policy)`,
		user.ID, req.Name, strings.TrimSpace(req.Slug), req.JoinPolicy,
	).Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.JoinCode, &ws.JoinPolicy,
		&ws.LogoURL, &ws.CreatedBy, &ws.CreatedAt)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	ws.Role = "owner"
	ws.MemberCount = 1
	httpx.JSON(w, http.StatusCreated, ws)
}

// ---------------------------------------------------------------- join

type joinWorkspaceRequest struct {
	// Reference is the workspace UUID or its short join code ("SPRNT-7QK2XM").
	Reference string `json:"reference"`
	Message   string `json:"message,omitempty"`
}

// handleJoinWorkspace is onboarding option 2: "Join an existing workspace".
//
// Result depends on the target's join_policy:
//   - open        -> status "joined", the caller is a contributor immediately
//   - request     -> status "pending", admins are notified to approve
//   - invite_only -> 403
func (s *Server) handleJoinWorkspace(w http.ResponseWriter, r *http.Request) {
	user, err := auth.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req joinWorkspaceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	req.Reference = strings.TrimSpace(req.Reference)
	if req.Reference == "" {
		httpx.Fail(w, r, httpx.BadRequest("Enter a workspace ID or join code"))
		return
	}
	if len(req.Message) > 500 {
		httpx.Fail(w, r, httpx.BadRequest("Message must be 500 characters or fewer"))
		return
	}

	if err := s.ensureProfile(r, user); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var (
		wsID   uuid.UUID
		name   string
		slug   string
		status string
	)
	err = s.db.QueryRow(r.Context(),
		`select workspace_id, name, slug, status from join_workspace($1, $2, nullif($3,''))`,
		user.ID, req.Reference, strings.TrimSpace(req.Message),
	).Scan(&wsID, &name, &slug, &status)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	if status == "joined" {
		s.hub.Publish(realtime.Event{
			Type:        "member.joined",
			WorkspaceID: wsID,
			ActorID:     user.ID,
			Payload:     realtime.Marshal(map[string]any{"user_id": user.ID, "name": user.Name}),
		})
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"workspace_id": wsID,
		"name":         name,
		"slug":         slug,
		"status":       status,
	})
}

// handleLookupWorkspace previews a workspace before joining, so the join screen
// can confirm "Acme Inc · 24 members" instead of accepting a blind code.
// It intentionally exposes only public-facing fields.
func (s *Server) handleLookupWorkspace(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.URL.Query().Get("reference"))
	if ref == "" {
		httpx.Fail(w, r, httpx.BadRequest("reference query parameter is required"))
		return
	}

	var (
		id          uuid.UUID
		name, slug  string
		policy      string
		logo        *string
		memberCount int
	)
	err := s.db.QueryRow(r.Context(), `
		select w.id, w.name, w.slug, w.join_policy, w.logo_url,
		       (select count(*) from workspace_members m
		         where m.workspace_id = w.id and m.status = 'active')
		  from workspaces w
		 where w.id::text = $1
		    or w.join_code = upper(replace($1, '-', ''))
		    or w.slug = lower($1)`, ref,
	).Scan(&id, &name, &slug, &policy, &logo, &memberCount)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Fail(w, r, httpx.Errorf(http.StatusNotFound, "workspace_not_found",
			"No workspace matches that ID or join code"))
		return
	}
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": id, "name": name, "slug": slug,
		"join_policy": policy, "logo_url": logo, "member_count": memberCount,
	})
}

// ---------------------------------------------------------------- read / update

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	_, wsID, role, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var ws models.Workspace
	err = s.db.QueryRow(r.Context(), `
		select w.id, w.name, w.slug,
		       case when $2 then w.join_code else '' end,
		       w.join_policy, w.logo_url, w.created_by, w.created_at,
		       (select count(*) from workspace_members m
		         where m.workspace_id = w.id and m.status = 'active')
		  from workspaces w where w.id = $1`,
		wsID, canManage(role),
	).Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.JoinCode, &ws.JoinPolicy,
		&ws.LogoURL, &ws.CreatedBy, &ws.CreatedAt, &ws.MemberCount)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	ws.Role = role
	httpx.JSON(w, http.StatusOK, ws)
}

type updateWorkspaceRequest struct {
	Name       *string `json:"name"`
	JoinPolicy *string `json:"join_policy"`
	LogoURL    *string `json:"logo_url"`
}

func (s *Server) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	_, wsID, role, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req updateWorkspaceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.JoinPolicy != nil && !validJoinPolicy(*req.JoinPolicy) {
		httpx.Fail(w, r, httpx.BadRequest("join_policy must be open, request, or invite_only"))
		return
	}

	var ws models.Workspace
	err = s.db.QueryRow(r.Context(), `
		update workspaces set
		  name        = coalesce($2, name),
		  join_policy = coalesce($3::join_policy, join_policy),
		  logo_url    = case when $4::text is null then logo_url else nullif($4,'') end
		where id = $1
		returning id, name, slug, join_code, join_policy, logo_url, created_by, created_at`,
		wsID, req.Name, req.JoinPolicy, req.LogoURL,
	).Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.JoinCode, &ws.JoinPolicy,
		&ws.LogoURL, &ws.CreatedBy, &ws.CreatedAt)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	ws.Role = role
	s.hub.Publish(realtime.Event{Type: "workspace.updated", WorkspaceID: wsID,
		Payload: realtime.Marshal(ws)})
	httpx.JSON(w, http.StatusOK, ws)
}

// handleRotateJoinCode invalidates the old code — the fix when a code leaks.
func (s *Server) handleRotateJoinCode(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var code string
	err = s.db.QueryRow(r.Context(),
		`update workspaces set join_code = gen_join_code() where id = $1 returning join_code`,
		wsID).Scan(&code)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"join_code": code})
}

// ---------------------------------------------------------------- members

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select m.user_id, p.email, p.full_name, p.avatar_url, m.role, m.status,
		       m.title, m.weekly_capacity_hours, p.presence, m.joined_at
		  from workspace_members m
		  join profiles p on p.id = m.user_id
		 where m.workspace_id = $1
		 order by array_position(
		            array['owner','admin','manager','contributor','guest'], m.role::text),
		          coalesce(p.full_name, p.email)`, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Member{}
	for rows.Next() {
		var m models.Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.FullName, &m.AvatarURL, &m.Role,
			&m.Status, &m.Title, &m.WeeklyCapacityHours, &m.Presence, &m.JoinedAt); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"members": out,
		"online":  s.hub.OnlineUsers(wsID),
	})
}

type updateMemberRequest struct {
	Role                *string  `json:"role"`
	Title               *string  `json:"title"`
	WeeklyCapacityHours *float64 `json:"weekly_capacity_hours"`
	Status              *string  `json:"status"`
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	actorID, wsID, actorRole, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	targetID, err := httpx.UUIDParam(chi.URLParam(r, "userID"), "userID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req updateMemberRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if req.Role != nil {
		if !validRole(*req.Role) {
			httpx.Fail(w, r, httpx.BadRequest("role must be admin, manager, contributor, or guest"))
			return
		}
		// Ownership transfer is its own operation; it would break the
		// single-owner index if allowed here.
		if *req.Role == "owner" {
			httpx.Fail(w, r, httpx.BadRequest("Use the ownership transfer flow to change the owner"))
			return
		}
		// You may not grant a role above your own, or demote someone senior.
		if !roleAtLeast(actorRole, *req.Role) {
			httpx.Fail(w, r, httpx.Errorf(http.StatusForbidden, "forbidden",
				"You cannot grant a role higher than your own"))
			return
		}
		var targetRole string
		if err := s.db.QueryRow(r.Context(),
			`select role from workspace_members where workspace_id=$1 and user_id=$2`,
			wsID, targetID).Scan(&targetRole); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		if targetRole == "owner" || (targetID != actorID && !roleAtLeast(actorRole, targetRole)) {
			httpx.Fail(w, r, httpx.Errorf(http.StatusForbidden, "forbidden",
				"You cannot change that member's role"))
			return
		}
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "suspended" {
		httpx.Fail(w, r, httpx.BadRequest("status must be active or suspended"))
		return
	}

	var m models.Member
	err = s.db.QueryRow(r.Context(), `
		update workspace_members m set
		  role   = coalesce($3::workspace_role, m.role),
		  title  = case when $4::text is null then m.title else nullif($4,'') end,
		  weekly_capacity_hours = coalesce($5, m.weekly_capacity_hours),
		  status = coalesce($6::member_status, m.status)
		from profiles p
		where m.workspace_id = $1 and m.user_id = $2 and p.id = m.user_id
		returning m.user_id, p.email, p.full_name, p.avatar_url, m.role, m.status,
		          m.title, m.weekly_capacity_hours, p.presence, m.joined_at`,
		wsID, targetID, req.Role, req.Title, req.WeeklyCapacityHours, req.Status,
	).Scan(&m.UserID, &m.Email, &m.FullName, &m.AvatarURL, &m.Role, &m.Status,
		&m.Title, &m.WeeklyCapacityHours, &m.Presence, &m.JoinedAt)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	s.hub.Publish(realtime.Event{Type: "member.updated", WorkspaceID: wsID,
		ActorID: actorID, Payload: realtime.Marshal(m)})
	httpx.JSON(w, http.StatusOK, m)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	actorID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	targetID, err := httpx.UUIDParam(chi.URLParam(r, "userID"), "userID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	tag, err := s.db.Exec(r.Context(), `
		delete from workspace_members
		 where workspace_id = $1 and user_id = $2 and role <> 'owner'`, wsID, targetID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(w, r, httpx.Errorf(http.StatusConflict, "cannot_remove",
			"That member does not exist, or is the workspace owner"))
		return
	}

	s.hub.Publish(realtime.Event{Type: "member.removed", WorkspaceID: wsID,
		ActorID: actorID, Payload: realtime.Marshal(map[string]any{"user_id": targetID})})
	httpx.JSON(w, http.StatusNoContent, nil)
}

// ---------------------------------------------------------------- join requests

func (s *Server) handleListJoinRequests(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select j.id, j.user_id, p.email, p.full_name, p.avatar_url,
		       j.message, j.status, j.created_at
		  from workspace_join_requests j
		  join profiles p on p.id = j.user_id
		 where j.workspace_id = $1 and j.status = 'pending'
		 order by j.created_at`, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.JoinRequest{}
	for rows.Next() {
		var jr models.JoinRequest
		if err := rows.Scan(&jr.ID, &jr.UserID, &jr.Email, &jr.FullName, &jr.AvatarURL,
			&jr.Message, &jr.Status, &jr.CreatedAt); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, jr)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"requests": out})
}

type decideJoinRequest struct {
	Approve bool   `json:"approve"`
	Role    string `json:"role,omitempty"`
}

func (s *Server) handleDecideJoinRequest(w http.ResponseWriter, r *http.Request) {
	actorID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	reqID, err := httpx.UUIDParam(chi.URLParam(r, "requestID"), "requestID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var body decideJoinRequest
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if body.Role == "" {
		body.Role = "contributor"
	}
	if !validRole(body.Role) || body.Role == "owner" {
		httpx.Fail(w, r, httpx.BadRequest("role must be admin, manager, contributor, or guest"))
		return
	}

	if _, err := s.db.Exec(r.Context(),
		`select decide_join_request($1, $2, $3, $4::workspace_role)`,
		actorID, reqID, body.Approve, body.Role); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	if body.Approve {
		s.hub.Publish(realtime.Event{Type: "member.joined", WorkspaceID: wsID, ActorID: actorID})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"approved": body.Approve})
}

// ---------------------------------------------------------------- helpers

func (s *Server) ensureProfile(r *http.Request, user *auth.User) error {
	_, err := s.db.Exec(r.Context(), `
		insert into profiles (id, email, full_name, avatar_url)
		values ($1, $2, nullif($3,''), nullif($4,''))
		on conflict (id) do update
		   set email = excluded.email, updated_at = now()`,
		user.ID, user.Email, user.Name, user.AvatarURL)
	return db.MapError(err)
}

func validJoinPolicy(v string) bool {
	return v == "open" || v == "request" || v == "invite_only"
}

func validRole(v string) bool {
	switch v {
	case "owner", "admin", "manager", "contributor", "guest":
		return true
	}
	return false
}

var roleRank = map[string]int{
	"guest": 0, "contributor": 1, "manager": 2, "admin": 3, "owner": 4,
}

func roleAtLeast(have, want string) bool { return roleRank[have] >= roleRank[want] }

func canManage(role string) bool { return roleAtLeast(role, "manager") }
