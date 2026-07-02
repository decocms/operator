// Package githubapp mints short-lived, repo-scoped GitHub App installation
// access tokens — the same mechanism admin uses to clone private repos
// (utils/loaders/github/tokens.ts): sign an RS256 JWT with the App private key,
// look up the repo's installation, then exchange for an installation token
// scoped to that single repo.
//
// The token is injected into the fast-deploy sync Job as GITHUB_TOKEN, which
// clones via https://x-access-token:$GITHUB_TOKEN@github.com/<owner>/<repo>.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

// App holds the GitHub App credentials used to mint installation tokens.
type App struct {
	appID string
	key   *rsa.PrivateKey
	http  *http.Client
}

// NewFromEnv builds an App from GITHUB_APP_ID + GITHUB_APP_PRIVATE_KEY.
// Returns (nil, nil) when unset — the App is simply disabled (callers then fall
// back to a static token / public-repo clone). Returns an error only when the
// vars are SET but invalid, so a misconfiguration surfaces instead of silently
// disabling private-repo access.
func NewFromEnv() (*App, error) {
	appID := strings.TrimSpace(os.Getenv("GITHUB_APP_ID"))
	rawKey := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if appID == "" && strings.TrimSpace(rawKey) == "" {
		return nil, nil // not configured → disabled
	}
	if appID == "" || strings.TrimSpace(rawKey) == "" {
		return nil, fmt.Errorf("both GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY must be set")
	}
	// Env vars often carry the PEM with literal "\n"; restore real newlines
	// (matches admin's tokens.ts).
	key, err := parsePrivateKey(strings.ReplaceAll(rawKey, `\n`, "\n"))
	if err != nil {
		return nil, fmt.Errorf("parse GITHUB_APP_PRIVATE_KEY: %w", err)
	}
	return &App{
		appID: appID,
		key:   key,
		http:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// parsePrivateKey accepts a PEM-encoded RSA key in either PKCS#1
// ("RSA PRIVATE KEY", GitHub's default) or PKCS#8 ("PRIVATE KEY") form.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k8, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("key is neither PKCS#1 nor PKCS#8: %w", err)
	}
	rsaKey, ok := k8.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA (got %T)", k8)
	}
	return rsaKey, nil
}

// jwt builds an RS256 App JWT (iss=appID). GitHub allows exp up to 10 min and
// recommends backdating iat 60s to tolerate clock drift.
func (a *App) jwt() (string, error) {
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"iat":%d,"exp":%d,"iss":%q}`,
		now.Add(-60*time.Second).Unix(),
		now.Add(9*time.Minute).Unix(),
		a.appID,
	)))
	signingInput := header + "." + claims
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// InstallationToken returns a short-lived access token scoped to owner/repo
// only. Mirrors admin's getRepoTokenFromAppInstallation.
func (a *App) InstallationToken(ctx context.Context, owner, repo string) (string, error) {
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	installID, err := a.installationID(ctx, jwt, owner, repo)
	if err != nil {
		return "", err
	}
	return a.accessToken(ctx, jwt, installID, repo)
}

func (a *App) installationID(ctx context.Context, jwt, owner, repo string) (int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/installation", githubAPI, owner, repo)
	var out struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, http.MethodGet, url, jwt, nil, &out); err != nil {
		return 0, fmt.Errorf("lookup installation for %s/%s: %w", owner, repo, err)
	}
	if out.ID == 0 {
		return 0, fmt.Errorf("no GitHub App installation found for %s/%s", owner, repo)
	}
	return out.ID, nil
}

func (a *App) accessToken(ctx context.Context, jwt string, installID int64, repo string) (string, error) {
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPI, installID)
	// Scope the token to just this repo (least privilege), matching admin.
	body, _ := json.Marshal(map[string]any{"repositories": []string{repo}})
	var out struct {
		Token string `json:"token"`
	}
	if err := a.do(ctx, http.MethodPost, url, jwt, body, &out); err != nil {
		return "", fmt.Errorf("mint installation access token: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("empty installation access token from GitHub")
	}
	return out.Token, nil
}

func (a *App) do(ctx context.Context, method, url, jwt string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %s %s: %d %s", method, url, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, out)
}
