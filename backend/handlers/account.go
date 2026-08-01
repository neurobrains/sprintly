package handlers

import (
	"net/http"
	"time"

	"github.com/sprintly/sprintly/backend/httpx"
	"github.com/sprintly/sprintly/backend/middleware"
	"github.com/sprintly/sprintly/backend/models"
)

// handleMe returns the caller's profile, upserting it from the JWT first.
//
// The middleware.users trigger normally creates the row, but a project restored from a
// dump or a user created before the trigger existed would have none. Doing it
// here makes /me the reliable "ensure I exist" call the frontend makes on boot.
//
// It goes through an RPC rather than a PostgREST upsert because the merge is
// conditional — an existing full_name wins over the one in the JWT, so a user who
// renamed themselves is not overwritten on every sign-in. `resolution=merge-duplicates`
// has no way to say that.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := middleware.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var p models.Profile
	err = s.data.RPCSingle(r.Context(), "upsert_profile", map[string]any{
		"p_id":     user.ID,
		"p_email":  user.Email,
		"p_name":   user.Name,
		"p_avatar": user.AvatarURL,
	}, &p)
	if err != nil {
		httpx.Fail(w, r, err)
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
	user, err := middleware.UserFrom(r.Context())
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

	// Only the keys the client actually sent are written, so PATCH keeps its
	// partial-update semantics instead of nulling everything omitted.
	patch := map[string]any{"last_seen_at": time.Now().UTC(), "updated_at": time.Now().UTC()}
	if req.FullName != nil {
		patch["full_name"] = *req.FullName
	}
	if req.Timezone != nil {
		patch["timezone"] = *req.Timezone
	}
	if req.Presence != nil {
		patch["presence"] = *req.Presence
	}
	if req.PresenceNote != nil {
		patch["presence_note"] = nilIfEmpty(*req.PresenceNote)
	}

	var p models.Profile
	err = s.data.From("profiles").
		Select(profileCols).
		Eq("id", user.ID).
		Single().
		Update(r.Context(), patch, &p)
	if err != nil {
		httpx.Fail(w, r, err)
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

// profileCols is the projection behind models.Profile. Named once so an embedded
// profile and a top-level one cannot drift apart.
const profileCols = "id,email,full_name,avatar_url,timezone,presence,presence_note,last_seen_at"

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// handleListMyWorkspaces drives the post-login routing decision: no workspaces
// means send the user to onboarding, one means jump straight into it.
func (s *Server) handleListMyWorkspaces(w http.ResponseWriter, r *http.Request) {
	user, err := middleware.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	list := []models.Workspace{}
	if err := s.data.RPC(r.Context(), "my_workspaces",
		map[string]any{"p_user": user.ID}, &list); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// Pending join requests let the UI show "waiting for approval" instead of
	// dumping the user back onto an empty onboarding screen.
	type pending struct {
		Name      string    `json:"name"`
		Slug      string    `json:"slug"`
		CreatedAt time.Time `json:"created_at"`
	}
	var raw []struct {
		CreatedAt time.Time `json:"created_at"`
		Workspace struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"workspaces"`
	}
	err = s.data.From("workspace_join_requests").
		Select("created_at,workspaces!inner(name,slug)").
		Eq("user_id", user.ID).
		Eq("status", "pending").
		Order("created_at", true).
		Get(r.Context(), &raw)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	pendingList := make([]pending, 0, len(raw))
	for _, p := range raw {
		pendingList = append(pendingList, pending{
			Name: p.Workspace.Name, Slug: p.Workspace.Slug, CreatedAt: p.CreatedAt,
		})
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"workspaces":       list,
		"pending_requests": pendingList,
	})
}

// handleMyTasks is the cross-workspace "My Work" inbox. The ordering (due date
// first, then priority rank, then age) and the membership check are one query in
// SQL and several round trips in PostgREST, so it stays an RPC.
func (s *Server) handleMyTasks(w http.ResponseWriter, r *http.Request) {
	user, err := middleware.UserFrom(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	type myTask struct {
		models.Task
		WorkspaceSlug string `json:"workspace_slug"`
		WorkspaceName string `json:"workspace_name"`
	}

	// The RPC's output columns are named to match models.Task's JSON tags, so
	// this decodes straight through.
	out := []myTask{}
	err = s.data.RPC(r.Context(), "my_tasks", map[string]any{
		"p_user":  user.ID,
		"p_limit": httpx.QueryInt(r, "limit", 100, 300),
	}, &out)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	for i := range out {
		out[i].Ref = taskRef(out[i].ProjectKey, out[i].Number)
		out[i].Labels = []models.Label{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": out})
}
