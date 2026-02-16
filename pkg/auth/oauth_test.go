package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildAuthorizeURL(t *testing.T) {
	cfg := OAuthProviderConfig{
		Issuer:     "https://auth.example.com",
		ClientID:   "test-client-id",
		Scopes:     "openid profile",
		Originator: "codex_cli_rs",
		Port:       1455,
		Provider:   "openai",
	}
	pkce := PKCECodes{
		CodeVerifier:  "test-verifier",
		CodeChallenge: "test-challenge",
	}

	u := BuildAuthorizeURL(cfg, pkce, "test-state", "http://localhost:1455/auth/callback")

	if !strings.HasPrefix(u, "https://auth.example.com/oauth/authorize?") {
		t.Errorf("URL does not start with expected prefix: %s", u)
	}
	if !strings.Contains(u, "client_id=test-client-id") {
		t.Error("URL missing client_id")
	}
	if !strings.Contains(u, "code_challenge=test-challenge") {
		t.Error("URL missing code_challenge")
	}
	if !strings.Contains(u, "code_challenge_method=S256") {
		t.Error("URL missing code_challenge_method")
	}
	if !strings.Contains(u, "state=test-state") {
		t.Error("URL missing state")
	}
	if !strings.Contains(u, "response_type=code") {
		t.Error("URL missing response_type")
	}
	if !strings.Contains(u, "id_token_add_organizations=true") {
		t.Error("URL missing id_token_add_organizations")
	}
	if !strings.Contains(u, "codex_cli_simplified_flow=true") {
		t.Error("URL missing codex_cli_simplified_flow")
	}
	if !strings.Contains(u, "originator=codex_cli_rs") {
		t.Error("URL missing originator")
	}
}

func TestBuildAuthorizeURLAnthropic(t *testing.T) {
	cfg := AnthropicOAuthConfig()
	cfg.Issuer = "https://console.example.com"       // override for test
	cfg.AuthorizeBaseURL = "https://claude.example.com" // override for test
	pkce := PKCECodes{
		CodeVerifier:  "test-verifier",
		CodeChallenge: "test-challenge",
	}

	u := BuildAuthorizeURL(cfg, pkce, "test-state", "https://console.example.com/oauth/code/callback")

	// Should use AuthorizeBaseURL, not Issuer
	if !strings.HasPrefix(u, "https://claude.example.com/oauth/authorize?") {
		t.Errorf("URL does not start with expected prefix: %s", u)
	}
	if !strings.Contains(u, "client_id="+cfg.ClientID) {
		t.Error("URL missing client_id")
	}
	if !strings.Contains(u, "scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainference") {
		t.Errorf("URL missing correct scopes: %s", u)
	}
	// Anthropic should NOT have OpenAI-specific params
	if strings.Contains(u, "id_token_add_organizations") {
		t.Error("Anthropic URL should not contain id_token_add_organizations")
	}
	if strings.Contains(u, "codex_cli_simplified_flow") {
		t.Error("Anthropic URL should not contain codex_cli_simplified_flow")
	}
	if strings.Contains(u, "originator") {
		t.Error("Anthropic URL should not contain originator")
	}
}

func TestParseTokenResponse(t *testing.T) {
	resp := map[string]interface{}{
		"access_token":  "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      "test-id-token",
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "test-access-token")
	}
	if cred.RefreshToken != "test-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", cred.RefreshToken, "test-refresh-token")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if cred.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want %q", cred.AuthMethod, "oauth")
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero")
	}
}

func TestParseTokenResponseNoAccessToken(t *testing.T) {
	body := []byte(`{"refresh_token": "test"}`)
	_, err := parseTokenResponse(body, "openai")
	if err == nil {
		t.Error("expected error for missing access_token")
	}
}

func TestParseTokenResponseAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTWithAccountID("acc-from-id")
	resp := map[string]interface{}{
		"access_token":  "not-a-jwt",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccountID != "acc-from-id" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-from-id")
	}
}

func makeJWTWithAccountID(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}

func TestExchangeCodeForTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
		Scopes:   "openid",
		Port:     1455,
		Provider: "openai",
	}

	cred, err := exchangeCodeForTokens(cfg, "test-code", "test-verifier", "http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatalf("exchangeCodeForTokens() error: %v", err)
	}

	if cred.AccessToken != "mock-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "mock-access-token")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
}

func TestExchangeCodeForTokensAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Anthropic expects JSON body, not form-urlencoded
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			http.Error(w, "expected application/json, got "+ct, http.StatusBadRequest)
			return
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if body["grant_type"] != "authorization_code" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"access_token":  "anthropic-access-token",
			"refresh_token": "anthropic-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:        server.URL,
		ClientID:      "test-anthropic-client",
		Scopes:        "org:create_api_key user:profile user:inference",
		Port:          8080,
		TokenEndpoint: "/v1/oauth/token",
		Provider:      "anthropic",
	}

	cred, err := exchangeCodeForTokens(cfg, "test-code", "test-verifier", "https://console.anthropic.com/oauth/code/callback")
	if err != nil {
		t.Fatalf("exchangeCodeForTokens() error: %v", err)
	}

	if cred.AccessToken != "anthropic-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "anthropic-access-token")
	}
	if cred.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "anthropic")
	}
}

func TestRefreshAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"access_token":  "refreshed-access-token",
			"refresh_token": "refreshed-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
	}

	cred := &AuthCredential{
		AccessToken:  "old-token",
		RefreshToken: "old-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}

	if refreshed.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "refreshed-access-token")
	}
	if refreshed.RefreshToken != "refreshed-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "refreshed-refresh-token")
	}
}

func TestRefreshAccessTokenNoRefreshToken(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	cred := &AuthCredential{
		AccessToken: "old-token",
		Provider:    "openai",
		AuthMethod:  "oauth",
	}

	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Error("expected error for missing refresh token")
	}
}

func TestOpenAIOAuthConfig(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	if cfg.Issuer != "https://auth.openai.com" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://auth.openai.com")
	}
	if cfg.ClientID == "" {
		t.Error("ClientID is empty")
	}
	if cfg.Port != 1455 {
		t.Errorf("Port = %d, want 1455", cfg.Port)
	}
	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "openai")
	}
}

func TestAnthropicOAuthConfig(t *testing.T) {
	cfg := AnthropicOAuthConfig()
	if cfg.Issuer != "https://console.anthropic.com" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://console.anthropic.com")
	}
	if cfg.ClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "9d1c250a-e61b-44d9-88ed-5944d1962f5e")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "anthropic")
	}
	if cfg.TokenEndpoint != "/v1/oauth/token" {
		t.Errorf("TokenEndpoint = %q, want %q", cfg.TokenEndpoint, "/v1/oauth/token")
	}
	if cfg.AuthorizeBaseURL != "https://claude.ai" {
		t.Errorf("AuthorizeBaseURL = %q, want %q", cfg.AuthorizeBaseURL, "https://claude.ai")
	}
	expectedScopes := "org:create_api_key user:profile user:inference"
	if cfg.Scopes != expectedScopes {
		t.Errorf("Scopes = %q, want %q", cfg.Scopes, expectedScopes)
	}
	// Verify tokenEndpointURL() resolves correctly
	expectedURL := "https://console.anthropic.com/v1/oauth/token"
	if cfg.tokenEndpointURL() != expectedURL {
		t.Errorf("tokenEndpointURL() = %q, want %q", cfg.tokenEndpointURL(), expectedURL)
	}
}

func TestParseDeviceCodeResponseIntervalAsNumber(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":5}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}

	if resp.DeviceAuthID != "abc" {
		t.Errorf("DeviceAuthID = %q, want %q", resp.DeviceAuthID, "abc")
	}
	if resp.UserCode != "DEF-1234" {
		t.Errorf("UserCode = %q, want %q", resp.UserCode, "DEF-1234")
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseIntervalAsString(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"5"}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}

	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseInvalidInterval(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"abc"}`)

	if _, err := parseDeviceCodeResponse(body); err == nil {
		t.Fatal("expected error for invalid interval")
	}
}
