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
	"net/url"
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

// The client credentials grant, as OpenShell's gateway uses it.
func TestClientCredentials(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	idp, _ := New(srv.URL)
	idp.Clients = map[string]Client{"agent-sb": {Secret: "s3cret", Subject: "agent-sb", Audience: "https://gw/mcp"}}
	idp.TTL = 90 * time.Second
	srv.Config.Handler = idp

	post := func(form url.Values, basic bool) (*http.Response, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+"/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if basic {
			req.SetBasicAuth(form.Get("client_id"), form.Get("client_secret"))
			form.Del("client_id")
			form.Del("client_secret")
			req.Body = io.NopCloser(strings.NewReader(form.Encode()))
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		return res, m
	}
	ok := url.Values{"grant_type": {"client_credentials"}, "client_id": {"agent-sb"}, "client_secret": {"s3cret"}}
	for _, basic := range []bool{false, true} {
		f := url.Values{}
		for k, v := range ok {
			f[k] = append([]string(nil), v...)
		}
		res, m := post(f, basic)
		if res.StatusCode != 200 || m["token_type"] != "Bearer" || m["expires_in"] != float64(90) || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("basic=%v: %d %v", basic, res.StatusCode, m)
		}
		parts := strings.Split(m["access_token"].(string), ".")
		b, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c map[string]any
		json.Unmarshal(b, &c)
		if c["sub"] != "agent-sb" || c["aud"] != "https://gw/mcp" || c["iss"] != srv.URL {
			t.Errorf("claims %v", c)
		}
	}
	if n := idp.minted["agent-sb"]; n != 2 {
		t.Errorf("minted %d", n)
	}
	for name, f := range map[string]url.Values{
		"wrong secret":   {"grant_type": {"client_credentials"}, "client_id": {"agent-sb"}, "client_secret": {"nope"}},
		"unknown client": {"grant_type": {"client_credentials"}, "client_id": {"x"}, "client_secret": {"s3cret"}},
	} {
		if res, m := post(f, false); res.StatusCode != 401 || m["error"] != "invalid_client" {
			t.Errorf("%s: %d %v", name, res.StatusCode, m)
		}
	}
	if res, m := post(url.Values{"grant_type": {"password"}}, false); res.StatusCode != 400 || m["error"] != "unsupported_grant_type" {
		t.Errorf("password grant: %d %v", res.StatusCode, m)
	}
}

func TestClientFlag(t *testing.T) {
	var c clientFlags
	if err := c.Set("agent-sb:s3cret:agent-sb:https://host.openshell.internal:18443/mcp"); err != nil {
		t.Fatal(err)
	}
	if got := c["agent-sb"]; got.Audience != "https://host.openshell.internal:18443/mcp" || got.Subject != "agent-sb" {
		t.Errorf("%+v", got)
	}
	if err := c.Set("only:two"); err == nil {
		t.Error("accepted a short client")
	}
}
