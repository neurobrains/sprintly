package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

// pgxTx is the subset of pgx.Tx the handlers use, named locally so the query
// helpers do not have to import pgx everywhere.
type pgxTx = pgx.Tx

func itoa(n int) string { return strconv.Itoa(n) }

func parseLabels(raw string) []models.Label {
	out := []models.Label{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []models.Label{}
	}
	return out
}

// ---------------------------------------------------------------- teams

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		select t.id, t.workspace_id, t.name, t.description, t.color,
		       (select count(*) from team_members m where m.team_id = t.id)
		  from teams t where t.workspace_id = $1 order by t.name`, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Team{}
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.Name, &t.Description,
			&t.Color, &t.MemberCount); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"teams": out})
}

type createTeamRequest struct {
	Name        string      `json:"name"`
	Description *string     `json:"description"`
	Color       string      `json:"color,omitempty"`
	MemberIDs   []uuid.UUID `json:"member_ids,omitempty"`
}

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

	var t models.Team
	err = s.db.InTx(r.Context(), func(tx pgxTx) error {
		if err := tx.QueryRow(r.Context(), `
			insert into teams (workspace_id, name, description, color)
			values ($1, $2, nullif($3,''), $4)
			returning id, workspace_id, name, description, color`,
			wsID, req.Name, derefString(req.Description), req.Color,
		).Scan(&t.ID, &t.WorkspaceID, &t.Name, &t.Description, &t.Color); err != nil {
			return err
		}

		// Only actual workspace members may be added.
		_, err := tx.Exec(r.Context(), `
			insert into team_members (team_id, user_id, is_lead)
			select $1, m.user_id, m.user_id = $3
			  from workspace_members m
			 where m.workspace_id = $4 and m.status = 'active' and m.user_id = any($2::uuid[])
			on conflict do nothing`, t.ID, req.MemberIDs, userID, wsID)
		return err
	})
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	t.MemberCount = len(req.MemberIDs)
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

	rows, err := s.db.Query(r.Context(),
		`select id, name, color from labels where workspace_id = $1 order by name`, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Label{}
	for rows.Next() {
		var l models.Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
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
	if err := s.db.QueryRow(r.Context(), `
		insert into labels (workspace_id, name, color) values ($1, $2, $3)
		returning id, name, color`, wsID, req.Name, req.Color,
	).Scan(&l.ID, &l.Name, &l.Color); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusCreated, l)
}

// ---------------------------------------------------------------- notifications

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	unreadOnly := r.URL.Query().Get("unread") == "true"
	rows, err := s.db.Query(r.Context(), `
		select n.id, n.kind, n.title, n.body, n.task_id, n.url, n.read_at, n.created_at,
		       p.id, p.email, p.full_name, p.avatar_url, p.presence
		  from notifications n
		  left join profiles p on p.id = n.actor_id
		 where n.workspace_id = $1 and n.user_id = $2
		   and (not $3 or n.read_at is null)
		 order by n.created_at desc
		 limit $4`, wsID, userID, unreadOnly, httpx.QueryInt(r, "limit", 50, 200))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Notification{}
	for rows.Next() {
		var (
			n      models.Notification
			id     *uuid.UUID
			email  *string
			name   *string
			avatar *string
			pres   *string
		)
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.TaskID, &n.URL,
			&n.ReadAt, &n.CreatedAt, &id, &email, &name, &avatar, &pres); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		if id != nil {
			n.Actor = &models.Profile{ID: *id, Email: deref(email), FullName: name,
				AvatarURL: avatar, Presence: deref(pres)}
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	var unread int
	if err := s.db.QueryRow(r.Context(),
		`select count(*) from notifications
		  where workspace_id = $1 and user_id = $2 and read_at is null`,
		wsID, userID).Scan(&unread); err != nil {
		httpx.Fail(w, r, db.MapError(err))
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

	tag, err := s.db.Exec(r.Context(), `
		update notifications set read_at = now()
		 where workspace_id = $1 and user_id = $2 and read_at is null
		   and (cardinality($3::bigint[]) = 0 or id = any($3::bigint[]))`,
		wsID, userID, req.IDs)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"marked": tag.RowsAffected()})
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
