package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	codexClientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthBaseURL     = "https://auth.openai.com"
	codexCallbackAddress = "127.0.0.1:1455"
	codexRedirectURI     = "http://localhost:1455/auth/callback"
	codexScope           = "openid profile email offline_access"
	codexJWTClaim        = "https://api.openai.com/auth"
	codexDeviceTimeout   = 15 * time.Minute
)

type DeviceCodeInfo struct {
	UserCode        string
	VerificationURL string
	ExpiresIn       time.Duration
}

type CodexOAuthOptions struct {
	AuthBaseURL     string
	CallbackAddress string
	RedirectURI     string
	HTTPClient      *http.Client
	Now             func() time.Time
	OpenURL         func(string) error
}

type CodexOAuth struct {
	authBaseURL     string
	callbackAddress string
	redirectURI     string
	httpClient      *http.Client
	now             func() time.Time
	openURL         func(string) error
}

func NewCodexOAuth(options CodexOAuthOptions) *CodexOAuth {
	authBaseURL := strings.TrimRight(strings.TrimSpace(options.AuthBaseURL), "/")
	if authBaseURL == "" {
		authBaseURL = codexAuthBaseURL
	}
	callbackAddress := strings.TrimSpace(options.CallbackAddress)
	if callbackAddress == "" {
		callbackAddress = codexCallbackAddress
	}
	redirectURI := strings.TrimSpace(options.RedirectURI)
	if redirectURI == "" && callbackAddress == codexCallbackAddress {
		redirectURI = codexRedirectURI
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	openURL := options.OpenURL
	if openURL == nil {
		openURL = openBrowserURL
	}
	return &CodexOAuth{
		authBaseURL:     authBaseURL,
		callbackAddress: callbackAddress,
		redirectURI:     redirectURI,
		httpClient:      client,
		now:             now,
		openURL:         openURL,
	}
}

func (o *CodexOAuth) LoginBrowser(ctx context.Context, onAuthURL func(string)) (OAuthCredential, error) {
	listener, err := net.Listen("tcp", o.callbackAddress)
	if err != nil {
		return OAuthCredential{}, fmt.Errorf("start Codex OAuth callback: %w", err)
	}
	redirectURI := o.redirectURI
	if redirectURI == "" {
		redirectURI = "http://" + listener.Addr().String() + "/auth/callback"
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		listener.Close()
		return OAuthCredential{}, err
	}
	state, err := randomURLToken(24)
	if err != nil {
		listener.Close()
		return OAuthCredential{}, err
	}

	codeCh := make(chan string, 1)
	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/auth/callback" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("state") != state {
				http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
				return
			}
			code := strings.TrimSpace(r.URL.Query().Get("code"))
			if code == "" {
				http.Error(w, "Missing authorization code", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><title>Liora authentication complete</title><p>Authentication complete. You can close this window.</p>")
			select {
			case codeCh <- code:
			default:
			}
		}),
	}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()
	defer server.Close()

	authorizeURL, err := o.authorizationURL(redirectURI, state, challenge)
	if err != nil {
		return OAuthCredential{}, err
	}
	if onAuthURL != nil {
		onAuthURL(authorizeURL)
	}
	if err := o.openURL(authorizeURL); err != nil && onAuthURL == nil {
		return OAuthCredential{}, fmt.Errorf("open Codex login URL: %w", err)
	}

	select {
	case <-ctx.Done():
		return OAuthCredential{}, ctx.Err()
	case err := <-serveErr:
		return OAuthCredential{}, fmt.Errorf("serve Codex OAuth callback: %w", err)
	case code := <-codeCh:
		return o.exchangeCode(ctx, code, verifier, redirectURI)
	}
}

func (o *CodexOAuth) authorizationURL(redirectURI string, state string, challenge string) (string, error) {
	parsed, err := url.Parse(o.authBaseURL + "/oauth/authorize")
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", codexClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", codexScope)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", "liora")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (o *CodexOAuth) exchangeCode(ctx context.Context, code string, verifier string, redirectURI string) (OAuthCredential, error) {
	return o.requestToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
}

func (o *CodexOAuth) Refresh(ctx context.Context, refreshToken string) (OAuthCredential, error) {
	return o.requestToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	})
}

func (o *CodexOAuth) LoginDevice(ctx context.Context, onDeviceCode func(DeviceCodeInfo)) (OAuthCredential, error) {
	request, err := o.newJSONRequest(ctx, "/api/accounts/deviceauth/usercode", map[string]string{"client_id": codexClientID})
	if err != nil {
		return OAuthCredential{}, err
	}
	response, err := o.httpClient.Do(request)
	if err != nil {
		return OAuthCredential{}, fmt.Errorf("start Codex device login: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return OAuthCredential{}, codexDeviceStatusError("start", response)
	}
	var device struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.NewDecoder(response.Body).Decode(&device); err != nil {
		return OAuthCredential{}, fmt.Errorf("decode Codex device login: %w", err)
	}
	intervalSeconds, err := parseDeviceInterval(device.Interval)
	if err != nil || strings.TrimSpace(device.DeviceAuthID) == "" || strings.TrimSpace(device.UserCode) == "" || intervalSeconds < 0 {
		return OAuthCredential{}, fmt.Errorf("Codex device login response is incomplete")
	}
	if onDeviceCode != nil {
		onDeviceCode(DeviceCodeInfo{
			UserCode:        device.UserCode,
			VerificationURL: o.authBaseURL + "/codex/device",
			ExpiresIn:       codexDeviceTimeout,
		})
	}
	loginCtx, cancel := context.WithTimeout(ctx, codexDeviceTimeout)
	defer cancel()
	interval := time.Duration(intervalSeconds * float64(time.Second))
	for {
		if err := waitForDevicePoll(loginCtx, interval); err != nil {
			return OAuthCredential{}, err
		}
		pollRequest, err := o.newJSONRequest(loginCtx, "/api/accounts/deviceauth/token", map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		})
		if err != nil {
			return OAuthCredential{}, err
		}
		pollResponse, err := o.httpClient.Do(pollRequest)
		if err != nil {
			return OAuthCredential{}, fmt.Errorf("poll Codex device login: %w", err)
		}
		if pollResponse.StatusCode == http.StatusForbidden || pollResponse.StatusCode == http.StatusNotFound {
			pollResponse.Body.Close()
			continue
		}
		if pollResponse.StatusCode < 200 || pollResponse.StatusCode >= 300 {
			err := codexDeviceStatusError("poll", pollResponse)
			pollResponse.Body.Close()
			return OAuthCredential{}, err
		}
		var authorized struct {
			AuthorizationCode string `json:"authorization_code"`
			CodeVerifier      string `json:"code_verifier"`
		}
		err = json.NewDecoder(pollResponse.Body).Decode(&authorized)
		pollResponse.Body.Close()
		if err != nil {
			return OAuthCredential{}, fmt.Errorf("decode Codex device authorization: %w", err)
		}
		if strings.TrimSpace(authorized.AuthorizationCode) == "" || strings.TrimSpace(authorized.CodeVerifier) == "" {
			return OAuthCredential{}, fmt.Errorf("Codex device authorization response is incomplete")
		}
		return o.exchangeCode(loginCtx, authorized.AuthorizationCode, authorized.CodeVerifier, o.authBaseURL+"/deviceauth/callback")
	}
}

func parseDeviceInterval(raw json.RawMessage) (float64, error) {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if value == "" {
		return 0, fmt.Errorf("device polling interval is missing")
	}
	return strconv.ParseFloat(value, 64)
}

func (o *CodexOAuth) newJSONRequest(ctx context.Context, path string, body any) (*http.Request, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.authBaseURL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func codexDeviceStatusError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("Codex device login %s returned %d: %s", operation, response.StatusCode, strings.TrimSpace(string(body)))
}

func waitForDevicePoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (o *CodexOAuth) requestToken(ctx context.Context, form url.Values) (OAuthCredential, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.authBaseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthCredential{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := o.httpClient.Do(request)
	if err != nil {
		return OAuthCredential{}, fmt.Errorf("Codex OAuth token request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return OAuthCredential{}, fmt.Errorf("Codex OAuth token request returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		return OAuthCredential{}, fmt.Errorf("decode Codex OAuth token: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" || token.ExpiresIn <= 0 {
		return OAuthCredential{}, fmt.Errorf("Codex OAuth token response is incomplete")
	}
	accountID, err := codexAccountID(token.AccessToken)
	if err != nil {
		return OAuthCredential{}, err
	}
	return OAuthCredential{
		Access:    token.AccessToken,
		Refresh:   token.RefreshToken,
		ExpiresAt: o.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
		AccountID: accountID,
	}, nil
}

func codexAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("Codex OAuth access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode Codex OAuth access token: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("decode Codex OAuth claims: %w", err)
	}
	var authClaim struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(claims[codexJWTClaim], &authClaim); err != nil || strings.TrimSpace(authClaim.AccountID) == "" {
		return "", fmt.Errorf("Codex OAuth token is missing ChatGPT account ID")
	}
	return strings.TrimSpace(authClaim.AccountID), nil
}

func generatePKCE() (string, string, error) {
	verifier, err := randomURLToken(64)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func randomURLToken(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func openBrowserURL(raw string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", raw)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		command = exec.Command("xdg-open", raw)
	}
	return command.Start()
}
