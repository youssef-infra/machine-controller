package vpcloud

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const ssoEndpoint = "https://ssoco.platform.vpgrp.net/auth/realms/vpgrp/protocol/openid-connect/token"

// getToken extracts an API bearer token from the Secret.
//
// Supports two modes:
//
//  1. "apiToken" field: pre-obtained bearer token (Gardener's SecretBinding flow).
//     This is the primary path.
//
//  2. "username" + "password" fields: SSO credentials.
//     Exchanges username/password for a JWT via Keycloak using the
//     VEEPEE_SSO_TOKEN env var (base64 of "client_id:client_secret").
func getToken(secret *corev1.Secret, ssoToken string) (string, error) {
	if secret == nil {
		return "", fmt.Errorf("secret is nil")
	}

	// Primary: direct API token.
	if token := string(secret.Data["apiToken"]); token != "" {
		return token, nil
	}

	// Fallback: SSO username/password exchange.
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username != "" && password != "" {
		if ssoToken == "" {
			return "", fmt.Errorf("SSO credentials found but VEEPEE_SSO_TOKEN env var is not set — set it or use apiToken instead")
		}

		token, err := exchangeSSOToken(ssoToken, username, password)
		if err != nil {
			return "", fmt.Errorf("SSO token exchange failed: %w", err)
		}
		return token, nil
	}

	return "", fmt.Errorf("no credentials found in secret: need 'apiToken' or 'username'+'password'")
}

// exchangeSSOToken exchanges username/password for a JWT via Keycloak.
// ssoToken is base64-encoded "client_id:client_secret".
func exchangeSSOToken(ssoToken, username, password string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(ssoToken)
	if err != nil {
		return "", fmt.Errorf("decoding VEEPEE_SSO_TOKEN: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("VEEPEE_SSO_TOKEN must be base64(client_id:client_secret)")
	}
	clientID, clientSecret := parts[0], parts[1]

	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"username":      {username},
		"password":      {password},
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost, ssoEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("creating SSO request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("SSO request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes := make([]byte, 1024)
		n, _ := resp.Body.Read(bodyBytes)
		return "", fmt.Errorf("SSO returned %d: %s", resp.StatusCode, string(bodyBytes[:n]))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding SSO response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("SSO response has empty access_token")
	}

	return tokenResp.AccessToken, nil
}
