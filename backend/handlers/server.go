// Package handlers wires the HTTP surface: routing, middleware, and handlers.
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/config"
	"github.com/sprintly/sprintly/backend/db"
	"github.com/sprintly/sprintly/backend/httpx"
	"github.com/sprintly/sprintly/backend/middleware"
	"github.com/sprintly/sprintly/backend/realtime"
)

type Server struct {
	cfg      *config.Config
	data     *db.Client
	verifier *middleware.Verifier
	hub      *realtime.Hub
}

// dbQuery lets handlers pass a partly-built query around without every file
// importing the db package.
type dbQuery = db.Query

func NewServer(cfg *config.Config, data *db.Client, hub *realtime.Hub) *Server {
	return &Server{cfg: cfg, data: data, verifier: middleware.NewVerifier(cfg), hub: hub}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(requestLogger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", s.health)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.verifier.Required)

		// ------------------------------------------------ account scope
		r.Get("/me", s.handleMe)
		r.Patch("/me", s.handleUpdateMe)
		r.Get("/me/workspaces", s.handleListMyWorkspaces)
		r.Get("/me/tasks", s.handleMyTasks)

		// Create or join — the two paths out of onboarding.
		r.Post("/workspaces", s.handleCreateWorkspace)
		r.Post("/workspaces/join", s.handleJoinWorkspace)
		r.Get("/workspaces/lookup", s.handleLookupWorkspace)

		// ------------------------------------------------ workspace scope
		r.Route("/workspaces/{workspaceID}", func(r chi.Router) {
			r.Use(s.requireWorkspace)

			r.Get("/", s.handleGetWorkspace)
			r.With(s.requireRole("admin")).Patch("/", s.handleUpdateWorkspace)
			r.With(s.requireRole("admin")).Post("/rotate-join-code", s.handleRotateJoinCode)

			r.Get("/members", s.handleListMembers)
			r.With(s.requireRole("manager")).Patch("/members/{userID}", s.handleUpdateMember)
			r.With(s.requireRole("admin")).Delete("/members/{userID}", s.handleRemoveMember)

			r.With(s.requireRole("manager")).Get("/join-requests", s.handleListJoinRequests)
			r.With(s.requireRole("manager")).Post("/join-requests/{requestID}", s.handleDecideJoinRequest)

			r.Get("/teams", s.handleListTeams)
			r.With(s.requireRole("manager")).Post("/teams", s.handleCreateTeam)

			r.Get("/labels", s.handleListLabels)
			r.With(s.denyGuest).Post("/labels", s.handleCreateLabel)

			r.Get("/projects", s.handleListProjects)
			r.With(s.requireRole("manager")).Post("/projects", s.handleCreateProject)
			r.Get("/projects/{projectID}", s.handleGetProject)
			r.With(s.requireRole("manager")).Patch("/projects/{projectID}", s.handleUpdateProject)

			r.Get("/tasks", s.handleListTasks)
			r.With(s.denyGuest).Post("/tasks", s.handleCreateTask)
			r.Get("/tasks/{taskID}", s.handleGetTask)
			r.With(s.denyGuest).Patch("/tasks/{taskID}", s.handleUpdateTask)
			r.With(s.denyGuest).Post("/tasks/{taskID}/move", s.handleMoveTask)
			r.With(s.denyGuest).Delete("/tasks/{taskID}", s.handleDeleteTask)

			r.Get("/tasks/{taskID}/comments", s.handleListComments)
			r.With(s.denyGuest).Post("/tasks/{taskID}/comments", s.handleCreateComment)
			r.Get("/tasks/{taskID}/activity", s.handleTaskActivity)
			r.Get("/tasks/{taskID}/dependencies", s.handleListDependencies)
			r.With(s.denyGuest).Post("/tasks/{taskID}/dependencies", s.handleCreateDependency)
			r.With(s.denyGuest).Delete("/dependencies/{depID}", s.handleDeleteDependency)

			r.Get("/time-entries", s.handleListTimeEntries)
			r.With(s.denyGuest).Post("/time-entries", s.handleLogTime)
			r.With(s.denyGuest).Post("/timer/start", s.handleStartTimer)
			r.With(s.denyGuest).Post("/timer/stop", s.handleStopTimer)
			r.Get("/timer/active", s.handleActiveTimer)

			r.Get("/workload", s.handleWorkload)
			r.Get("/activity", s.handleWorkspaceActivity)
			r.Get("/notifications", s.handleListNotifications)
			r.Post("/notifications/read", s.handleMarkNotificationsRead)

			r.Get("/events", s.handleWebSocket)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.ErrNotFound)
	})
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	status := "ok"
	code := http.StatusOK
	if err := s.data.Ping(ctx); err != nil {
		status, code = "degraded", http.StatusServiceUnavailable
	}
	httpx.JSON(w, code, map[string]any{
		"status":  status,
		"service": "sprintly-api",
		"env":     s.cfg.Env,
		"time":    time.Now().UTC(),
	})
}

// ---------------------------------------------------------------- middleware

// requireWorkspace resolves {workspaceID} — which may be a UUID or a slug —
// and loads the caller's membership. A non-member gets 404, not 403, so the
// endpoint cannot be used to probe which workspaces exist.
func (s *Server) requireWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := middleware.UserFrom(r.Context())
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}

		ref := chi.URLParam(r, "workspaceID")

		// One round trip: read the caller's membership row and inner-join the
		// workspace, filtering on whichever identifier they used.
		var row struct {
			Role      string `json:"role"`
			Status    string `json:"status"`
			Workspace struct {
				ID uuid.UUID `json:"id"`
			} `json:"workspaces"`
		}

		q := s.data.From("workspace_members").
			Select("role,status,workspaces!inner(id)").
			Eq("user_id", user.ID).
			Single()

		if parsed, parseErr := uuid.Parse(ref); parseErr == nil {
			q = q.Eq("workspace_id", parsed)
		} else {
			q = q.Eq("workspaces.slug", ref)
		}

		if err := q.Get(r.Context(), &row); err != nil {
			// Not a member reads as "does not exist", so this cannot be used to
			// probe which workspaces are out there.
			httpx.Fail(w, r, err)
			return
		}

		wsID, role, status := row.Workspace.ID, row.Role, row.Status
		if status != "active" {
			httpx.Fail(w, r, httpx.Errorf(http.StatusForbidden, "membership_"+status,
				"Your membership in this workspace is %s", status))
			return
		}

		ctx := middleware.WithMembership(r.Context(), &middleware.Membership{
			WorkspaceID: wsID, Role: role, Status: status,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRole gates a route on a minimum role. Must run after requireWorkspace.
func (s *Server) requireRole(minimum string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m, err := middleware.MembershipFrom(r.Context())
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
			if !m.AtLeast(minimum) {
				httpx.Fail(w, r, httpx.Errorf(http.StatusForbidden, "forbidden",
					"This action requires the %s role or higher", minimum))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// denyGuest blocks the read-only guest role from mutating anything.
func (s *Server) denyGuest(next http.Handler) http.Handler {
	return s.requireRole("contributor")(next)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		level := slog.LevelInfo
		if ww.Status() >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", chimw.GetReqID(r.Context()),
		)
	})
}

// ctxIDs is the pair every workspace-scoped handler needs.
func (s *Server) ctxIDs(r *http.Request) (userID, workspaceID uuid.UUID, role string, err error) {
	user, err := middleware.UserFrom(r.Context())
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	m, err := middleware.MembershipFrom(r.Context())
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	return user.ID, m.WorkspaceID, m.Role, nil
}
