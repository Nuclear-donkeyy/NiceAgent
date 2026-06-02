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
	"net/url"
	"strings"
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

func TestOIDCBrowserLoginCreatesSessionCookie(t *testing.T) {
	key := mustGenerateRSAKey(t)
	issuer := "https://issuer.example.test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, key.PublicKey, "kid-1")
	}))
	defer jwksServer.Close()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code-a" || r.Form.Get("client_id") != "niceagent-web" {
			t.Fatalf("token request form = %s", r.Form.Encode())
		}
		writeOIDCTokenResponse(t, w, signTestJWT(t, key, "kid-1", map[string]any{
			"iss":                  issuer,
			"sub":                  "browser-user",
			"aud":                  "niceagent-web",
			"exp":                  time.Now().Add(time.Hour).Unix(),
			"email":                "browser@example.test",
			"name":                 "Browser User",
			"niceagent_project_id": "browser-project",
			"niceagent_org_id":     "browser-org",
			"niceagent_roles":      []string{"owner"},
		}), "refresh-a")
	}))
	defer tokenServer.Close()

	_, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode: "oidc",
		OIDC: OIDCConfig{
			Issuer:   issuer,
			Audience: "niceagent-web",
			JWKSURL:  jwksServer.URL,
		},
		OIDCBrowser: OIDCBrowserConfig{
			ClientID:          "niceagent-web",
			ClientSecret:      "client-secret",
			AuthURL:           "https://idp.example.test/authorize",
			TokenURL:          tokenServer.URL,
			RedirectURL:       "https://app.example.test/auth/oidc/callback",
			SessionSecret:     "session-secret-session-secret",
			SessionTTLSeconds: 3600,
		},
	})

	login := httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusFound {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	location, err := url.Parse(loginResponse.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse login redirect: %v", err)
	}
	if location.Host != "idp.example.test" || location.Query().Get("client_id") != "niceagent-web" || location.Query().Get("response_type") != "code" {
		t.Fatalf("login redirect = %s", location.String())
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("login redirect missing state")
	}
	stateCookie := findCookie(loginResponse.Result().Cookies(), oidcStateCookieName)
	if stateCookie == nil || stateCookie.Value != state {
		t.Fatalf("state cookie = %#v, want state %q", stateCookie, state)
	}

	callback := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=code-a&state="+url.QueryEscape(state), nil)
	callback.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body = %s", callbackResponse.Code, callbackResponse.Body.String())
	}
	sessionCookie := findCookie(callbackResponse.Result().Cookies(), oidcSessionCookieName)
	if sessionCookie == nil || sessionCookie.Value == "" || !sessionCookie.HttpOnly {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}
	csrfCookie := findCookie(callbackResponse.Result().Cookies(), oidcCSRFCookieName)
	if csrfCookie == nil || csrfCookie.Value == "" || csrfCookie.HttpOnly {
		t.Fatalf("csrf cookie = %#v", csrfCookie)
	}

	listChats := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	listChats.AddCookie(sessionCookie)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listChats)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("session list chats status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestOIDCBrowserRefreshUpdatesSession(t *testing.T) {
	key := mustGenerateRSAKey(t)
	issuer := "https://issuer.example.test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, key.PublicKey, "kid-1")
	}))
	defer jwksServer.Close()

	var refreshSeen bool
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			writeOIDCTokenResponse(t, w, signTestJWT(t, key, "kid-1", map[string]any{
				"iss":                  issuer,
				"sub":                  "refresh-user",
				"aud":                  "niceagent-web",
				"exp":                  time.Now().Add(time.Hour).Unix(),
				"niceagent_project_id": "refresh-project",
				"niceagent_roles":      []string{"owner"},
			}), "refresh-a")
		case "refresh_token":
			refreshSeen = true
			if r.Form.Get("refresh_token") != "refresh-a" {
				t.Fatalf("refresh token = %q", r.Form.Get("refresh_token"))
			}
			writeOIDCTokenResponse(t, w, signTestJWT(t, key, "kid-1", map[string]any{
				"iss":                  issuer,
				"sub":                  "refresh-user",
				"aud":                  "niceagent-web",
				"exp":                  time.Now().Add(time.Hour).Unix(),
				"niceagent_project_id": "refresh-project",
				"niceagent_roles":      []string{"owner"},
			}), "refresh-b")
		default:
			t.Fatalf("unexpected grant type %q", r.Form.Get("grant_type"))
		}
	}))
	defer tokenServer.Close()

	_, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode: "oidc",
		OIDC: OIDCConfig{
			Issuer:   issuer,
			Audience: "niceagent-web",
			JWKSURL:  jwksServer.URL,
		},
		OIDCBrowser: OIDCBrowserConfig{
			ClientID:          "niceagent-web",
			AuthURL:           "https://idp.example.test/authorize",
			TokenURL:          tokenServer.URL,
			SessionSecret:     "session-secret-session-secret",
			SessionTTLSeconds: 3600,
		},
	})

	sessionCookie, csrfCookie := loginOIDCTestCookies(t, handler)
	refresh := httptest.NewRequest(http.MethodPost, "/auth/oidc/refresh", nil)
	refresh.AddCookie(sessionCookie)
	refresh.AddCookie(csrfCookie)
	refresh.Header.Set(oidcCSRFHeaderName, csrfCookie.Value)
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	if !refreshSeen {
		t.Fatal("token endpoint did not receive refresh_token grant")
	}
	nextSession := findCookie(refreshResponse.Result().Cookies(), oidcSessionCookieName)
	if nextSession == nil || nextSession.Value == "" || nextSession.Value == sessionCookie.Value {
		t.Fatalf("refreshed session cookie = %#v, old = %q", nextSession, sessionCookie.Value)
	}
	nextCSRF := findCookie(refreshResponse.Result().Cookies(), oidcCSRFCookieName)
	if nextCSRF == nil || nextCSRF.Value == "" || nextCSRF.Value == csrfCookie.Value || nextCSRF.HttpOnly {
		t.Fatalf("refreshed csrf cookie = %#v, old = %q", nextCSRF, csrfCookie.Value)
	}
	oldSessionRequest := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	oldSessionRequest.AddCookie(sessionCookie)
	oldSessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(oldSessionResponse, oldSessionRequest)
	if oldSessionResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, body = %s", oldSessionResponse.Code, oldSessionResponse.Body.String())
	}
	newSessionRequest := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	newSessionRequest.AddCookie(nextSession)
	newSessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(newSessionResponse, newSessionRequest)
	if newSessionResponse.Code != http.StatusOK {
		t.Fatalf("new session status = %d, body = %s", newSessionResponse.Code, newSessionResponse.Body.String())
	}
}

func TestOIDCBrowserRefreshRejectsMissingCSRF(t *testing.T) {
	key := mustGenerateRSAKey(t)
	issuer := "https://issuer.example.test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, key.PublicKey, "kid-1")
	}))
	defer jwksServer.Close()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		writeOIDCTokenResponse(t, w, signTestJWT(t, key, "kid-1", map[string]any{
			"iss":                  issuer,
			"sub":                  "csrf-user",
			"aud":                  "niceagent-web",
			"exp":                  time.Now().Add(time.Hour).Unix(),
			"niceagent_project_id": "csrf-project",
			"niceagent_roles":      []string{"owner"},
		}), "refresh-a")
	}))
	defer tokenServer.Close()

	_, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode: "oidc",
		OIDC: OIDCConfig{
			Issuer:   issuer,
			Audience: "niceagent-web",
			JWKSURL:  jwksServer.URL,
		},
		OIDCBrowser: OIDCBrowserConfig{
			ClientID:          "niceagent-web",
			AuthURL:           "https://idp.example.test/authorize",
			TokenURL:          tokenServer.URL,
			SessionSecret:     "session-secret-session-secret",
			SessionTTLSeconds: 3600,
		},
	})

	sessionCookie := loginOIDCTestSession(t, handler)
	refresh := httptest.NewRequest(http.MethodPost, "/auth/oidc/refresh", nil)
	refresh.AddCookie(sessionCookie)
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusForbidden {
		t.Fatalf("refresh without csrf status = %d, body = %s", refreshResponse.Code, refreshResponse.Body.String())
	}
}

func TestOIDCBrowserLogoutRequiresCSRFWhenSessionExists(t *testing.T) {
	key := mustGenerateRSAKey(t)
	issuer := "https://issuer.example.test"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, key.PublicKey, "kid-1")
	}))
	defer jwksServer.Close()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		writeOIDCTokenResponse(t, w, signTestJWT(t, key, "kid-1", map[string]any{
			"iss":                  issuer,
			"sub":                  "logout-user",
			"aud":                  "niceagent-web",
			"exp":                  time.Now().Add(time.Hour).Unix(),
			"niceagent_project_id": "logout-project",
			"niceagent_roles":      []string{"owner"},
		}), "refresh-a")
	}))
	defer tokenServer.Close()

	_, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode: "oidc",
		OIDC: OIDCConfig{
			Issuer:   issuer,
			Audience: "niceagent-web",
			JWKSURL:  jwksServer.URL,
		},
		OIDCBrowser: OIDCBrowserConfig{
			ClientID:          "niceagent-web",
			AuthURL:           "https://idp.example.test/authorize",
			TokenURL:          tokenServer.URL,
			SessionSecret:     "session-secret-session-secret",
			SessionTTLSeconds: 3600,
		},
	})

	sessionCookie, csrfCookie := loginOIDCTestCookies(t, handler)
	missingCSRF := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	missingCSRF.AddCookie(sessionCookie)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingCSRF)
	if missingResponse.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf status = %d, body = %s", missingResponse.Code, missingResponse.Body.String())
	}

	logout := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logout.AddCookie(sessionCookie)
	logout.AddCookie(csrfCookie)
	logout.Header.Set(oidcCSRFHeaderName, csrfCookie.Value)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", logoutResponse.Code, logoutResponse.Body.String())
	}
	if cleared := findCookie(logoutResponse.Result().Cookies(), oidcCSRFCookieName); cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("cleared csrf cookie = %#v", cleared)
	}
	reuse := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	reuse.AddCookie(sessionCookie)
	reuseResponse := httptest.NewRecorder()
	handler.ServeHTTP(reuseResponse, reuse)
	if reuseResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked logout session status = %d, body = %s", reuseResponse.Code, reuseResponse.Body.String())
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

func writeOIDCTokenResponse(t *testing.T, w http.ResponseWriter, idToken, refreshToken string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"id_token":      idToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
	}); err != nil {
		t.Fatalf("write token response: %v", err)
	}
}

func loginOIDCTestSession(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	sessionCookie, _ := loginOIDCTestCookies(t, handler)
	return sessionCookie
}

func loginOIDCTestCookies(t *testing.T, handler http.Handler) (*http.Cookie, *http.Cookie) {
	t.Helper()
	login := httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusFound {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body.String())
	}
	location := loginResponse.Header().Get("Location")
	state := ""
	if parsed, err := url.Parse(location); err == nil {
		state = parsed.Query().Get("state")
	}
	if strings.TrimSpace(state) == "" {
		t.Fatalf("login location missing state: %s", location)
	}
	stateCookie := findCookie(loginResponse.Result().Cookies(), oidcStateCookieName)
	if stateCookie == nil {
		t.Fatal("login response missing state cookie")
	}
	callback := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=code-a&state="+url.QueryEscape(state), nil)
	callback.AddCookie(stateCookie)
	callbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body = %s", callbackResponse.Code, callbackResponse.Body.String())
	}
	sessionCookie := findCookie(callbackResponse.Result().Cookies(), oidcSessionCookieName)
	if sessionCookie == nil {
		t.Fatal("callback response missing session cookie")
	}
	csrfCookie := findCookie(callbackResponse.Result().Cookies(), oidcCSRFCookieName)
	if csrfCookie == nil {
		t.Fatal("callback response missing csrf cookie")
	}
	return sessionCookie, csrfCookie
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
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
