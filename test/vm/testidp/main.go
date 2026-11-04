// testidp is the token issuer of the OpenShell tests (test/openshell):
// it issues the agents' access tokens for mcp-gateway's HTTPS listener,
// as a site's IdP would with a client per agent (CONTRACT.md §16a). The
// tokens are ES256 JWTs with subject agent-<name> and no local-user
// claim, so the gateway keeps the subject as the agent's principal.
//
// It serves OIDC discovery and the key set, which the gateway fetches
// from the issuer (http is accepted on the loopback), and hands out
// tokens to whoever asks: for tests only.
//
//	GET /.well-known/openid-configuration
//	GET /jwks
//	GET /token?sub=agent-sb&aud=https://...&exp=3600   → the JWT
//
// Usage: testidp [--listen 127.0.0.1:18445]
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strconv"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18445", "address (the issuer is http://ADDRESS)")
	flag.Parse()
	idp, err := New("http://" + *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(*listen, idp))
}

// IDP issues tokens signed with one ephemeral P-256 key (kid k1).
type IDP struct {
	Issuer string
	key    *ecdsa.PrivateKey
	now    func() time.Time
}

func New(issuer string) (*IDP, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &IDP{Issuer: issuer, key: k, now: time.Now}, nil
}

func (i *IDP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, map[string]any{
			"issuer": i.Issuer, "jwks_uri": i.Issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"ES256"},
		})
	case "/jwks":
		writeJSON(w, map[string]any{"keys": []any{i.JWK()}})
	case "/token":
		q := r.URL.Query()
		if q.Get("sub") == "" || q.Get("aud") == "" {
			http.Error(w, "want sub and aud", http.StatusBadRequest)
			return
		}
		exp, _ := strconv.Atoi(q.Get("exp"))
		if exp <= 0 {
			exp = 3600
		}
		tok, err := i.Token(q.Get("sub"), q.Get("aud"), time.Duration(exp)*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(tok))
	default:
		http.NotFound(w, r)
	}
}

// JWK is the public key as a JSON Web Key.
func (i *IDP) JWK() map[string]any {
	b, _ := i.key.PublicKey.Bytes() // 0x04 || X || Y
	return map[string]any{"kty": "EC", "crv": "P-256", "kid": "k1", "use": "sig", "alg": "ES256",
		"x": b64(b[1:33]), "y": b64(b[33:65])}
}

// Token signs a JWT for sub and aud, valid for ttl.
func (i *IDP) Token(sub, aud string, ttl time.Duration) (string, error) {
	now := i.now()
	h, _ := json.Marshal(map[string]any{"alg": "ES256", "typ": "JWT", "kid": "k1"})
	c, _ := json.Marshal(map[string]any{"iss": i.Issuer, "sub": sub, "aud": aud,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(ttl).Unix()})
	in := b64(h) + "." + b64(c)
	d := sha256.Sum256([]byte(in))
	r, s, err := ecdsa.Sign(rand.Reader, i.key, d[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return in + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
