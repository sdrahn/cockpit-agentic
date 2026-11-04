package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) []byte {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d %s", url, res.StatusCode, b)
	}
	return b
}

// The token verifies with the published key and carries what
// mcp-gateway checks (iss, aud, exp, sub), and no local-user claim.
func TestTokenVerifiesWithDiscoveredKey(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	idp, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = idp

	var disc struct{ Issuer, JWKS_URI string }
	json.Unmarshal(get(t, srv.URL+"/.well-known/openid-configuration"), &disc)
	if disc.Issuer != srv.URL || disc.JWKS_URI != srv.URL+"/jwks" {
		t.Fatalf("discovery: %+v", disc)
	}
	var set struct {
		Keys []struct{ Kty, Crv, Kid, Use, X, Y string }
	}
	json.Unmarshal(get(t, disc.JWKS_URI), &set)
	if len(set.Keys) != 1 || set.Keys[0].Kty != "EC" || set.Keys[0].Crv != "P-256" || set.Keys[0].Kid != "k1" || set.Keys[0].Use != "sig" {
		t.Fatalf("jwks: %+v", set)
	}
	dec := func(s string) []byte { b, _ := base64.RawURLEncoding.DecodeString(s); return b }
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(dec(set.Keys[0].X)), Y: new(big.Int).SetBytes(dec(set.Keys[0].Y))}

	tok := string(get(t, srv.URL+"/token?sub=agent-sb&aud=https://host.openshell.internal:18443/mcp&exp=60"))
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWS: %q", tok)
	}
	d := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig := dec(parts[2])
	if len(sig) != 64 || !ecdsa.Verify(pub, d[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the signature does not verify with the published key")
	}
	var h map[string]any
	json.Unmarshal(dec(parts[0]), &h)
	if h["alg"] != "ES256" || h["kid"] != "k1" {
		t.Errorf("header %v", h)
	}
	var c map[string]any
	json.Unmarshal(dec(parts[1]), &c)
	if c["iss"] != srv.URL || c["sub"] != "agent-sb" || c["aud"] != "https://host.openshell.internal:18443/mcp" {
		t.Errorf("claims %v", c)
	}
	if exp := int64(c["exp"].(float64)); exp < time.Now().Unix()+50 || exp > time.Now().Unix()+70 {
		t.Errorf("exp %d", exp)
	}
	if _, ok := c["preferred_username"]; ok {
		t.Error("a local-user claim would map the agent to a local account")
	}
}

func TestTokenWantsSubAndAud(t *testing.T) {
	idp, _ := New("http://x")
	rec := httptest.NewRecorder()
	idp.ServeHTTP(rec, httptest.NewRequest("GET", "/token?sub=agent-sb", nil))
	if rec.Code != 400 {
		t.Errorf("without aud: %d", rec.Code)
	}
}
