package api

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
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

	rows, err := s.db.Query(r.Context(), `
		select c.id, c.task_id, c.doc_id, c.parent_id, c.body, c.mentions,
		       c.edited_at, c.created_at,
		       p.id, p.email, p.full_name, p.avatar_url, p.presence
		  from comments c
		  join profiles p on p.id = c.author_id
		 where c.task_id = $1 and c.workspace_id = $2
		 order by c.created_at`, taskID, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Comment{}
	for rows.Next() {
		var c models.Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.DocID, &c.ParentID, &c.Body, &c.Mentions,
			&c.EditedAt, &c.CreatedAt,
			&c.Author.ID, &c.Author.Email, &c.Author.FullName, &c.Author.AvatarURL,
			&c.Author.Presence); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"comments": out})
}

type createCommentRequest struct {
	Body     string  `json:"body"`
	ParentID *string `json:"parent_id"`
	// Mentions may be sent explicitly by a rich-text editor; if omitted, they are
	// parsed out of the markdown body.
	Mentions []uuid.UUID `json:"mentions,omitempty"`
}

// mentionRe matches the `@[Display Name](uuid)` form the editor emits.
var mentionRe = regexp.MustCompile(`@\[[^\]]*\]\(([0-9a-fA-F-]{36})\)`)

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
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

	var req createCommentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" {
		httpx.Fail(w, r, httpx.BadRequest("Comment cannot be empty"))
		return
	}
	if len(req.Body) > 20000 {
		httpx.Fail(w, r, httpx.BadRequest("Comment is too long"))
		return
	}

	mentions := req.Mentions
	if len(mentions) == 0 {
		mentions = parseMentions(req.Body)
	}

	var c models.Comment
	err = s.db.QueryRow(r.Context(), `
		with inserted as (
		  insert into comments (workspace_id, task_id, parent_id, author_id, body, mentions)
		  select $1, $2, nullif($3,'')::uuid, $4, $5, $6::uuid[]
		   where exists (select 1 from tasks t where t.id = $2 and t.workspace_id = $1)
		  returning *
		)
		select c.id, c.task_id, c.doc_id, c.parent_id, c.body, c.mentions,
		       c.edited_at, c.created_at,
		       p.id, p.email, p.full_name, p.avatar_url, p.presence
		  from inserted c join profiles p on p.id = c.author_id`,
		wsID, taskID, derefString(req.ParentID), userID, req.Body, mentions,
	).Scan(&c.ID, &c.TaskID, &c.DocID, &c.ParentID, &c.Body, &c.Mentions,
		&c.EditedAt, &c.CreatedAt,
		&c.Author.ID, &c.Author.Email, &c.Author.FullName, &c.Author.AvatarURL, &c.Author.Presence)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	// The DB trigger writes mention notifications; push them live from here.
	for _, m := range c.Mentions {
		if m == userID {
			continue
		}
		s.hub.PublishTo(wsID, m, realtime.Event{
			Type:    "notification.new",
			ActorID: userID,
			Payload: realtime.Marshal(map[string]any{
				"kind": "mention", "task_id": taskID, "comment_id": c.ID,
			}),
		})
	}

	s.hub.Publish(realtime.Event{Type: "comment.created", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(c)})
	httpx.JSON(w, http.StatusCreated, c)
}

func parseMentions(body string) []uuid.UUID {
	matches := mentionRe.FindAllStringSubmatch(body, -1)
	seen := map[uuid.UUID]struct{}{}
	out := []uuid.UUID{}
	for _, m := range matches {
		id, err := uuid.Parse(m[1])
		if err != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ---------------------------------------------------------------- activity

func (s *Server) handleTaskActivity(w http.ResponseWriter, r *http.Request) {
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
	s.writeActivity(w, r, `a.workspace_id = $1 and a.task_id = $2`, wsID, taskID)
}

func (s *Server) handleWorkspaceActivity(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	s.writeActivity(w, r, `a.workspace_id = $1 and ($2::uuid is null or a.project_id = $2)`,
		wsID, optionalUUID(r.URL.Query().Get("project_id")))
}

func (s *Server) writeActivity(w http.ResponseWriter, r *http.Request, where string, args ...any) {
	args = append(args, httpx.QueryInt(r, "limit", 50, 200))
	rows, err := s.db.Query(r.Context(), `
		select a.id, a.task_id, a.verb, a.field, a.old_value, a.new_value, a.created_at,
		       p.id, p.email, p.full_name, p.avatar_url, p.presence
		  from activities a
		  left join profiles p on p.id = a.actor_id
		 where `+where+`
		 order by a.created_at desc
		 limit $`+itoa(len(args)), args...)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Activity{}
	for rows.Next() {
		var (
			a       models.Activity
			id      *uuid.UUID
			email   *string
			name    *string
			avatar  *string
			present *string
		)
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Verb, &a.Field, &a.OldValue, &a.NewValue,
			&a.CreatedAt, &id, &email, &name, &avatar, &present); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		if id != nil {
			a.Actor = &models.Profile{ID: *id, Email: deref(email), FullName: name,
				AvatarURL: avatar, Presence: deref(present)}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"activity": out})
}

// ---------------------------------------------------------------- dependencies

func (s *Server) handleListDependencies(w http.ResponseWriter, r *http.Request) {
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

	// One query for both directions; `direction` tells the UI which list to
	// render it under ("Blocked by" vs "Blocks").
	rows, err := s.db.Query(r.Context(), `
		select d.id, d.source_id, d.target_id, d.kind,
		       other.title, op.key, other.number, other.state,
		       case when d.target_id = $1 then 'incoming' else 'outgoing' end
		  from task_dependencies d
		  join tasks other on other.id = case when d.target_id = $1 then d.source_id else d.target_id end
		  join projects op on op.id = other.project_id
		 where d.workspace_id = $2 and (d.source_id = $1 or d.target_id = $1)
		 order by d.created_at`, taskID, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	type dep struct {
		models.Dependency
		Direction string `json:"direction"`
	}

	out := []dep{}
	for rows.Next() {
		var (
			d      dep
			key    string
			number int
		)
		if err := rows.Scan(&d.ID, &d.SourceID, &d.TargetID, &d.Kind,
			&d.Title, &key, &number, &d.State, &d.Direction); err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		d.Ref = taskRef(key, number)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"dependencies": out})
}

type createDependencyRequest struct {
	// OtherID is the task on the far end of the edge.
	OtherID string `json:"other_id"`
	// Direction "incoming" means the other task blocks this one.
	Direction string `json:"direction"`
	Kind      string `json:"kind,omitempty"`
}

func (s *Server) handleCreateDependency(w http.ResponseWriter, r *http.Request) {
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

	var req createDependencyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	otherID, err := httpx.UUIDParam(req.OtherID, "other_id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if otherID == taskID {
		httpx.Fail(w, r, httpx.BadRequest("A task cannot depend on itself"))
		return
	}
	if req.Kind == "" {
		req.Kind = "blocks"
	}
	switch req.Kind {
	case "blocks", "relates_to", "duplicates":
	default:
		httpx.Fail(w, r, httpx.BadRequest("kind must be blocks, relates_to, or duplicates"))
		return
	}

	source, target := taskID, otherID
	if req.Direction == "incoming" {
		source, target = otherID, taskID
	}

	var id uuid.UUID
	err = s.db.QueryRow(r.Context(), `
		insert into task_dependencies (workspace_id, source_id, target_id, kind)
		select $1, $2, $3, $4::dependency_kind
		 where (select count(*) from tasks t
		         where t.id in ($2, $3) and t.workspace_id = $1) = 2
		returning id`, wsID, source, target, req.Kind).Scan(&id)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	s.hub.Publish(realtime.Event{Type: "dependency.created", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(map[string]any{
			"id": id, "source_id": source, "target_id": target, "kind": req.Kind})})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": id, "source_id": source, "target_id": target, "kind": req.Kind})
}

func (s *Server) handleDeleteDependency(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	depID, err := httpx.UUIDParam(chi.URLParam(r, "depID"), "depID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	tag, err := s.db.Exec(r.Context(),
		`delete from task_dependencies where id = $1 and workspace_id = $2`, depID, wsID)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(w, r, httpx.ErrNotFound)
		return
	}

	s.hub.Publish(realtime.Event{Type: "dependency.deleted", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(map[string]any{"id": depID})})
	httpx.JSON(w, http.StatusNoContent, nil)
}
