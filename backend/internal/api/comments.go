package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

// commentCols embeds the author through the author_id foreign key. The
// constraint name is spelled out because comments has more than one FK to
// profiles-adjacent tables, and PostgREST needs the disambiguation.
const commentCols = "id,task_id,doc_id,parent_id,body,mentions,edited_at,created_at," +
	"profiles!comments_author_id_fkey(" + profileCols + ")"

type commentRow struct {
	models.Comment
	AuthorProfile *models.Profile `json:"profiles"`
}

func (r commentRow) comment() models.Comment {
	c := r.Comment
	if r.AuthorProfile != nil {
		c.Author = *r.AuthorProfile
	}
	if c.Mentions == nil {
		c.Mentions = []uuid.UUID{}
	}
	return c
}

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

	var rows []commentRow
	err = s.data.From("comments").
		Select(commentCols).
		Eq("task_id", taskID).
		Eq("workspace_id", wsID).
		Order("created_at", false).
		Get(r.Context(), &rows)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := make([]models.Comment, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.comment())
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

// assertTaskInWorkspace is the guard the old `where exists (...)` sub-select
// provided. It has to be its own round trip now, so every caller that writes a
// row keyed by task_id must run it first — otherwise a task UUID from another
// tenant would be accepted.
func (s *Server) assertTaskInWorkspace(ctx context.Context, taskID, wsID uuid.UUID) error {
	var t struct {
		ID uuid.UUID `json:"id"`
	}
	return s.data.From("tasks").
		Select("id").
		Eq("id", taskID).
		Eq("workspace_id", wsID).
		Single().
		Get(ctx, &t)
}

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

	if err := s.assertTaskInWorkspace(r.Context(), taskID, wsID); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	mentions := req.Mentions
	if len(mentions) == 0 {
		mentions = parseMentions(req.Body)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	err = s.data.From("comments").
		Select("id").
		Single().
		Insert(r.Context(), map[string]any{
			"workspace_id": wsID,
			"task_id":      taskID,
			"parent_id":    nilIfEmpty(derefString(req.ParentID)),
			"author_id":    userID,
			"body":         req.Body,
			"mentions":     mentions,
		}, &created)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// Re-read for the embedded author — PostgREST does not return embedded
	// resources in an insert's representation.
	var row commentRow
	if err := s.data.From("comments").
		Select(commentCols).
		Eq("id", created.ID).
		Single().
		Get(r.Context(), &row); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	c := row.comment()

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

type activityRow struct {
	models.Activity
	ActorProfile *models.Profile `json:"profiles"`
}

const activityCols = "id,task_id,verb,field,old_value,new_value,created_at," +
	"profiles!activities_actor_id_fkey(" + profileCols + ")"

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

	s.writeActivity(w, r, s.data.From("activities").
		Eq("workspace_id", wsID).
		Eq("task_id", taskID))
}

func (s *Server) handleWorkspaceActivity(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	q := s.data.From("activities").Eq("workspace_id", wsID)
	if projectID := optionalUUID(r.URL.Query().Get("project_id")); projectID != nil {
		q = q.Eq("project_id", *projectID)
	}
	s.writeActivity(w, r, q)
}

func (s *Server) writeActivity(w http.ResponseWriter, r *http.Request, q *supaQuery) {
	var rows []activityRow
	err := q.Select(activityCols).
		Order("created_at", true).
		Limit(httpx.QueryInt(r, "limit", 50, 200)).
		Get(r.Context(), &rows)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out := make([]models.Activity, 0, len(rows))
	for _, row := range rows {
		a := row.Activity
		a.Actor = row.ActorProfile
		out = append(out, a)
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

	// Both directions in one result, with `direction` telling the UI which list
	// to render it under ("Blocked by" vs "Blocks"). The join target depends on
	// which end of the edge the task is on, which is a CASE — so it stays SQL.
	type dep struct {
		models.Dependency
		ProjectKey string `json:"project_key"`
		Number     int    `json:"number"`
		Direction  string `json:"direction"`
	}

	out := []dep{}
	err = s.data.RPC(r.Context(), "task_dependency_list", map[string]any{
		"p_task":      taskID,
		"p_workspace": wsID,
	}, &out)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	for i := range out {
		out[i].Ref = taskRef(out[i].ProjectKey, out[i].Number)
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

	// Both ends must live in this workspace. The old query enforced it with a
	// count sub-select; here it is an explicit read before the write.
	var found []struct {
		ID uuid.UUID `json:"id"`
	}
	err = s.data.From("tasks").
		Select("id").
		In("id", []string{source.String(), target.String()}).
		Eq("workspace_id", wsID).
		Get(r.Context(), &found)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(found) != 2 {
		httpx.Fail(w, r, httpx.ErrNotFound)
		return
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	// A cycle trips the deps_guard_cycle trigger, which raises check_violation
	// and surfaces as a 400 through the error mapping.
	err = s.data.From("task_dependencies").
		Select("id").
		Single().
		Insert(r.Context(), map[string]any{
			"workspace_id": wsID, "source_id": source, "target_id": target, "kind": req.Kind,
		}, &created)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	payload := map[string]any{
		"id": created.ID, "source_id": source, "target_id": target, "kind": req.Kind,
	}
	s.hub.Publish(realtime.Event{Type: "dependency.created", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(payload)})
	httpx.JSON(w, http.StatusCreated, payload)
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

	var deleted []struct {
		ID uuid.UUID `json:"id"`
	}
	err = s.data.From("task_dependencies").
		Select("id").
		Eq("id", depID).
		Eq("workspace_id", wsID).
		Delete(r.Context(), &deleted)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(deleted) == 0 {
		httpx.Fail(w, r, httpx.ErrNotFound)
		return
	}

	s.hub.Publish(realtime.Event{Type: "dependency.deleted", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(map[string]any{"id": depID})})
	httpx.JSON(w, http.StatusNoContent, nil)
}
