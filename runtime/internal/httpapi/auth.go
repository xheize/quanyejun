package httpapi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const sessionCookie = "quanyejun_session"
const flowCookie = "quanyejun_login"

type AuthConfig struct {
	Issuer, ClientID, ClientSecret, RedirectURL, WebURL string
	Key                                                 []byte
	Secure                                              bool
}

// A persisted local key keeps anonymous browser identities stable across restarts.
// SSO client secrets and access tokens never reach the frontend.
func AuthConfigFromEnvironment(keyPath string) (AuthConfig, error) {
	c := AuthConfig{Issuer: strings.TrimRight(os.Getenv("SSO_ISSUER_URL"), "/"), ClientID: os.Getenv("SSO_CLIENT_ID"), ClientSecret: os.Getenv("SSO_CLIENT_SECRET"), RedirectURL: os.Getenv("SSO_REDIRECT_URL"), WebURL: os.Getenv("WEB_PUBLIC_URL")}
	if c.WebURL == "" {
		c.WebURL = "http://localhost:3000"
	}
	if c.RedirectURL == "" {
		c.RedirectURL = "http://localhost:8080/auth/callback"
	}
	c.Secure = strings.HasPrefix(c.RedirectURL, "https://")
	if raw := os.Getenv("SESSION_ENCRYPTION_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return c, errors.New("SESSION_ENCRYPTION_KEY must be base64 encoding of 32 random bytes")
		}
		c.Key = key
	} else {
		key, err := os.ReadFile(keyPath)
		if os.IsNotExist(err) {
			if err = os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
				return c, err
			}
			key = make([]byte, 32)
			if _, err = rand.Read(key); err != nil {
				return c, err
			}
			var file *os.File
			file, err = os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				_, err = file.Write(key)
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			} else if os.IsExist(err) {
				key, err = os.ReadFile(keyPath)
			}
		}
		if err != nil {
			return c, err
		}
		if len(key) != 32 {
			return c, errors.New("invalid persisted session key")
		}
		c.Key = key
	}
	return c, nil
}

type Principal struct {
	ActorID      string `json:"id"`
	Role         string `json:"role"`
	LoginEnabled bool   `json:"loginEnabled"`
}
type browserSession struct {
	ActorID     string `json:"actor"`
	AccessToken string `json:"token,omitempty"`
	Expires     int64  `json:"expires"`
}
type loginFlow struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Expires  int64  `json:"expires"`
}
type Auth struct {
	config AuthConfig
	aead   cipher.AEAD
	client *http.Client
}

// Bind the configured identity namespace to the issuer advertised by SSO.
// In particular, a proxy alias must not silently identify users as another issuer.
func (a *Auth) ValidateIssuer(ctx context.Context) error {
	if a.config.Issuer == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.config.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return errors.New("invalid SSO discovery URL")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return errors.New("SSO discovery unavailable")
	}
	defer res.Body.Close()
	var metadata struct {
		Issuer        string `json:"issuer"`
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
		UserInfo      string `json:"userinfo_endpoint"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&metadata) != nil {
		return errors.New("invalid SSO discovery response")
	}
	if metadata.Issuer != a.config.Issuer {
		return errors.New("SSO_ISSUER_URL does not match the advertised issuer; align the SSO public URL and client configuration")
	}
	if metadata.Authorization != a.config.Issuer+"/oauth2/auth" || metadata.Token != a.config.Issuer+"/oauth2/token" || metadata.UserInfo != a.config.Issuer+"/oauth2/userinfo" {
		return errors.New("SSO endpoints do not match the configured sso-server contract")
	}
	return nil
}

func NewAuth(config AuthConfig) (*Auth, error) {
	if len(config.Key) != 32 {
		return nil, errors.New("session encryption key must be 32 bytes")
	}
	if (config.Issuer == "") != (config.ClientID == "") {
		return nil, errors.New("SSO_ISSUER_URL and SSO_CLIENT_ID must be configured together")
	}
	for _, raw := range []string{config.WebURL, config.RedirectURL, config.Issuer} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return nil, fmt.Errorf("invalid public authentication URL")
		}
		local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return nil, errors.New("authentication URLs require HTTPS except on loopback")
		}
	}
	if config.Issuer != "" && (config.RedirectURL == "" || config.WebURL == "") {
		return nil, errors.New("SSO requires redirect and web URLs")
	}
	block, err := aes.NewCipher(config.Key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Auth{config: config, aead: aead, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *Auth) seal(name string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(a.aead.Seal(nonce, nonce, data, []byte(name))), nil
}
func (a *Auth) open(c *gin.Context, name string, value any) bool {
	raw, err := c.Cookie(name)
	if err != nil || len(raw) > 4096 {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) < a.aead.NonceSize() {
		return false
	}
	n := a.aead.NonceSize()
	plain, err := a.aead.Open(nil, data[:n], data[n:], []byte(name))
	return err == nil && json.Unmarshal(plain, value) == nil
}
func (a *Auth) setCookie(c *gin.Context, name string, value any, age int) error {
	raw, err := a.seal(name, value)
	if err != nil {
		return err
	}
	if len(raw) > 3800 {
		return errors.New("session payload too large")
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: raw, Path: "/", HttpOnly: true, Secure: a.config.Secure, SameSite: http.SameSiteLaxMode, MaxAge: age})
	return nil
}
func (a *Auth) clear(c *gin.Context, name string) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: a.config.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (a *Auth) identify(c *gin.Context) {
	var session browserSession
	valid := a.open(c, sessionCookie, &session) && session.Expires > time.Now().Unix()
	role := "anonymous"
	if valid && session.AccessToken != "" {
		// Revalidate with SSO so revoked or blocked users cannot keep using a local cookie.
		subject, err := a.userInfo(c, session.AccessToken)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "SSO session unavailable or expired; log in again"})
			return
		}
		if a.actorFor(subject) != session.ActorID {
			c.AbortWithStatus(401)
			return
		}
		role = "authenticated"
	} else if !valid || !strings.HasPrefix(session.ActorID, "anon:") {
		session = browserSession{ActorID: "anon:" + uuid.NewString(), Expires: time.Now().Add(90 * 24 * time.Hour).Unix()}
		if err := a.setCookie(c, sessionCookie, session, 90*86400); err != nil {
			c.AbortWithStatus(500)
			return
		}
	}
	c.Set("principal", Principal{ActorID: session.ActorID, Role: role, LoginEnabled: a.config.Issuer != ""})
	c.Set("sessionExpires", session.Expires)
	c.Next()
}

func (a *Auth) actorFor(subject string) string {
	digest := sha256.Sum256([]byte(a.config.Issuer + "\x00" + subject))
	return "sso:" + hex.EncodeToString(digest[:])
}
func (a *Auth) userInfo(c *gin.Context, token string) (string, error) {
	req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, a.config.Issuer+"/oauth2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var data struct {
		Sub string `json:"sub"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&data) != nil || data.Sub == "" {
		return "", errors.New("invalid SSO user info")
	}
	return data.Sub, nil
}
func (a *Auth) login(c *gin.Context) {
	if a.config.Issuer == "" {
		c.JSON(503, gin.H{"error": "SSO is not configured"})
		return
	}
	flow := loginFlow{State: uuid.NewString(), Verifier: uuid.NewString() + uuid.NewString(), Expires: time.Now().Add(10 * time.Minute).Unix()}
	if err := a.setCookie(c, flowCookie, flow, 600); err != nil {
		c.Status(500)
		return
	}
	challenge := sha256.Sum256([]byte(flow.Verifier))
	q := url.Values{"client_id": {a.config.ClientID}, "redirect_uri": {a.config.RedirectURL}, "response_type": {"code"}, "scope": {"openid"}, "state": {flow.State}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	c.Redirect(http.StatusFound, a.config.Issuer+"/oauth2/auth?"+q.Encode())
}
func (a *Auth) callback(c *gin.Context) {
	var flow loginFlow
	valid := a.open(c, flowCookie, &flow)
	a.clear(c, flowCookie)
	if a.config.Issuer == "" || !valid || flow.Expires <= time.Now().Unix() || flow.State != c.Query("state") || c.Query("code") == "" || c.Query("error") != "" {
		c.JSON(400, gin.H{"error": "invalid or expired login flow"})
		return
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {a.config.ClientID}, "redirect_uri": {a.config.RedirectURL}, "code": {c.Query("code")}, "code_verifier": {flow.Verifier}}
	req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, a.config.Issuer+"/oauth2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if a.config.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(a.config.ClientID), url.QueryEscape(a.config.ClientSecret))
	}
	res, err := a.client.Do(req)
	if err != nil {
		c.Status(502)
		return
	}
	defer res.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&token) != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || token.ExpiresIn <= 0 {
		c.JSON(502, gin.H{"error": "SSO token exchange failed"})
		return
	}
	sub, err := a.userInfo(c, token.AccessToken)
	if err != nil {
		c.Status(502)
		return
	}
	age := min(token.ExpiresIn, 1800)
	session := browserSession{ActorID: a.actorFor(sub), AccessToken: token.AccessToken, Expires: time.Now().Add(time.Duration(age) * time.Second).Unix()}
	if err := a.setCookie(c, sessionCookie, session, age); err != nil {
		c.Status(500)
		return
	}
	// Anonymous history is not promoted into persistent SSO history.
	c.Redirect(http.StatusSeeOther, a.config.WebURL)
}
func (a *Auth) logout(c *gin.Context) {
	a.clear(c, sessionCookie)
	a.clear(c, flowCookie)
	c.Status(http.StatusNoContent)
}

func principal(c *gin.Context) Principal {
	value, _ := c.Get("principal")
	p, _ := value.(Principal)
	return p
}
