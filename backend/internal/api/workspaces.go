package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/internal/auth"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

const workspaceCols = "id,name,slug,join_code,join_policy,logo_url,created_by,created_at"

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
// That mattered under pgx and matters more now: over PostgREST there is no
// client-side transaction to fall back on.
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
	if err := s.ensureProfile(r.Context(), user); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// RPC, not RPCSingle: create_workspace returns a composite (`returns
	// workspaces`), which PostgREST renders as a bare object. The object accept
	// header RPCSingle sets is for set-returning functions.
	var ws models.Workspace
	err = s.data.RPC(r.Context(), "create_workspace", map[string]any{
		"p_user":   user.ID,
		"p_name":   req.Name,
		"p_slug":   nilIfEmpty(strings.TrimSpace(req.Slug)),
		"p_policy": req.JoinPolicy,
	}, &ws)
	if err != nil {
		httpx.Fail(w, r, err)
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

	if err := s.ensureProfile(r.Context(), user); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var res struct {
		WorkspaceID uuid.UUID `json:"workspace_id"`
		Name        string    `json:"name"`
		Slug        string    `json:"slug"`
		Status      string    `json:"status"`
	}
	err = s.data.RPC(r.Context(), "join_workspace", map[string]any{
		"p_user":      user.ID,
		"p_reference": req.Reference,
		"p_message":   nilIfEmpty(strings.TrimSpace(req.Message)),
	}, &res)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if res.Status == "joined" {
		s.hub.Publish(realtime.Event{
			Type:        "member.joined",
			WorkspaceID: res.WorkspaceID,
			ActorID:     user.ID,
			Payload:     realtime.Marshal(map[string]any{"user_id": user.ID, "name": user.Name}),
		})
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"workspace_id": res.WorkspaceID,
		"name":         res.Name,
		"slug":         res.Slug,
		"status":       res.Status,
	})
}

// handleLookupWorkspace previews a workspace before joining, so the join screen
// can confirm "Acme Inc · 24 members" instead of accepting a blind code.
// It intentionally exposes only public-facing fields — no join_code.
//
// Matching a UUID, a join code and a slug in one lookup is three different
// normalisations of the same input, so it stays SQL.
func (s *Server) handleLookupWorkspace(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.URL.Query().Get("reference"))
	if ref == "" {
		httpx.Fail(w, r, httpx.BadRequest("reference query parameter is required"))
		return
	}

	var out struct {
		ID          uuid.UUID `json:"id"`
		Name        string    `json:"name"`
		Slug        string    `json:"slug"`
		JoinPolicy  string    `json:"join_policy"`
		LogoURL     *string   `json:"logo_url"`
		MemberCount int       `json:"member_count"`
	}
	err := s.data.RPCSingle(r.Context(), "lookup_workspace",
		map[string]any{"p_reference": ref}, &out)
	if err != nil {
		if err == httpx.ErrNotFound {
			httpx.Fail(w, r, httpx.Errorf(http.StatusNotFound, "workspace_not_found",
				"No workspace matches that ID or join code"))
			return
		}
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- read / update

// activeMemberCount is the "24 members" figure. PostgREST can aggregate over an
// embedded resource, and the embedded filter narrows it to active members.
func (s *Server) activeMemberCount(ctx context.Context, wsID uuid.UUID) (int, error) {
	return s.data.From("workspace_members").
		Eq("workspace_id", wsID).
		Eq("status", "active").
		Count(ctx)
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	_, wsID, role, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var ws models.Workspace
	if err := s.data.From("workspaces").
		Select(workspaceCols).
		Eq("id", wsID).
		Single().
		Get(r.Context(), &ws); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	count, err := s.activeMemberCount(r.Context(), wsID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// The join code is a credential — only managers and above see it.
	if !canManage(role) {
		ws.JoinCode = ""
	}
	ws.Role = role
	ws.MemberCount = count
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

	patch := map[string]any{}
	if req.Name != nil {
		patch["name"] = *req.Name
	}
	if req.JoinPolicy != nil {
		patch["join_policy"] = *req.JoinPolicy
	}
	if req.LogoURL != nil {
		patch["logo_url"] = nilIfEmpty(*req.LogoURL)
	}

	var ws models.Workspace
	if len(patch) == 0 {
		err = s.data.From("workspaces").Select(workspaceCols).Eq("id", wsID).Single().
			Get(r.Context(), &ws)
	} else {
		err = s.data.From("workspaces").Select(workspaceCols).Eq("id", wsID).Single().
			Update(r.Context(), patch, &ws)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	ws.Role = role
	s.hub.Publish(realtime.Event{Type: "workspace.updated", WorkspaceID: wsID,
		Payload: realtime.Marshal(ws)})
	httpx.JSON(w, http.StatusOK, ws)
}

// handleRotateJoinCode invalidates the old code — the fix when a code leaks.
// gen_join_code() is a database function, so the new value has to be generated
// there rather than sent in a patch body.
func (s *Server) handleRotateJoinCode(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// Scalar return — PostgREST sends the bare JSON value.
	var code string
	if err := s.data.RPC(r.Context(), "rotate_join_code",
		map[string]any{"p_workspace": wsID}, &code); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"join_code": code})
}

// ---------------------------------------------------------------- members

const memberCols = "user_id,role,status,title,weekly_capacity_hours,joined_at," +
	"profiles!inner(email,full_name,avatar_url,presence)"

type memberRow struct {
	models.Member
	Profile struct {
		Email     string  `json:"email"`
		FullName  *string `json:"full_name"`
		AvatarURL *string `json:"avatar_url"`
		Presence  string  `json:"presence"`
	} `json:"profiles"`
}

func (m memberRow) member() models.Member {
	out := m.Member
	out.Email = m.Profile.Email
	out.FullName = m.Profile.FullName
	out.AvatarURL = m.Profile.AvatarURL
	out.Presence = m.Profile.Presence
	return out
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var rows []memberRow
	err = s.data.From("workspace_members").
		Select(memberCols).
		Eq("workspace_id", wsID).
		Get(r.Context(), &rows)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := make([]models.Member, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.member())
	}

	// Seniority first, then display name. This was array_position() in SQL;
	// PostgREST can only order by column value, which would sort roles
	// alphabetically, so the ordering moves here.
	sort.SliceStable(out, func(i, j int) bool {
		if roleRank[out[i].Role] != roleRank[out[j].Role] {
			return roleRank[out[i].Role] > roleRank[out[j].Role]
		}
		return memberLabel(out[i]) < memberLabel(out[j])
	})

	httpx.JSON(w, http.StatusOK, map[string]any{
		"members": out,
		"online":  s.hub.OnlineUsers(wsID),
	})
}

func memberLabel(m models.Member) string {
	if m.FullName != nil && *m.FullName != "" {
		return strings.ToLower(*m.FullName)
	}
	return strings.ToLower(m.Email)
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

		var current struct {
			Role string `json:"role"`
		}
		if err := s.data.From("workspace_members").
			Select("role").
			Eq("workspace_id", wsID).
			Eq("user_id", targetID).
			Single().
			Get(r.Context(), &current); err != nil {
			httpx.Fail(w, r, err)
			return
		}
		if current.Role == "owner" || (targetID != actorID && !roleAtLeast(actorRole, current.Role)) {
			httpx.Fail(w, r, httpx.Errorf(http.StatusForbidden, "forbidden",
				"You cannot change that member's role"))
			return
		}
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "suspended" {
		httpx.Fail(w, r, httpx.BadRequest("status must be active or suspended"))
		return
	}

	patch := map[string]any{}
	if req.Role != nil {
		patch["role"] = *req.Role
	}
	if req.Title != nil {
		patch["title"] = nilIfEmpty(*req.Title)
	}
	if req.WeeklyCapacityHours != nil {
		patch["weekly_capacity_hours"] = *req.WeeklyCapacityHours
	}
	if req.Status != nil {
		patch["status"] = *req.Status
	}

	if len(patch) > 0 {
		if err := s.data.From("workspace_members").
			Eq("workspace_id", wsID).
			Eq("user_id", targetID).
			Update(r.Context(), patch, nil); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}

	var row memberRow
	if err := s.data.From("workspace_members").
		Select(memberCols).
		Eq("workspace_id", wsID).
		Eq("user_id", targetID).
		Single().
		Get(r.Context(), &row); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	m := row.member()

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

	// The role filter is what protects the owner; deleting them would leave the
	// workspace unadministrable.
	var deleted []struct {
		UserID uuid.UUID `json:"user_id"`
	}
	err = s.data.From("workspace_members").
		Select("user_id").
		Eq("workspace_id", wsID).
		Eq("user_id", targetID).
		Neq("role", "owner").
		Delete(r.Context(), &deleted)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(deleted) == 0 {
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

	var rows []struct {
		models.JoinRequest
		Profile struct {
			Email     string  `json:"email"`
			FullName  *string `json:"full_name"`
			AvatarURL *string `json:"avatar_url"`
		} `json:"profiles"`
	}
	err = s.data.From("workspace_join_requests").
		Select("id,user_id,message,status,created_at,profiles!inner(email,full_name,avatar_url)").
		Eq("workspace_id", wsID).
		Eq("status", "pending").
		Order("created_at", false).
		Get(r.Context(), &rows)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := make([]models.JoinRequest, 0, len(rows))
	for _, row := range rows {
		jr := row.JoinRequest
		jr.Email = row.Profile.Email
		jr.FullName = row.Profile.FullName
		jr.AvatarURL = row.Profile.AvatarURL
		out = append(out, jr)
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

	// Approving writes the membership, updates the request and notifies the
	// applicant — one transaction, so it stays in the RPC.
	if err := s.data.RPC(r.Context(), "decide_join_request", map[string]any{
		"p_decider": actorID,
		"p_request": reqID,
		"p_approve": body.Approve,
		"p_role":    body.Role,
	}, nil); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if body.Approve {
		s.hub.Publish(realtime.Event{Type: "member.joined", WorkspaceID: wsID, ActorID: actorID})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"approved": body.Approve})
}

// ---------------------------------------------------------------- helpers

// ensureProfile guarantees a profiles row exists before anything references it.
// Same RPC as /me, so the conditional merge (an existing full_name is not
// clobbered by the JWT's) is defined in exactly one place.
func (s *Server) ensureProfile(ctx context.Context, user *auth.User) error {
	var p models.Profile
	return s.data.RPCSingle(ctx, "upsert_profile", map[string]any{
		"p_id":     user.ID,
		"p_email":  user.Email,
		"p_name":   user.Name,
		"p_avatar": user.AvatarURL,
	}, &p)
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
