package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/control-plane/internal/app"
)

const (
	oidcSessionCookieName = "niceagent_session"
	oidcStateCookieName   = "niceagent_oidc_state"
	oidcCSRFCookieName    = "niceagent_csrf"
	oidcCSRFHeaderName    = "X-NiceAgent-CSRF"
)

type OIDCBrowserConfig struct {
	ClientID          string
	ClientSecret      string
	AuthURL           string
	TokenURL          string
	RedirectURL       string
	SessionSecret     string
	SessionTTLSeconds int
	HTTPClient        *http.Client
	Now               func() time.Time
}

type oidcTokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

type oidcSessionPayload struct {
	SessionID    string           `json:"session_id"`
	Actor        app.ActorContext `json:"actor"`
	RefreshToken string           `json:"refresh_token,omitempty"`
	CSRFToken    string           `json:"csrf_token,omitempty"`
	ExpiresAt    int64            `json:"expires_at"`
}

func normalizeOIDCBrowserConfig(cfg OIDCBrowserConfig) OIDCBrowserConfig {
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.AuthURL = strings.TrimSpace(cfg.AuthURL)
	cfg.TokenURL = strings.TrimSpace(cfg.TokenURL)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	if cfg.SessionTTLSeconds <= 0 {
		cfg.SessionTTLSeconds = 12 * 60 * 60
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return cfg
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if !s.oidcBrowserConfigured() {
		platform.WriteError(w, http.StatusNotFound, "OIDC browser login is not configured")
		return
	}
	state, err := randomURLToken(24)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to create state")
		return
	}
	setCookie(w, r, oidcStateCookieName, state, 10*time.Minute)
	authURL, err := url.Parse(s.oidcBrowser.AuthURL)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "invalid OIDC auth URL")
		return
	}
	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", s.oidcBrowser.ClientID)
	query.Set("redirect_uri", s.oidcRedirectURL(r))
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	authURL.RawQuery = query.Encode()
	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if !s.oidcBrowserConfigured() {
		platform.WriteError(w, http.StatusNotFound, "OIDC browser login is not configured")
		return
	}
	if r.URL.Query().Get("error") != "" {
		platform.WriteError(w, http.StatusUnauthorized, "OIDC login failed")
		return
	}
	if !s.verifyOIDCState(r) {
		platform.WriteError(w, http.StatusUnauthorized, "invalid OIDC state")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		platform.WriteError(w, http.StatusBadRequest, "missing OIDC code")
		return
	}
	token, err := s.exchangeOIDCToken(r.Context(), map[string]string{
		"grant_type":   "authorization_code",
		"code":         code,
		"redirect_uri": s.oidcRedirectURL(r),
	})
	if err != nil {
		platform.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	actor, err := s.actorFromOIDCIDToken(r.Context(), token.IDToken)
	if err != nil {
		platform.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err := s.setOIDCSession(w, r, actor, token.RefreshToken); err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	clearCookie(w, r, oidcStateCookieName)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) oidcRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.oidcBrowserConfigured() {
		platform.WriteError(w, http.StatusNotFound, "OIDC browser login is not configured")
		return
	}
	session, err := s.oidcSessionFromRequest(r)
	if err != nil {
		platform.WriteError(w, http.StatusUnauthorized, "missing OIDC session")
		return
	}
	if !s.verifyOIDCCSRF(r, session) {
		platform.WriteError(w, http.StatusForbidden, "invalid OIDC CSRF token")
		return
	}
	if strings.TrimSpace(session.RefreshToken) == "" {
		platform.WriteError(w, http.StatusUnauthorized, "OIDC refresh token is not available")
		return
	}
	token, err := s.exchangeOIDCToken(r.Context(), map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": session.RefreshToken,
	})
	if err != nil {
		platform.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	actor := session.Actor
	if strings.TrimSpace(token.IDToken) != "" {
		actor, err = s.actorFromOIDCIDToken(r.Context(), token.IDToken)
		if err != nil {
			platform.WriteError(w, http.StatusUnauthorized, err.Error())
			return
		}
	}
	refreshToken := firstNonEmpty(token.RefreshToken, session.RefreshToken)
	if err := s.repo.RevokeOIDCBrowserSession(session.SessionID, s.oidcBrowser.Now().UTC()); err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to revoke old session")
		return
	}
	if err := s.setOIDCSession(w, r, actor, refreshToken); err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to refresh session")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"status": "refreshed"})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if session, err := s.oidcSessionFromRequest(r); err == nil {
		if !s.verifyOIDCCSRF(r, session) {
			platform.WriteError(w, http.StatusForbidden, "invalid OIDC CSRF token")
			return
		}
		if err := s.repo.RevokeOIDCBrowserSession(session.SessionID, s.oidcBrowser.Now().UTC()); err != nil {
			platform.WriteError(w, http.StatusInternalServerError, "failed to revoke session")
			return
		}
	}
	clearCookie(w, r, oidcSessionCookieName)
	clearCookie(w, r, oidcStateCookieName)
	clearCookie(w, r, oidcCSRFCookieName)
	platform.WriteJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}

func (s *Server) oidcBrowserConfigured() bool {
	return s.authMode == "oidc" &&
		s.oidcVerifier != nil &&
		strings.TrimSpace(s.oidcBrowser.AuthURL) != "" &&
		strings.TrimSpace(s.oidcBrowser.TokenURL) != "" &&
		strings.TrimSpace(s.oidcBrowser.ClientID) != "" &&
		strings.TrimSpace(s.oidcBrowser.SessionSecret) != ""
}

func (s *Server) actorFromOIDCIDToken(ctx context.Context, idToken string) (app.ActorContext, error) {
	if s.oidcVerifier == nil {
		return app.ActorContext{}, errors.New("OIDC verifier is not configured")
	}
	if strings.TrimSpace(idToken) == "" {
		return app.ActorContext{}, errors.New("OIDC token response did not include id_token")
	}
	return s.oidcVerifier.ActorFromBearer(ctx, idToken)
}

func (s *Server) exchangeOIDCToken(ctx context.Context, fields map[string]string) (oidcTokenResponse, error) {
	form := url.Values{}
	for key, value := range fields {
		form.Set(key, value)
	}
	form.Set("client_id", s.oidcBrowser.ClientID)
	if strings.TrimSpace(s.oidcBrowser.ClientSecret) != "" {
		form.Set("client_secret", s.oidcBrowser.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.oidcBrowser.TokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.oidcBrowser.HTTPClient.Do(request)
	if err != nil {
		return oidcTokenResponse{}, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var token oidcTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oidcTokenResponse{}, errors.New("OIDC token response is invalid")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || token.Error != "" {
		reason := firstNonEmpty(token.ErrorDesc, token.Error, fmt.Sprintf("OIDC token endpoint failed with status %d", response.StatusCode))
		return oidcTokenResponse{}, errors.New(reason)
	}
	return token, nil
}

func (s *Server) setOIDCSession(w http.ResponseWriter, r *http.Request, actor app.ActorContext, refreshToken string) error {
	expiresAt := s.oidcBrowser.Now().UTC().Add(time.Duration(s.oidcBrowser.SessionTTLSeconds) * time.Second)
	sessionID, err := randomURLToken(24)
	if err != nil {
		return err
	}
	csrfToken, err := randomURLToken(24)
	if err != nil {
		return err
	}
	payload := oidcSessionPayload{
		SessionID:    sessionID,
		Actor:        actor,
		RefreshToken: strings.TrimSpace(refreshToken),
		CSRFToken:    csrfToken,
		ExpiresAt:    expiresAt.Unix(),
	}
	if err := s.repo.UpsertOIDCBrowserSession(app.OIDCBrowserSession{
		ID:               sessionID,
		UserID:           actor.UserID,
		ProjectID:        actor.ProjectID,
		RefreshTokenHash: oidcTokenHash(refreshToken),
		CreatedAt:        s.oidcBrowser.Now().UTC(),
		UpdatedAt:        s.oidcBrowser.Now().UTC(),
		ExpiresAt:        expiresAt,
	}); err != nil {
		return err
	}
	value, err := s.signOIDCSession(payload)
	if err != nil {
		return err
	}
	setCookie(w, r, oidcSessionCookieName, value, time.Duration(s.oidcBrowser.SessionTTLSeconds)*time.Second)
	setOIDCCSRFCookie(w, r, csrfToken, time.Duration(s.oidcBrowser.SessionTTLSeconds)*time.Second)
	return nil
}

func (s *Server) oidcSessionFromRequest(r *http.Request) (oidcSessionPayload, error) {
	cookie, err := r.Cookie(oidcSessionCookieName)
	if err != nil {
		return oidcSessionPayload{}, err
	}
	return s.verifyOIDCSession(cookie.Value)
}

func (s *Server) signOIDCSession(payload oidcSessionPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(s.oidcBrowser.SessionSecret))
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (s *Server) verifyOIDCSession(value string) (oidcSessionPayload, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 2 {
		return oidcSessionPayload{}, errors.New("invalid OIDC session")
	}
	mac := hmac.New(sha256.New, []byte(s.oidcBrowser.SessionSecret))
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(got, expected) {
		return oidcSessionPayload{}, errors.New("invalid OIDC session signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oidcSessionPayload{}, err
	}
	var payload oidcSessionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return oidcSessionPayload{}, err
	}
	if payload.Actor.UserID == "" || payload.Actor.ProjectID == "" {
		return oidcSessionPayload{}, errors.New("invalid OIDC session actor")
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return oidcSessionPayload{}, errors.New("invalid OIDC session id")
	}
	if strings.TrimSpace(payload.CSRFToken) == "" {
		return oidcSessionPayload{}, errors.New("invalid OIDC session csrf token")
	}
	if s.oidcBrowser.Now().Unix() > payload.ExpiresAt {
		return oidcSessionPayload{}, errors.New("OIDC session expired")
	}
	if !s.repo.IsOIDCBrowserSessionActive(payload.SessionID, payload.Actor.UserID, s.oidcBrowser.Now().UTC()) {
		return oidcSessionPayload{}, errors.New("OIDC session revoked")
	}
	return payload, nil
}

func (s *Server) verifyOIDCCSRF(r *http.Request, session oidcSessionPayload) bool {
	headerToken := strings.TrimSpace(r.Header.Get(oidcCSRFHeaderName))
	if headerToken == "" {
		return false
	}
	cookie, err := r.Cookie(oidcCSRFCookieName)
	if err != nil {
		return false
	}
	cookieToken := strings.TrimSpace(cookie.Value)
	sessionToken := strings.TrimSpace(session.CSRFToken)
	return cookieToken != "" &&
		sessionToken != "" &&
		hmac.Equal([]byte(headerToken), []byte(cookieToken)) &&
		hmac.Equal([]byte(headerToken), []byte(sessionToken))
}

func (s *Server) verifyOIDCState(r *http.Request) bool {
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil {
		return false
	}
	got := strings.TrimSpace(r.URL.Query().Get("state"))
	want := strings.TrimSpace(cookie.Value)
	return got != "" && want != "" && hmac.Equal([]byte(got), []byte(want))
}

func (s *Server) oidcRedirectURL(r *http.Request) string {
	if strings.TrimSpace(s.oidcBrowser.RedirectURL) != "" {
		return s.oidcBrowser.RedirectURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/auth/oidc/callback"
}

func randomURLToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func oidcTokenHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}

func setOIDCCSRFCookie(w http.ResponseWriter, r *http.Request, value string, maxAge time.Duration) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCSRFCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
