package api

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/httpx"
	"github.com/sprintly/sprintly/backend/internal/models"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

const projectSelect = `
	select p.id, p.workspace_id, p.team_id, p.name, p.key, p.description, p.color, p.icon,
	       p.start_date, p.target_date, p.archived_at, p.created_at,
	       coalesce(pp.total_tasks, 0), coalesce(pp.done_tasks, 0),
	       coalesce(pp.overdue_tasks, 0), pp.percent_complete
	  from projects p
	  left join project_progress pp on pp.project_id = p.id`

func scanProject(row interface{ Scan(...any) error }) (models.Project, error) {
	var p models.Project
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.TeamID, &p.Name, &p.Key, &p.Description,
		&p.Color, &p.Icon, &p.StartDate, &p.TargetDate, &p.ArchivedAt, &p.CreatedAt,
		&p.TotalTasks, &p.DoneTasks, &p.OverdueTasks, &p.PercentComplete)
	return p, err
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	_, wsID, _, err := s.ctxIDs(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	includeArchived := r.URL.Query().Get("archived") == "true"
	rows, err := s.db.Query(r.Context(), projectSelect+`
		 where p.workspace_id = $1 and ($2 or p.archived_at is null)
		 order by p.archived_at nulls first, p.created_at`, wsID, includeArchived)
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}
	defer rows.Close()

	out := []models.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			httpx.Fail(w, r, db.MapError(err))
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"projects": out})
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

	p, err := scanProject(s.db.QueryRow(r.Context(),
		projectSelect+` where p.id = $1 and p.workspace_id = $2`, projectID, wsID))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
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

	p, err := scanProject(s.db.QueryRow(r.Context(), `
		with created as (
		  insert into projects (workspace_id, team_id, name, key, description, color, icon,
		                        start_date, target_date, created_by)
		  values ($1, nullif($2,'')::uuid, $3, $4, $5, $6, $7,
		          nullif($8,'')::date, nullif($9,'')::date, $10)
		  returning *
		)
		select p.id, p.workspace_id, p.team_id, p.name, p.key, p.description, p.color, p.icon,
		       p.start_date, p.target_date, p.archived_at, p.created_at, 0, 0, 0, null::numeric
		  from created p`,
		wsID, derefString(req.TeamID), req.Name, req.Key, req.Description, req.Color, req.Icon,
		derefString(req.StartDate), derefString(req.TargetDate), userID))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

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

	if _, err := s.db.Exec(r.Context(), `
		update projects set
		  name        = coalesce($3, name),
		  description = case when $4::text is null then description else nullif($4,'') end,
		  color       = coalesce($5, color),
		  icon        = case when $6::text is null then icon else nullif($6,'') end,
		  team_id     = case when $7::text is null then team_id else nullif($7,'')::uuid end,
		  start_date  = case when $8::text is null then start_date else nullif($8,'')::date end,
		  target_date = case when $9::text is null then target_date else nullif($9,'')::date end,
		  archived_at = case when $10::boolean is null then archived_at
		                     when $10 then coalesce(archived_at, now()) else null end
		where id = $1 and workspace_id = $2`,
		projectID, wsID, req.Name, req.Description, req.Color, req.Icon,
		req.TeamID, req.StartDate, req.TargetDate, req.Archived); err != nil {
		httpx.Fail(w, r, db.MapError(err))
		return
	}

	p, err := scanProject(s.db.QueryRow(r.Context(),
		projectSelect+` where p.id = $1 and p.workspace_id = $2`, projectID, wsID))
	if err != nil {
		httpx.Fail(w, r, db.MapError(err))
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
