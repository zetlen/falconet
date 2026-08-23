package github

// The GitHub App: the manifest conversion that creates one, the JWT that
// authenticates as one, and the installation lookup that says whether it is
// installed on the repository. README step 3, done by `falconet init`
// (ADR-0006 D5).

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"time"
)

// App is what POST /app-manifests/{code}/conversions answers with: the App
// GitHub just registered from the manifest, and the one copy of its private
// key that will ever exist outside GitHub.
//
// The answer also carries client_secret and webhook_secret. Neither is read:
// falconet's App is a credential, not an OAuth App and not a webhook
// receiver, so there is nothing to do with either but hold it, and a secret
// that is not decoded is one that cannot leak. They are discarded unused.
type App struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	HTMLURL  string `json:"html_url"`
	ClientID string `json:"client_id"`
	// PEM is the private key, exactly as received. It goes into a sealed
	// box and nowhere else: never to disk, never to a log, never into an
	// error message.
	PEM string `json:"pem"`
}

// ConvertAppManifest is POST /app-manifests/{code}/conversions: the
// temporary code GitHub sent the browser back with, exchanged for the App.
// The code is good for one hour.
//
// token is what the request authenticates with, and "" sends no
// Authorization header at all: the documentation lists no token type for
// this endpoint and its example is a bare curl, which is consistent with a
// bootstrap flow — the App does not exist until this call answers, so
// nothing could authenticate as it. The caller tries without first and
// with the setup token on a 401 or 403, and records which worked; ADR-0006
// lists this as unverified until a live run says.
func (c *Client) ConvertAppManifest(code, token string) (*App, error) {
	as := *c
	as.Token = token
	var out App
	if err := as.Do("POST", "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Installation is GET /repos/{owner}/{name}/installation's answer: the
// App's installation on that repository, when there is one.
type Installation struct {
	ID      int64 `json:"id"`
	AppID   int64 `json:"app_id"`
	Account User  `json:"account"`
}

// GetInstallation is GET /repos/{owner}/{name}/installation, authenticated
// as the App with jwt — "you must use a JWT to access this endpoint", and a
// JWT goes in `Authorization: Bearer`. A 404 is the App not being installed
// there (or not existing); the caller polls until it is.
func (c *Client) GetInstallation(owner, name, jwt string) (*Installation, error) {
	as := *c
	as.Token = jwt
	var out Installation
	if err := as.Do("GET", RepoPath(owner, name, "/installation"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// JWTLifetime is how long a JWT is good for from now: ten minutes, GitHub's
// maximum ("this time must be no more than 10 minutes into the future").
const JWTLifetime = 10 * time.Minute

// JWTDrift is how far into the past the JWT's issued-at is set: "to
// protect against clock drift, we recommend that you set this 60 seconds in
// the past".
const JWTDrift = 60 * time.Second

// AppJWT is a JSON Web Token that authenticates as the App: the header
// {"alg":"RS256","typ":"JWT"}, the claims iat = now − 60s, exp = now + 10m
// and iss = appID, signed with the App's private key — RSASSA-PKCS1-v1_5
// over SHA-256, which is what RS256 is — each part base64url without
// padding. The documentation's iss is the App's client ID or its ID, as a
// string in every example it gives; the ID is what falconet stores, so it
// is what is used.
//
// Standard library only: crypto/rsa and crypto/sha256 are named in ADR-0006
// D1 for exactly this, and a JWT with three claims is not worth a
// dependency.
//
// pemBytes is the key as GitHub issued it — PKCS#1, "RSA PRIVATE KEY" — or
// PKCS#8 with an RSA key inside, which is what a re-encoded one would be.
// Nothing from pemBytes reaches the error: a key that does not parse is
// said to not parse, and no more.
func AppJWT(appID string, pemBytes []byte, now time.Time) (string, error) {
	key, err := parseRSAPrivateKey(pemBytes)
	if err != nil {
		return "", err
	}
	header, err := json.Marshal(struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}{"RS256", "JWT"})
	if err != nil {
		return "", fmt.Errorf("encoding the JWT header: %v", err)
	}
	claims, err := json.Marshal(struct {
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
		Issuer   string `json:"iss"`
	}{now.Add(-JWTDrift).Unix(), now.Add(JWTLifetime).Unix(), appID})
	if err != nil {
		return "", fmt.Errorf("encoding the JWT claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing the JWT: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// parseRSAPrivateKey reads one PEM block holding an RSA private key, in
// either of the two encodings one comes in.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("the App's private key is not a PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the App's private key does not parse as an RSA private key (PKCS#1 or PKCS#8)")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the App's private key is not an RSA key, and GitHub signs App JWTs with RS256")
	}
	return key, nil
}
