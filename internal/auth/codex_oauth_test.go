package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCodexOAuthBrowserLoginExchangesPKCECodeAndExtractsAccount(t *testing.T) {
	var tokenForm url.Values
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected auth path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		tokenForm = make(url.Values, len(r.PostForm))
		for key, values := range r.PostForm {
			tokenForm[key] = append([]string(nil), values...)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  testCodexJWT(t, "account-123"),
			"refresh_token": "refresh-token",
			"expires_in":    3600,
		})
	}))
	defer authServer.Close()

	now := time.Unix(1_800_000_000, 0).UTC()
	oauth := NewCodexOAuth(CodexOAuthOptions{
		AuthBaseURL:     authServer.URL,
		CallbackAddress: "127.0.0.1:0",
		HTTPClient:      authServer.Client(),
		Now:             func() time.Time { return now },
		OpenURL: func(raw string) error {
			authorizeURL, err := url.Parse(raw)
			if err != nil {
				return err
			}
			if authorizeURL.Path != "/oauth/authorize" {
				t.Fatalf("unexpected authorize path %s", authorizeURL.Path)
			}
			if authorizeURL.Query().Get("code_challenge_method") != "S256" || authorizeURL.Query().Get("state") == "" {
				t.Fatalf("missing PKCE/state in %s", raw)
			}
			callback, err := url.Parse(authorizeURL.Query().Get("redirect_uri"))
			if err != nil {
				return err
			}
			query := callback.Query()
			query.Set("code", "authorization-code")
			query.Set("state", authorizeURL.Query().Get("state"))
			callback.RawQuery = query.Encode()
			go func() {
				_, _ = http.Get(callback.String())
			}()
			return nil
		},
	})

	credential, err := oauth.LoginBrowser(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "account-123" || credential.Refresh != "refresh-token" || credential.Access == "" {
		t.Fatalf("unexpected credential %#v", credential)
	}
	if !credential.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires_at=%s want=%s", credential.ExpiresAt, now.Add(time.Hour))
	}
	for key, want := range map[string]string{
		"grant_type": "authorization_code",
		"client_id":  codexClientID,
		"code":       "authorization-code",
	} {
		if got := tokenForm.Get(key); got != want || got == "" {
			t.Fatalf("token form %s=%q want=%q", key, got, want)
		}
	}
	if strings.TrimSpace(tokenForm.Get("code_verifier")) == "" {
		t.Fatal("token exchange omitted PKCE verifier")
	}
	if redirect := tokenForm.Get("redirect_uri"); !strings.HasPrefix(redirect, "http://127.0.0.1:") || !strings.HasSuffix(redirect, "/auth/callback") {
		t.Fatalf("unexpected redirect_uri %q", redirect)
	}
}

func TestCodexOAuthDeviceLoginReportsCodeAndPollsUntilAuthorized(t *testing.T) {
	polls := 0
	var tokenRedirect string
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"device_auth_id":"device-1","user_code":"ABCD-EFGH","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"authorization_code":"device-auth-code","code_verifier":"device-verifier"}`)
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			tokenRedirect = r.Form.Get("redirect_uri")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  testCodexJWT(t, "device-account"),
				"refresh_token": "device-refresh",
				"expires_in":    3600,
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer authServer.Close()
	oauth := NewCodexOAuth(CodexOAuthOptions{AuthBaseURL: authServer.URL, HTTPClient: authServer.Client()})
	var gotInfo DeviceCodeInfo

	credential, err := oauth.LoginDevice(context.Background(), func(info DeviceCodeInfo) { gotInfo = info })
	if err != nil {
		t.Fatal(err)
	}
	if gotInfo.UserCode != "ABCD-EFGH" || gotInfo.VerificationURL != authServer.URL+"/codex/device" {
		t.Fatalf("unexpected device code info %#v", gotInfo)
	}
	if polls != 2 || credential.AccountID != "device-account" || credential.Refresh != "device-refresh" {
		t.Fatalf("unexpected device flow polls=%d credential=%#v", polls, credential)
	}
	if tokenRedirect != authServer.URL+"/deviceauth/callback" {
		t.Fatalf("unexpected device token redirect %q", tokenRedirect)
	}
}

func testCodexJWT(t *testing.T, accountID string) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": accountID},
	})
	encode := base64.RawURLEncoding.EncodeToString
	return encode(header) + "." + encode(payload) + ".signature"
}
