package handlers

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/httpx"
	"github.com/sprintly/sprintly/backend/models"
	"github.com/sprintly/sprintly/backend/realtime"
)

func itoa(n int) string { return strconv.Itoa(n) }

// nowRFC3339 is what goes into a JSON patch body where SQL would have said
// now(). PostgREST takes an ISO timestamp for a timestamptz column.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// embeddedCount decodes PostgREST's aggregate embedding, which comes back as
// `"team_members": [{"count": 3}]` — an array even though it holds one row.
type embeddedCount []struct {
	Count int `json:"count"`
}

func (e embeddedCount) value() int {
	if len(e) == 0 {
		return 0
	}
	return e[0].Count
}

// ---------------------------------------------------------------- teams

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var rows []struct {
		models.Team
		Members embeddedCount `json:"team_members"`
	}
	err = s.data.From("teams").
		Select("id,workspace_id,name,description,color,team_members(count)").
		Eq("workspace_id", wsID).
		Order("name", false).
		Get(r.Context(), &rows)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := make([]models.Team, 0, len(rows))
	for _, row := range rows {
		t := row.Team
		t.MemberCount = row.Members.value()
		out = append(out, t)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"teams": out})
}

type createTeamRequest struct {
	Name        string      `json:"name"`
	Description *string     `json:"description"`
	Color       string      `json:"color,omitempty"`
	MemberIDs   []uuid.UUID `json:"member_ids,omitempty"`
}

// handleCreateTeam goes through an RPC because it is two writes that must not
// half-apply, and because the member insert is a SELECT-driven INSERT that
// filters the requested ids down to actual active workspace members — neither
// of which PostgREST can express.
func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req createTeamRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpx.Fail(w, r, httpx.BadRequest("Team name is required"))
		return
	}
	if req.Color == "" {
		req.Color = "#6366f1"
	}

	// The creator is always in their own team.
	if !slices.Contains(req.MemberIDs, userID) {
		req.MemberIDs = append(req.MemberIDs, userID)
	}

	ids := make([]string, len(req.MemberIDs))
	for i, id := range req.MemberIDs {
		ids[i] = id.String()
	}

	var t models.Team
	err = s.data.RPCSingle(r.Context(), "create_team", map[string]any{
		"p_workspace":   wsID,
		"p_name":        req.Name,
		"p_description": nilIfEmpty(derefString(req.Description)),
		"p_color":       req.Color,
		"p_lead":        userID,
		"p_members":     ids,
	}, &t)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	s.hub.Publish(realtime.Event{Type: "team.created", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(t)})
	httpx.JSON(w, http.StatusCreated, t)
}

// ---------------------------------------------------------------- labels

func (s *Server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := []models.Label{}
	err = s.data.From("labels").
		Select("id,name,color").
		Eq("workspace_id", wsID).
		Order("name", false).
		Get(r.Context(), &out)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"labels": out})
}

type createLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

func (s *Server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req createLabelRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpx.Fail(w, r, httpx.BadRequest("Label name is required"))
		return
	}
	if req.Color == "" {
		req.Color = "#64748b"
	}

	var l models.Label
	err = s.data.From("labels").
		Select("id,name,color").
		Single().
		Insert(r.Context(), map[string]any{
			"workspace_id": wsID, "name": req.Name, "color": req.Color,
		}, &l)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, l)
}

// ---------------------------------------------------------------- notifications

// notificationRow is a notification plus its embedded actor profile.
type notificationRow struct {
	models.Notification
	ActorProfile *models.Profile `json:"profiles"`
}

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	q := s.data.From("notifications").
		Select("id,kind,title,body,task_id,url,read_at,created_at,"+
			"profiles!notifications_actor_id_fkey(id,email,full_name,avatar_url,presence)").
		Eq("workspace_id", wsID).
		Eq("user_id", userID).
		Order("created_at", true).
		Limit(httpx.QueryInt(r, "limit", 50, 200))

	if r.URL.Query().Get("unread") == "true" {
		q = q.IsNull("read_at", true)
	}

	var rows []notificationRow
	if err := q.Get(r.Context(), &rows); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := make([]models.Notification, 0, len(rows))
	for _, row := range rows {
		n := row.Notification
		n.Actor = row.ActorProfile
		out = append(out, n)
	}

	unread, err := s.data.From("notifications").
		Eq("workspace_id", wsID).
		Eq("user_id", userID).
		IsNull("read_at", true).
		Count(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"notifications": out, "unread_count": unread,
	})
}

type markReadRequest struct {
	// Empty IDs marks everything in the workspace read.
	IDs []int64 `json:"ids,omitempty"`
}

func (s *Server) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req markReadRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	q := s.data.From("notifications").
		Select("id").
		Eq("workspace_id", wsID).
		Eq("user_id", userID).
		IsNull("read_at", true)

	if len(req.IDs) > 0 {
		ids := make([]string, len(req.IDs))
		for i, id := range req.IDs {
			ids[i] = strconv.FormatInt(id, 10)
		}
		q = q.In("id", ids)
	}

	// return=representation gives back the affected rows, which is how the count
	// is obtained without a command tag.
	var marked []struct {
		ID int64 `json:"id"`
	}
	if err := q.Update(r.Context(), map[string]any{"read_at": nowRFC3339()}, &marked); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"marked": len(marked)})
}

// ---------------------------------------------------------------- websocket

// handleWebSocket upgrades to the workspace event stream. Auth already ran in
// the middleware chain — the browser WebSocket API cannot set an Authorization
// header, so the token arrives as ?access_token=.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  originPatterns(s.cfg.AllowedOrigins),
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		return // Accept already wrote the failure response
	}
	defer conn.CloseNow()

	// Detach from the router's 30s request timeout — this connection is
	// long-lived. Teardown is driven by the socket itself: when the client goes
	// away the read loop errors out and Serve returns.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()

	s.hub.Serve(ctx, conn, userID, wsID)
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// originPatterns strips the scheme from allowed origins, which is the form
// websocket.AcceptOptions expects.
func originPatterns(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://")
		if o = strings.TrimSuffix(o, "/"); o != "" {
			out = append(out, o)
		}
	}
	return out
}
