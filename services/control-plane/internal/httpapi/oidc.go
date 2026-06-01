package httpapi

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"niceagent/control-plane/internal/app"
)

type OIDCConfig struct {
	Issuer           string
	Audience         string
	JWKSURL          string
	DefaultProjectID string
	DefaultOrgID     string
	UserIDClaim      string
	ProjectIDClaim   string
	OrgIDClaim       string
	RolesClaim       string
	EmailClaim       string
	NameClaim        string
	HTTPClient       *http.Client
	Now              func() time.Time
}

type OIDCVerifier struct {
	cfg    OIDCConfig
	client *http.Client
	now    func() time.Time

	mu     sync.Mutex
	keys   map[string]*rsa.PublicKey
	loaded time.Time
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type oidcClaims struct {
	Issuer    string         `json:"iss"`
	Subject   string         `json:"sub"`
	Aud       any            `json:"aud"`
	Expires   int64          `json:"exp"`
	NotBefore int64          `json:"nbf"`
	IssuedAt  int64          `json:"iat"`
	Raw       map[string]any `json:"-"`
}

func NewOIDCVerifier(cfg OIDCConfig) (*OIDCVerifier, error) {
	cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	cfg.Audience = strings.TrimSpace(cfg.Audience)
	cfg.JWKSURL = strings.TrimSpace(cfg.JWKSURL)
	if cfg.JWKSURL == "" && cfg.Issuer != "" {
		cfg.JWKSURL = cfg.Issuer + "/.well-known/jwks.json"
	}
	cfg.UserIDClaim = firstNonEmpty(cfg.UserIDClaim, "sub")
	cfg.ProjectIDClaim = firstNonEmpty(cfg.ProjectIDClaim, "niceagent_project_id")
	cfg.OrgIDClaim = firstNonEmpty(cfg.OrgIDClaim, "niceagent_org_id")
	cfg.RolesClaim = firstNonEmpty(cfg.RolesClaim, "niceagent_roles")
	cfg.EmailClaim = firstNonEmpty(cfg.EmailClaim, "email")
	cfg.NameClaim = firstNonEmpty(cfg.NameClaim, "name")
	if cfg.Issuer == "" {
		return nil, errors.New("OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
	}
	if cfg.Audience == "" {
		return nil, errors.New("OIDC_AUDIENCE is required when AUTH_MODE=oidc")
	}
	if cfg.JWKSURL == "" {
		return nil, errors.New("OIDC_JWKS_URL is required when AUTH_MODE=oidc")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &OIDCVerifier{
		cfg:    cfg,
		client: client,
		now:    now,
		keys:   map[string]*rsa.PublicKey{},
	}, nil
}

func (v *OIDCVerifier) ActorFromBearer(ctx context.Context, token string) (app.ActorContext, error) {
	header, claims, signingInput, signature, err := parseJWT(token)
	if err != nil {
		return app.ActorContext{}, err
	}
	if header.Algorithm != "RS256" {
		return app.ActorContext{}, fmt.Errorf("unsupported OIDC token alg %q", header.Algorithm)
	}
	key, err := v.keyForToken(ctx, header.KeyID)
	if err != nil {
		return app.ActorContext{}, err
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		_ = v.refreshKeys(ctx)
		key, keyErr := v.keyForToken(ctx, header.KeyID)
		if keyErr != nil {
			return app.ActorContext{}, err
		}
		if retryErr := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); retryErr != nil {
			return app.ActorContext{}, err
		}
	}
	if err := v.validateClaims(claims); err != nil {
		return app.ActorContext{}, err
	}
	userID := stringClaim(claims.Raw, v.cfg.UserIDClaim)
	if userID == "" {
		userID = claims.Subject
	}
	projectID := firstNonEmpty(stringClaim(claims.Raw, v.cfg.ProjectIDClaim), v.cfg.DefaultProjectID)
	if userID == "" || projectID == "" {
		return app.ActorContext{}, errors.New("OIDC token must include user and project claims")
	}
	return app.ActorContext{
		UserID:           userID,
		Email:            stringClaim(claims.Raw, v.cfg.EmailClaim),
		Name:             stringClaim(claims.Raw, v.cfg.NameClaim),
		IdentityProvider: "oidc",
		IdentityIssuer:   claims.Issuer,
		IdentitySubject:  claims.Subject,
		ProjectID:        projectID,
		OrgID:            firstNonEmpty(stringClaim(claims.Raw, v.cfg.OrgIDClaim), v.cfg.DefaultOrgID),
		Roles:            rolesClaim(claims.Raw, v.cfg.RolesClaim),
	}, nil
}

func parseJWT(token string) (jwtHeader, oidcClaims, string, []byte, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return jwtHeader{}, oidcClaims{}, "", nil, errors.New("invalid OIDC token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtHeader{}, oidcClaims{}, "", nil, errors.New("invalid OIDC token header")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtHeader{}, oidcClaims{}, "", nil, errors.New("invalid OIDC token payload")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtHeader{}, oidcClaims{}, "", nil, errors.New("invalid OIDC token signature")
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return jwtHeader{}, oidcClaims{}, "", nil, errors.New("invalid OIDC token header JSON")
	}
	rawClaims := map[string]any{}
	if err := json.Unmarshal(payloadBytes, &rawClaims); err != nil {
		return jwtHeader{}, oidcClaims{}, "", nil, errors.New("invalid OIDC token payload JSON")
	}
	claims := oidcClaims{
		Issuer:    stringClaim(rawClaims, "iss"),
		Subject:   stringClaim(rawClaims, "sub"),
		Aud:       rawClaims["aud"],
		Expires:   int64Claim(rawClaims, "exp"),
		NotBefore: int64Claim(rawClaims, "nbf"),
		IssuedAt:  int64Claim(rawClaims, "iat"),
		Raw:       rawClaims,
	}
	return header, claims, parts[0] + "." + parts[1], signature, nil
}

func (v *OIDCVerifier) validateClaims(claims oidcClaims) error {
	if claims.Issuer != v.cfg.Issuer {
		return errors.New("OIDC token issuer mismatch")
	}
	if claims.Subject == "" {
		return errors.New("OIDC token subject is required")
	}
	now := v.now().Unix()
	const skewSeconds = int64(60)
	if claims.Expires == 0 || now > claims.Expires+skewSeconds {
		return errors.New("OIDC token expired")
	}
	if claims.NotBefore > 0 && now+skewSeconds < claims.NotBefore {
		return errors.New("OIDC token not yet valid")
	}
	if !audienceContains(claims.Aud, v.cfg.Audience) {
		return errors.New("OIDC token audience mismatch")
	}
	return nil
}

func (v *OIDCVerifier) keyForToken(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	key := keyFromMap(v.keys, kid)
	stale := time.Since(v.loaded) > 10*time.Minute
	v.mu.Unlock()
	if key != nil && !stale {
		return key, nil
	}
	if err := v.refreshKeys(ctx); err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	key = keyFromMap(v.keys, kid)
	if key == nil {
		return nil, errors.New("OIDC signing key not found")
	}
	return key, nil
}

func (v *OIDCVerifier) refreshKeys(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OIDC JWKS request failed with status %d", response.StatusCode)
	}
	var jwks struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			KeyType   string `json:"kty"`
			Algorithm string `json:"alg"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(response.Body).Decode(&jwks); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, jwk := range jwks.Keys {
		if jwk.KeyType != "RSA" || jwk.Modulus == "" || jwk.Exponent == "" {
			continue
		}
		key, err := rsaKeyFromJWK(jwk.Modulus, jwk.Exponent)
		if err != nil {
			continue
		}
		keys[jwk.KeyID] = key
	}
	if len(keys) == 0 {
		return errors.New("OIDC JWKS did not contain RSA signing keys")
	}
	v.mu.Lock()
	v.keys = keys
	v.loaded = v.now()
	v.mu.Unlock()
	return nil
}

func rsaKeyFromJWK(modulus, exponent string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	if e == 0 {
		return nil, errors.New("invalid RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func keyFromMap(keys map[string]*rsa.PublicKey, kid string) *rsa.PublicKey {
	if kid != "" {
		return keys[kid]
	}
	if len(keys) == 1 {
		for _, key := range keys {
			return key
		}
	}
	return nil
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return ""
	}
	prefix := "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func stringClaim(claims map[string]any, key string) string {
	value, ok := claims[strings.TrimSpace(key)]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func int64Claim(claims map[string]any, key string) int64 {
	value, ok := claims[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		n, _ := typed.Int64()
		return n
	default:
		return 0
	}
}

func audienceContains(raw any, expected string) bool {
	switch aud := raw.(type) {
	case string:
		return aud == expected
	case []any:
		for _, item := range aud {
			if s, ok := item.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}

func rolesClaim(claims map[string]any, key string) []string {
	value, ok := claims[strings.TrimSpace(key)]
	if !ok && key != "roles" {
		value, ok = claims["roles"]
	}
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case string:
		return splitCSV(typed)
	case []any:
		roles := make([]string, 0, len(typed))
		for _, item := range typed {
			role, ok := item.(string)
			if !ok {
				continue
			}
			role = strings.TrimSpace(role)
			if role != "" {
				roles = append(roles, role)
			}
		}
		return roles
	default:
		return nil
	}
}
