// Package models holds the wire types the API returns. They are deliberately
// flat: the frontend consumes them directly, so field names are the JSON contract.
package models

import (
	"time"

	"github.com/google/uuid"
)

type Profile struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	FullName     *string   `json:"full_name"`
	AvatarURL    *string   `json:"avatar_url"`
	Timezone     string    `json:"timezone"`
	Presence     string    `json:"presence"`
	PresenceNote *string   `json:"presence_note"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type Workspace struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	JoinCode   string    `json:"join_code,omitempty"` // omitted for non-managers
	JoinPolicy string    `json:"join_policy"`
	LogoURL    *string   `json:"logo_url"`
	CreatedBy  uuid.UUID `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`

	// Populated on the workspace list so the switcher can render without N+1s.
	Role        string `json:"role,omitempty"`
	MemberCount int    `json:"member_count,omitempty"`
}

type Member struct {
	UserID              uuid.UUID `json:"user_id"`
	Email               string    `json:"email"`
	FullName            *string   `json:"full_name"`
	AvatarURL           *string   `json:"avatar_url"`
	Role                string    `json:"role"`
	Status              string    `json:"status"`
	Title               *string   `json:"title"`
	WeeklyCapacityHours float64   `json:"weekly_capacity_hours"`
	Presence            string    `json:"presence"`
	JoinedAt            time.Time `json:"joined_at"`
}

type Team struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Color       string    `json:"color"`
	MemberCount int       `json:"member_count"`
}

type Project struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	TeamID      *uuid.UUID `json:"team_id"`
	Name        string     `json:"name"`
	Key         string     `json:"key"`
	Description *string    `json:"description"`
	Color       string     `json:"color"`
	Icon        *string    `json:"icon"`
	StartDate   *time.Time `json:"start_date"`
	TargetDate  *time.Time `json:"target_date"`
	ArchivedAt  *time.Time `json:"archived_at"`
	CreatedAt   time.Time  `json:"created_at"`

	TotalTasks      int      `json:"total_tasks"`
	DoneTasks       int      `json:"done_tasks"`
	OverdueTasks    int      `json:"overdue_tasks"`
	PercentComplete *float64 `json:"percent_complete"`
}

type Label struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Color string    `json:"color"`
}

type Task struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	ProjectID     uuid.UUID  `json:"project_id"`
	ProjectKey    string     `json:"project_key"`
	ParentID      *uuid.UUID `json:"parent_id"`
	Number        int        `json:"number"`
	Ref           string     `json:"ref"` // "SPR-114"
	Title         string     `json:"title"`
	Description   *string    `json:"description"`
	State         string     `json:"state"`
	Priority      string     `json:"priority"`
	BoardRank     float64    `json:"board_rank"`
	AssigneeID    *uuid.UUID `json:"assignee_id"`
	Assignee      *Profile   `json:"assignee,omitempty"`
	ReporterID    *uuid.UUID `json:"reporter_id"`
	StartDate     *time.Time `json:"start_date"`
	DueDate       *time.Time `json:"due_date"`
	EstimateHours *float64   `json:"estimate_hours"`
	CompletedAt   *time.Time `json:"completed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	Labels       []Label `json:"labels"`
	SubtaskCount int     `json:"subtask_count"`
	SubtaskDone  int     `json:"subtask_done"`
	CommentCount int     `json:"comment_count"`
	BlockedBy    int     `json:"blocked_by"`
	LoggedSecs   int     `json:"logged_seconds"`
}

type Comment struct {
	ID        uuid.UUID   `json:"id"`
	TaskID    *uuid.UUID  `json:"task_id"`
	DocID     *uuid.UUID  `json:"doc_id"`
	ParentID  *uuid.UUID  `json:"parent_id"`
	Author    Profile     `json:"author"`
	Body      string      `json:"body"`
	Mentions  []uuid.UUID `json:"mentions"`
	EditedAt  *time.Time  `json:"edited_at"`
	CreatedAt time.Time   `json:"created_at"`
}

type Activity struct {
	ID        int64      `json:"id"`
	TaskID    *uuid.UUID `json:"task_id"`
	Actor     *Profile   `json:"actor"`
	Verb      string     `json:"verb"`
	Field     *string    `json:"field"`
	OldValue  *string    `json:"old_value"`
	NewValue  *string    `json:"new_value"`
	CreatedAt time.Time  `json:"created_at"`
}

type Dependency struct {
	ID       uuid.UUID `json:"id"`
	SourceID uuid.UUID `json:"source_id"`
	TargetID uuid.UUID `json:"target_id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Ref      string    `json:"ref"`
	State    string    `json:"state"`
}

type TimeEntry struct {
	ID              uuid.UUID  `json:"id"`
	TaskID          *uuid.UUID `json:"task_id"`
	TaskTitle       *string    `json:"task_title"`
	UserID          uuid.UUID  `json:"user_id"`
	Description     *string    `json:"description"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSeconds *int       `json:"duration_seconds"`
	IsBillable      bool       `json:"is_billable"`
}

type Workload struct {
	UserID              uuid.UUID `json:"user_id"`
	FullName            *string   `json:"full_name"`
	AvatarURL           *string   `json:"avatar_url"`
	WeeklyCapacityHours float64   `json:"weekly_capacity_hours"`
	OpenHours           float64   `json:"open_hours"`
	OpenTasks           int       `json:"open_tasks"`
	OverdueTasks        int       `json:"overdue_tasks"`
	UtilizationPct      *float64  `json:"utilization_pct"`
}

type Notification struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      *string    `json:"body"`
	TaskID    *uuid.UUID `json:"task_id"`
	URL       *string    `json:"url"`
	Actor     *Profile   `json:"actor"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type JoinRequest struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	FullName  *string   `json:"full_name"`
	AvatarURL *string   `json:"avatar_url"`
	Message   *string   `json:"message"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
