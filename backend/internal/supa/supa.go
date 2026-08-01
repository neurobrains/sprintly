// Package supa is Sprintly's data layer: a small PostgREST client that talks to
// Supabase's REST endpoint with the service role key.
//
// Why this instead of a Postgres connection: the deployment only ever holds the
// project URL, the anon key and the service role key — never the database
// password — so there is no `postgresql://` URL to open a pool against. PostgREST
// is the supported way in for a trusted server-side caller.
//
// Two consequences worth knowing before you use it:
//
//   - The service role key bypasses RLS entirely. Exactly like the previous pgx
//     connection did, so the trust model is unchanged: the Go API is its own
//     authorization boundary. Every query MUST be scoped by workspace_id, and
//     forgetting is a cross-tenant leak that nothing downstream will catch.
//
//   - There are no client-side transactions. HTTP calls do not compose into one.
//     Anything that must not half-apply belongs in a SECURITY DEFINER function in
//     supabase/schema.sql, called through RPC — which is where create_workspace,
//     join_workspace, decide_join_request and move_task already live.
package supa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is safe for concurrent use.
type Client struct {
	rest string // https://<ref>.supabase.co/rest/v1
	key  string // service role key
	http *http.Client
}

func New(supabaseURL, serviceKey string) *Client {
	return &Client{
		rest: strings.TrimRight(supabaseURL, "/") + "/rest/v1",
		key:  serviceKey,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// Ping is the liveness probe. It asks PostgREST for zero rows of a table that
// always exists, which exercises DNS, TLS, the key and the schema cache without
// reading any data.
func (c *Client) Ping(ctx context.Context) error {
	return c.From("workspaces").Select("id").Limit(0).Get(ctx, &[]struct{}{})
}

// ---------------------------------------------------------------- query builder

type Query struct {
	c      *Client
	table  string
	params url.Values
	prefer []string
	single bool
	err    error

	// onResponse inspects response headers before the body is decoded. Only
	// Count uses it, to read the total out of Content-Range.
	onResponse func(*http.Response)
}

func (c *Client) From(table string) *Query {
	return &Query{c: c, table: table, params: url.Values{}}
}

// Select names the columns to return. PostgREST embedding works here too:
//
//	Select("*,assignee:profiles!tasks_assignee_id_fkey(id,full_name,avatar_url)")
func (q *Query) Select(cols string) *Query { q.params.Set("select", cols); return q }

func (q *Query) Eq(col string, v any) *Query  { return q.filter(col, "eq", v) }
func (q *Query) Neq(col string, v any) *Query { return q.filter(col, "neq", v) }
func (q *Query) Gt(col string, v any) *Query  { return q.filter(col, "gt", v) }
func (q *Query) Gte(col string, v any) *Query { return q.filter(col, "gte", v) }
func (q *Query) Lt(col string, v any) *Query  { return q.filter(col, "lt", v) }
func (q *Query) Lte(col string, v any) *Query { return q.filter(col, "lte", v) }

// IsNull filters on NULL / NOT NULL.
func (q *Query) IsNull(col string, null bool) *Query {
	if null {
		return q.filter(col, "is", "null")
	}
	q.params.Add(col, "not.is.null")
	return q
}

// In matches any of the values. An empty set is a filter that matches nothing,
// which is the safe reading — never "no filter".
func (q *Query) In(col string, vals []string) *Query {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	q.params.Add(col, "in.("+strings.Join(quoted, ",")+")")
	return q
}

// ILike wraps the pattern in %…% itself; callers pass a bare search term.
func (q *Query) ILike(col, term string) *Query {
	return q.filter(col, "ilike", "*"+term+"*")
}

// Or takes a raw PostgREST or= expression, e.g. `task_id.eq.X,doc_id.eq.Y`.
func (q *Query) Or(expr string) *Query { q.params.Add("or", "("+expr+")"); return q }

func (q *Query) Order(col string, desc bool) *Query {
	dir := ".asc"
	if desc {
		dir = ".desc"
	}
	// Multiple order keys accumulate into one comma-separated parameter.
	if cur := q.params.Get("order"); cur != "" {
		q.params.Set("order", cur+","+col+dir)
	} else {
		q.params.Set("order", col+dir)
	}
	return q
}

func (q *Query) Limit(n int) *Query  { q.params.Set("limit", strconv.Itoa(n)); return q }
func (q *Query) Offset(n int) *Query { q.params.Set("offset", strconv.Itoa(n)); return q }

// Single asks for exactly one row. Zero or many becomes ErrNotFound, which is
// the pgx.ErrNoRows behaviour the handlers were written against.
func (q *Query) Single() *Query { q.single = true; return q }

func (q *Query) filter(col, op string, v any) *Query {
	q.params.Add(col, fmt.Sprintf("%s.%v", op, v))
	return q
}

// ---------------------------------------------------------------- verbs

func (q *Query) Get(ctx context.Context, dest any) error {
	return q.do(ctx, http.MethodGet, nil, dest)
}

// Insert writes one row or a slice of rows. dest may be nil to discard the
// result, in which case nothing is returned over the wire either.
func (q *Query) Insert(ctx context.Context, body, dest any) error {
	return q.do(ctx, http.MethodPost, body, dest)
}

// Upsert is Insert with ON CONFLICT DO UPDATE. onConflict names the conflicting
// column(s), matching a unique constraint.
func (q *Query) Upsert(ctx context.Context, onConflict string, body, dest any) error {
	q.params.Set("on_conflict", onConflict)
	q.prefer = append(q.prefer, "resolution=merge-duplicates")
	return q.do(ctx, http.MethodPost, body, dest)
}

// Update applies body to every row matching the filters. Guard it: an Update
// with no filters rewrites the whole table.
func (q *Query) Update(ctx context.Context, body, dest any) error {
	return q.do(ctx, http.MethodPatch, body, dest)
}

func (q *Query) Delete(ctx context.Context, dest any) error {
	return q.do(ctx, http.MethodDelete, nil, dest)
}

// Count returns how many rows match the filters, without transferring them.
// PostgREST reports it in the Content-Range header as "0-0/<total>".
func (q *Query) Count(ctx context.Context) (int, error) {
	q.params.Set("select", "id")
	q.params.Set("limit", "1")
	q.prefer = append(q.prefer, "count=exact")

	var total int
	q.onResponse = func(resp *http.Response) {
		_, after, found := strings.Cut(resp.Header.Get("Content-Range"), "/")
		if !found || after == "*" {
			return
		}
		if n, err := strconv.Atoi(after); err == nil {
			total = n
		}
	}
	if err := q.do(ctx, http.MethodGet, nil, nil); err != nil {
		return 0, err
	}
	return total, nil
}

// RPC calls a Postgres function. This is the escape hatch for anything that
// needs a transaction, a CTE, or SQL that PostgREST cannot express.
func (c *Client) RPC(ctx context.Context, fn string, args map[string]any, dest any) error {
	q := &Query{c: c, table: "rpc/" + fn, params: url.Values{}}
	if args == nil {
		args = map[string]any{}
	}
	return q.do(ctx, http.MethodPost, args, dest)
}

// RPCSingle calls a function returning a composite/scalar and decodes the one value.
func (c *Client) RPCSingle(ctx context.Context, fn string, args map[string]any, dest any) error {
	q := &Query{c: c, table: "rpc/" + fn, params: url.Values{}, single: true}
	if args == nil {
		args = map[string]any{}
	}
	return q.do(ctx, http.MethodPost, args, dest)
}

func (q *Query) do(ctx context.Context, method string, body, dest any) error {
	if q.err != nil {
		return q.err
	}

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s body: %w", q.table, err)
		}
		reader = bytes.NewReader(buf)
	}

	endpoint := q.c.rest + "/" + q.table
	if enc := q.params.Encode(); enc != "" {
		endpoint += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}

	req.Header.Set("apikey", q.c.key)
	req.Header.Set("Authorization", "Bearer "+q.c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if q.single {
		// Makes PostgREST return a bare object and 406 on a row count != 1.
		req.Header.Set("Accept", "application/vnd.pgrst.object+json")
	}

	prefer := q.prefer
	if dest != nil {
		prefer = append(prefer, "return=representation")
	} else if method != http.MethodGet {
		prefer = append(prefer, "return=minimal")
	}
	if len(prefer) > 0 {
		req.Header.Set("Prefer", strings.Join(prefer, ","))
	}

	resp, err := q.c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, q.table, err)
	}
	defer resp.Body.Close()

	if q.onResponse != nil {
		q.onResponse(resp)
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read %s response: %w", q.table, err)
	}

	if resp.StatusCode >= 300 {
		return mapRESTError(resp.StatusCode, payload)
	}
	if dest == nil || len(payload) == 0 || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(payload, dest); err != nil {
		return fmt.Errorf("decode %s response: %w", q.table, err)
	}
	return nil
}
