package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// keySet caches the public keys Supabase publishes at
// /auth/v1/.well-known/jwks.json. Supabase signs access tokens with ES256 by
// default on new projects and RS256 on some older ones; both are handled here.
//
// Keys are refreshed lazily: a lookup miss triggers at most one refetch per
// minRefresh window, which is what makes key rotation self-healing without
// hammering the auth server when an attacker sends garbage `kid`s.
type keySet struct {
	url    string
	client *http.Client

	mu          sync.RWMutex
	keys        map[string]any
	lastFetched time.Time
}

const (
	jwksTTL        = 12 * time.Hour
	jwksMinRefresh = time.Minute
)

func newKeySet(url string) *keySet {
	return &keySet{
		url:    url,
		client: &http.Client{Timeout: 10 * time.Second},
		keys:   map[string]any{},
	}
}

func (k *keySet) key(ctx context.Context, kid string) (any, error) {
	k.mu.RLock()
	pub, ok := k.keys[kid]
	fresh := time.Since(k.lastFetched) < jwksTTL
	canRefresh := time.Since(k.lastFetched) > jwksMinRefresh
	k.mu.RUnlock()

	if ok && fresh {
		return pub, nil
	}
	if !ok && !canRefresh {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}

	if err := k.refresh(ctx); err != nil {
		// Serve a stale key rather than reject every request during an outage.
		if ok {
			return pub, nil
		}
		return nil, err
	}

	k.mu.RLock()
	defer k.mu.RUnlock()
	if pub, ok := k.keys[kid]; ok {
		return pub, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (k *keySet) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: unexpected status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	parsed := make(map[string]any, len(doc.Keys))
	for _, key := range doc.Keys {
		pub, err := key.publicKey()
		if err != nil {
			continue // ignore key types we cannot use rather than failing the set
		}
		parsed[key.Kid] = pub
	}
	if len(parsed) == 0 {
		return fmt.Errorf("JWKS contained no usable keys")
	}

	k.mu.Lock()
	k.keys = parsed
	k.lastFetched = time.Now()
	k.mu.Unlock()
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j jwk) publicKey() (any, error) {
	switch j.Kty {
	case "EC":
		curve, err := curveFor(j.Crv)
		if err != nil {
			return nil, err
		}
		x, err := b64uint(j.X)
		if err != nil {
			return nil, err
		}
		y, err := b64uint(j.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil

	case "RSA":
		n, err := b64uint(j.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(j.E)
		if err != nil {
			return nil, err
		}
		// Left-pad the exponent to 4 bytes so it can be read as a uint32.
		if len(eBytes) > 4 {
			return nil, fmt.Errorf("RSA exponent too large")
		}
		padded := make([]byte, 4)
		copy(padded[4-len(eBytes):], eBytes)
		return &rsa.PublicKey{N: n, E: int(binary.BigEndian.Uint32(padded))}, nil

	default:
		return nil, fmt.Errorf("unsupported key type %q", j.Kty)
	}
}

func curveFor(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported curve %q", name)
	}
}

func b64uint(raw string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}
