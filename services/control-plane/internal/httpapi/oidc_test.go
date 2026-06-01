package httpapi

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOIDCModeValidatesBearerJWTAndIgnoresTrustedHeaders(t *testing.T) {
	key := mustGenerateRSAKey(t)
	issuer := "https://issuer.example.test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, key.PublicKey, "kid-1")
	}))
	defer jwksServer.Close()

	store, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode: "oidc",
		OIDC: OIDCConfig{
			Issuer:   issuer,
			Audience: "niceagent",
			JWKSURL:  jwksServer.URL,
		},
	})

	trustedHeaderOnly := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActor(trustedHeaderOnly, "user-a", "project-a")
	trustedHeaderResponse := httptest.NewRecorder()
	handler.ServeHTTP(trustedHeaderResponse, trustedHeaderOnly)
	if trustedHeaderResponse.Code != http.StatusUnauthorized {
		t.Fatalf("trusted header only status = %d, body = %s", trustedHeaderResponse.Code, trustedHeaderResponse.Body.String())
	}

	token := signTestJWT(t, key, "kid-1", map[string]any{
		"iss":                  issuer,
		"sub":                  "oidc-user",
		"aud":                  []string{"niceagent"},
		"exp":                  time.Now().Add(time.Hour).Unix(),
		"iat":                  time.Now().Add(-time.Minute).Unix(),
		"email":                "oidc-user@example.test",
		"name":                 "OIDC User",
		"niceagent_project_id": "project-oidc",
		"niceagent_org_id":     "org-oidc",
		"niceagent_roles":      []string{"owner"},
	})

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "oidc"}))
	createChat.Header.Set("Authorization", "Bearer "+token)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createChat)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("oidc create chat status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	listChats := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	listChats.Header.Set("Authorization", "Bearer "+token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listChats)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("oidc list chats status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	if roles := store.ListProjectRoles("oidc-user", "project-oidc"); len(roles) != 0 {
		t.Fatalf("oidc token roles should not mutate project membership, got %v", roles)
	}
}

func TestOIDCModeRejectsInvalidAudience(t *testing.T) {
	key := mustGenerateRSAKey(t)
	issuer := "https://issuer.example.test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, key.PublicKey, "kid-1")
	}))
	defer jwksServer.Close()

	_, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode: "oidc",
		OIDC: OIDCConfig{
			Issuer:   issuer,
			Audience: "niceagent",
			JWKSURL:  jwksServer.URL,
		},
	})
	token := signTestJWT(t, key, "kid-1", map[string]any{
		"iss":                  issuer,
		"sub":                  "oidc-user",
		"aud":                  "other-audience",
		"exp":                  time.Now().Add(time.Hour).Unix(),
		"niceagent_project_id": "project-oidc",
		"niceagent_roles":      []string{"owner"},
	})

	request := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid audience status = %d, body = %s", response.Code, response.Body.String())
	}
}

func mustGenerateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func writeTestJWKS(t *testing.T, w http.ResponseWriter, key rsa.PublicKey, kid string) {
	t.Helper()
	exponent := big.NewInt(int64(key.E)).Bytes()
	body := map[string]any{
		"keys": []map[string]string{
			{
				"kty": "RSA",
				"kid": kid,
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(exponent),
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("write jwks: %v", err)
	}
}
