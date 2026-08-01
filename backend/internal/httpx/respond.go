// Package httpx holds the small HTTP conveniences shared by every handler:
// JSON encoding/decoding, a typed error shape, and request-scoped helpers.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// Error is the single error shape the API returns. `Code` is a stable machine
// string the frontend switches on; `Message` is safe to show a user.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func Errorf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

var (
	ErrUnauthorized = &Error{http.StatusUnauthorized, "unauthorized", "Sign in to continue", ""}
	ErrForbidden    = &Error{http.StatusForbidden, "forbidden", "You do not have access to this resource", ""}
	ErrNotFound     = &Error{http.StatusNotFound, "not_found", "Not found", ""}
	ErrConflict     = &Error{http.StatusConflict, "conflict", "That already exists", ""}
)

func BadRequest(format string, args ...any) *Error {
	return Errorf(http.StatusBadRequest, "bad_request", format, args...)
}

// JSON writes v with the given status. A nil body sends 204.
func JSON(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	buf, err := json.Marshal(v)
	if err != nil {
		slog.Error("encode response", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// Fail renders err as JSON. Anything that is not an *Error is logged in full and
// reported to the client as a generic 500 — internals never leak.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		slog.ErrorContext(r.Context(), "unhandled error",
			"error", err, "path", r.URL.Path, "method", r.Method)
		apiErr = &Error{http.StatusInternalServerError, "internal", "Something went wrong on our end", ""}
	}
	JSON(w, apiErr.Status, apiErr)
}

// Decode reads a JSON body, rejecting unknown fields so typos in client payloads
// surface immediately instead of being silently dropped.
func Decode(r *http.Request, dst any) error {
	if r.Body == nil {
		return BadRequest("request body is required")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20)) // 2 MiB
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return BadRequest("invalid JSON body: %v", err)
	}
	return nil
}

// UUIDParam parses a chi URL parameter that must be a UUID.
func UUIDParam(raw, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, BadRequest("%s must be a UUID", name)
	}
	return id, nil
}

// QueryInt reads an optional positive integer query parameter.
func QueryInt(r *http.Request, name string, fallback, max int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}
