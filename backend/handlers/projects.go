package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/httpx"
	"github.com/sprintly/sprintly/backend/models"
	"github.com/sprintly/sprintly/backend/realtime"
)

// projectCols is the projection behind models.Project's own columns. The
// counters come from the project_progress view, which is fetched separately —
// PostgREST can only embed across a foreign key, and a view has none.
const projectCols = "id,workspace_id,team_id,name,key,description,color,icon," +
	"start_date,target_date,archived_at,created_at"

type projectProgress struct {
	ProjectID       uuid.UUID `json:"project_id"`
	TotalTasks      int       `json:"total_tasks"`
	DoneTasks       int       `json:"done_tasks"`
	OverdueTasks    int       `json:"overdue_tasks"`
	PercentComplete *float64  `json:"percent_complete"`
}

// attachProgress fills the counters on projects in one extra round trip,
// regardless of how many projects there are.
func (s *Server) attachProgress(ctx context.Context, projects []models.Project) error {
	if len(projects) == 0 {
		return nil
	}
	ids := make([]string, len(projects))
	for i, p := range projects {
		ids[i] = p.ID.String()
	}

	var rows []projectProgress
	if err := s.data.From("project_progress").
		Select("project_id,total_tasks,done_tasks,overdue_tasks,percent_complete").
		In("project_id", ids).
		Get(ctx, &rows); err != nil {
		return err
	}

	byID := make(map[uuid.UUID]projectProgress, len(rows))
	for _, row := range rows {
		byID[row.ProjectID] = row
	}
	for i := range projects {
		if pp, ok := byID[projects[i].ID]; ok {
			projects[i].TotalTasks = pp.TotalTasks
			projects[i].DoneTasks = pp.DoneTasks
			projects[i].OverdueTasks = pp.OverdueTasks
			projects[i].PercentComplete = pp.PercentComplete
		}
	}
	return nil
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	q := s.data.From("projects").
		Select(projectCols).
		Eq("workspace_id", wsID).
		Order("archived_at", false).
		Order("created_at", false)

	if r.URL.Query().Get("archived") != "true" {
		q = q.IsNull("archived_at", true)
	}

	out := []models.Project{}
	if err := q.Get(r.Context(), &out); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := s.attachProgress(r.Context(), out); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"projects": out})
}

// getProject reads one project scoped to the workspace. The workspace_id filter
// is what stops a project UUID from another tenant being readable.
func (s *Server) getProject(ctx context.Context, projectID, wsID uuid.UUID) (models.Project, error) {
	var p models.Project
	if err := s.data.From("projects").
		Select(projectCols).
		Eq("id", projectID).
		Eq("workspace_id", wsID).
		Single().
		Get(ctx, &p); err != nil {
		return p, err
	}
	one := []models.Project{p}
	if err := s.attachProgress(ctx, one); err != nil {
		return p, err
	}
	return one[0], nil
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	projectID, err := httpx.UUIDParam(chi.URLParam(r, "projectID"), "projectID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	p, err := s.getProject(r.Context(), projectID, wsID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

type createProjectRequest struct {
	Name        string  `json:"name"`
	Key         string  `json:"key"`
	Description *string `json:"description"`
	Color       string  `json:"color,omitempty"`
	Icon        *string `json:"icon"`
	TeamID      *string `json:"team_id"`
	StartDate   *string `json:"start_date"`
	TargetDate  *string `json:"target_date"`
}

var projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,7}$`)

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req createProjectRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpx.Fail(w, r, httpx.BadRequest("Project name is required"))
		return
	}

	req.Key = strings.ToUpper(strings.TrimSpace(req.Key))
	if req.Key == "" {
		req.Key = deriveProjectKey(req.Name)
	}
	if !projectKeyRe.MatchString(req.Key) {
		httpx.Fail(w, r, httpx.BadRequest("Project key must be 2-8 uppercase letters or digits, starting with a letter"))
		return
	}
	if req.Color == "" {
		req.Color = "#6366f1"
	}

	body := map[string]any{
		"workspace_id": wsID,
		"name":         req.Name,
		"key":          req.Key,
		"color":        req.Color,
		"created_by":   userID,
		"team_id":      nilIfEmpty(derefString(req.TeamID)),
		"description":  req.Description,
		"icon":         req.Icon,
		"start_date":   nilIfEmpty(derefString(req.StartDate)),
		"target_date":  nilIfEmpty(derefString(req.TargetDate)),
	}

	var p models.Project
	if err := s.data.From("projects").
		Select(projectCols).
		Single().
		Insert(r.Context(), body, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	// A brand new project has no tasks, so the counters are zero by definition.

	s.hub.Publish(realtime.Event{Type: "project.created", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(p)})
	httpx.JSON(w, http.StatusCreated, p)
}

type updateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
	Icon        *string `json:"icon"`
	TeamID      *string `json:"team_id"`
	StartDate   *string `json:"start_date"`
	TargetDate  *string `json:"target_date"`
	Archived    *bool   `json:"archived"`
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	userID, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	projectID, err := httpx.UUIDParam(chi.URLParam(r, "projectID"), "projectID")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var req updateProjectRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	patch := map[string]any{}
	if req.Name != nil {
		patch["name"] = *req.Name
	}
	if req.Color != nil {
		patch["color"] = *req.Color
	}
	if req.Description != nil {
		patch["description"] = nilIfEmpty(*req.Description)
	}
	if req.Icon != nil {
		patch["icon"] = nilIfEmpty(*req.Icon)
	}
	if req.TeamID != nil {
		patch["team_id"] = nilIfEmpty(strings.TrimSpace(*req.TeamID))
	}
	if req.StartDate != nil {
		patch["start_date"] = nilIfEmpty(strings.TrimSpace(*req.StartDate))
	}
	if req.TargetDate != nil {
		patch["target_date"] = nilIfEmpty(strings.TrimSpace(*req.TargetDate))
	}
	if req.Archived != nil {
		// Archiving twice keeps the original timestamp; unarchiving clears it.
		if *req.Archived {
			patch["archived_at"] = time.Now().UTC()
		} else {
			patch["archived_at"] = nil
		}
	}

	if len(patch) > 0 {
		if err := s.data.From("projects").
			Eq("id", projectID).
			Eq("workspace_id", wsID).
			Update(r.Context(), patch, nil); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}

	p, err := s.getProject(r.Context(), projectID, wsID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	s.hub.Publish(realtime.Event{Type: "project.updated", WorkspaceID: wsID,
		ActorID: userID, Payload: realtime.Marshal(p)})
	httpx.JSON(w, http.StatusOK, p)
}

// deriveProjectKey turns "Mobile App Redesign" into "MAR", and a single word
// like "Platform" into "PLA".
func deriveProjectKey(name string) string {
	key := deriveProjectKeyRaw(name)
	if !projectKeyRe.MatchString(key) {
		return "PRJ" // e.g. a name that is all digits or non-Latin script
	}
	return key
}

func deriveProjectKeyRaw(name string) string {
	words := strings.Fields(strings.ToUpper(name))
	letters := regexp.MustCompile(`[^A-Z0-9]`)

	if len(words) >= 2 {
		var b strings.Builder
		for _, word := range words {
			clean := letters.ReplaceAllString(word, "")
			if clean == "" {
				continue
			}
			b.WriteByte(clean[0])
			if b.Len() == 4 {
				break
			}
		}
		if b.Len() >= 2 {
			return b.String()
		}
	}

	clean := letters.ReplaceAllString(strings.ToUpper(name), "")
	if len(clean) >= 3 {
		return clean[:3]
	}
	if len(clean) >= 2 {
		return clean
	}
	return "PRJ"
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}
