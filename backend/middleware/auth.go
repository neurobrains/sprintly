// Package middleware verifies Supabase access tokens and attaches the caller's
// identity and workspace role to the request context.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/sprintly/sprintly/backend/config"
	"github.com/sprintly/sprintly/backend/db"
	"github.com/sprintly/sprintly/backend/httpx"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxMembership
)

// User is the authenticated caller, taken straight from the Supabase JWT.
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Provider  string    `json:"provider,omitempty"` // "google" for Google sign-in
}

// Membership is the caller's standing in the workspace addressed by the route.
type Membership struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
}

// Role ranking, so "at least manager" is a comparison rather than a set literal.
var roleRank = map[string]int{
	"guest": 0, "contributor": 1, "manager": 2, "admin": 3, "owner": 4,
}

// AtLeast reports whether the member's role meets the required level.
func (m Membership) AtLeast(role string) bool {
	return roleRank[m.Role] >= roleRank[role]
}

// Verifier validates Supabase access tokens.
type Verifier struct {
	jwks      *keySet
	hmacKey   []byte
	audience  string
	issuerURL string
}

func NewVerifier(cfg *config.Config) *Verifier {
	v := &Verifier{audience: cfg.JWTAudience}
	if url := cfg.JWKSURL(); url != "" {
		v.jwks = newKeySet(url)
		v.issuerURL = cfg.SupabaseURL + "/auth/v1"
	}
	if cfg.SupabaseJWTSecret != "" {
		v.hmacKey = []byte(cfg.SupabaseJWTSecret)
	}
	return v
}

type claims struct {
	jwt.RegisteredClaims
	Email        string         `json:"email"`
	Role         string         `json:"role"`
	SessionID    string         `json:"session_id"`
	AppMetadata  map[string]any `json:"app_metadata"`
	UserMetadata map[string]any `json:"user_metadata"`
}

// Verify parses and validates a raw access token, returning the caller.
func (v *Verifier) Verify(ctx context.Context, raw string) (*User, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"ES256", "RS256", "HS256"}),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(30*time.Second),
		jwt.WithExpirationRequired(),
	)

	var c claims
	token, err := parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() == "HS256" {
			if len(v.hmacKey) == 0 {
				return nil, fmt.Errorf("HS256 token received but SUPABASE_JWT_SECRET is not set")
			}
			return v.hmacKey, nil
		}
		if v.jwks == nil {
			return nil, fmt.Errorf("asymmetric token received but SUPABASE_URL is not set")
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("token is missing a kid header")
		}
		return v.jwks.key(ctx, kid)
	})
	if err != nil || !token.Valid {
		return nil, httpx.ErrUnauthorized
	}

	// Supabase issues tokens for the project's auth server; anything else is
	// a token minted elsewhere and must not be trusted.
	if v.issuerURL != "" && c.Issuer != "" && c.Issuer != v.issuerURL {
		return nil, httpx.ErrUnauthorized
	}

	id, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, httpx.ErrUnauthorized
	}

	u := &User{ID: id, Email: c.Email}
	u.Name = firstString(c.UserMetadata, "full_name", "name")
	u.AvatarURL = firstString(c.UserMetadata, "avatar_url", "picture")
	u.Provider = firstString(c.AppMetadata, "provider")
	if u.Email == "" {
		u.Email = firstString(c.UserMetadata, "email")
	}
	return u, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- middleware

// Required rejects requests without a valid bearer token.
func (v *Verifier) Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			httpx.Fail(w, r, httpx.ErrUnauthorized)
			return
		}
		user, err := v.Verify(r.Context(), raw)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}

		// The token travels on the context so the data layer can forward it to
		// PostgREST when no service key is configured. A service-key client
		// ignores it.
		ctx := db.WithToken(WithUser(r.Context(), user), raw)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken accepts the standard Authorization header and, for the WebSocket
// upgrade (where browsers cannot set headers), an `access_token` query param.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("access_token"))
}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxUser, u)
}

// UserFrom returns the authenticated caller. Only call it behind Required.
func UserFrom(ctx context.Context) (*User, error) {
	u, ok := ctx.Value(ctxUser).(*User)
	if !ok || u == nil {
		return nil, httpx.ErrUnauthorized
	}
	return u, nil
}

func WithMembership(ctx context.Context, m *Membership) context.Context {
	return context.WithValue(ctx, ctxMembership, m)
}

// MembershipFrom returns the caller's role in the addressed workspace. Only call
// it behind the workspace middleware.
func MembershipFrom(ctx context.Context) (*Membership, error) {
	m, ok := ctx.Value(ctxMembership).(*Membership)
	if !ok || m == nil {
		return nil, httpx.ErrForbidden
	}
	return m, nil
}
