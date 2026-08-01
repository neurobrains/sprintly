package db

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sprintly/sprintly/backend/httpx"
)

// restError is PostgREST's error body. `code` is the Postgres SQLSTATE for
// anything raised by the database, which is what lets the mapping below stay
// identical to the one the pgx layer used — including the deliberate SQLSTATEs
// the RPCs in schema.sql raise.
type restError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details"`
	Hint    string `json:"hint"`
}

// ErrNoRows is returned when Single() matched zero rows.
var ErrNoRows = httpx.ErrNotFound

func mapRESTError(status int, payload []byte) error {
	var e restError
	_ = json.Unmarshal(payload, &e)

	// Single() asks for exactly one row; PostgREST answers 406 when the count
	// is not 1. Zero rows is a 404 to the caller, which is what handlers written
	// against pgx.ErrNoRows expect.
	if status == http.StatusNotAcceptable || e.Code == "PGRST116" {
		return httpx.ErrNotFound
	}

	switch e.Code {
	case "23505": // unique_violation
		return &httpx.Error{Status: http.StatusConflict, Code: "conflict",
			Message: friendlyUnique(e.Details + e.Message), Detail: e.Details}
	case "23503": // foreign_key_violation
		return httpx.Errorf(http.StatusBadRequest, "invalid_reference",
			"That reference does not exist")
	case "23514": // check_violation
		return httpx.Errorf(http.StatusBadRequest, "invalid", "%s", e.Message)
	case "23502": // not_null_violation
		return httpx.Errorf(http.StatusBadRequest, "missing_field", "%s", e.Message)
	case "42501": // insufficient_privilege — raised by our RPCs
		return &httpx.Error{Status: http.StatusForbidden, Code: "forbidden", Message: e.Message}
	case "P0002": // no_data_found — raised by our RPCs
		return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Message: e.Message}
	case "P0001": // bare RAISE EXCEPTION
		return httpx.Errorf(http.StatusBadRequest, "invalid", "%s", e.Message)
	}

	// A bad key or an expired one — a deployment fault, not the caller's.
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return httpx.Errorf(http.StatusInternalServerError, "internal",
			"data layer rejected the service credentials")
	}
	if status == http.StatusNotFound {
		return httpx.ErrNotFound
	}

	return httpx.Errorf(http.StatusBadGateway, "upstream",
		"database request failed: %s", firstNonEmpty(e.Message, string(payload), "unknown error"))
}

// friendlyUnique turns a violated constraint into something a user can act on.
// PostgREST reports the constraint inside the message/details text rather than
// as its own field, so this matches on substring.
func friendlyUnique(text string) string {
	switch {
	case strings.Contains(text, "workspaces_slug_key"):
		return "That workspace URL is taken"
	case strings.Contains(text, "workspaces_join_code_key"):
		return "Could not allocate a join code, please retry"
	case strings.Contains(text, "projects_workspace_id_key_key"):
		return "A project with that key already exists"
	case strings.Contains(text, "workspace_members_pkey"):
		return "Already a member of this workspace"
	case strings.Contains(text, "time_entries_one_running_idx"):
		return "You already have a timer running"
	case strings.Contains(text, "labels_workspace_id_name_key"):
		return "A label with that name already exists"
	case strings.Contains(text, "channels_workspace_id_name_key"):
		return "A channel with that name already exists"
	case strings.Contains(text, "teams_workspace_id_name_key"):
		return "A team with that name already exists"
	default:
		return "That already exists"
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
