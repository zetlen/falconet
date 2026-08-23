package github

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// testRSAKey is one 2048-bit key for the package's tests, generated once:
// what GitHub issues an App.
func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

// pkcs1 is the key as GitHub downloads it: "RSA PRIVATE KEY".
func pkcs1(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// decodeJWT splits a token into its header, claims and signature, decoding
// the first two.
func decodeJWT(t *testing.T, token string) (header, claims map[string]any, signingInput string, signature []byte) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the JWT has %d parts, want 3: %q", len(parts), token)
	}
	for i, into := range []*map[string]any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("part %d is not base64url without padding: %v", i, err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("part %d is not JSON: %v", i, err)
		}
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("the signature is not base64url without padding: %v", err)
	}
	return header, claims, parts[0] + "." + parts[1], signature
}

func TestAppJWTVerifiesWithThePublicKeyAndCarriesTheThreeClaims(t *testing.T) {
	key := testRSAKey(t)
	now := time.Unix(1_700_000_000, 0)
	token, err := AppJWT("12345", pkcs1(key), now)
	if err != nil {
		t.Fatal(err)
	}
	header, claims, signingInput, signature := decodeJWT(t, token)
	if !reflect.DeepEqual(header, map[string]any{"alg": "RS256", "typ": "JWT"}) {
		t.Errorf("header: %v", header)
	}
	want := map[string]any{
		"iat": float64(now.Unix() - 60),
		"exp": float64(now.Unix() + 600),
		"iss": "12345",
	}
	if !reflect.DeepEqual(claims, want) {
		t.Errorf("claims: %v, want %v", claims, want)
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Errorf("the signature does not verify with the public key: %v", err)
	}
	// A different key does not verify it, and a changed claim breaks it.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(&other.PublicKey, crypto.SHA256, digest[:], signature); err == nil {
		t.Error("another key verified the signature")
	}
	tampered := sha256.Sum256([]byte(signingInput + "x"))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, tampered[:], signature); err == nil {
		t.Error("a changed signing input still verified")
	}
}

func TestAppJWTExpiresInTenMinutesWhichIsGitHubsMaximum(t *testing.T) {
	if JWTLifetime > 10*time.Minute {
		t.Errorf("the lifetime is %s, and GitHub's maximum is 10 minutes", JWTLifetime)
	}
	if JWTDrift != 60*time.Second {
		t.Errorf("iat is set %s in the past; the documentation says 60 seconds", JWTDrift)
	}
	key := testRSAKey(t)
	for _, now := range []time.Time{time.Unix(0, 0), time.Unix(1_700_000_000, 123456789), time.Now()} {
		token, err := AppJWT("1", pkcs1(key), now)
		if err != nil {
			t.Fatal(err)
		}
		_, claims, _, _ := decodeJWT(t, token)
		exp, iat := int64(claims["exp"].(float64)), int64(claims["iat"].(float64))
		if exp-now.Unix() != 600 {
			t.Errorf("at %v: exp is %d seconds from now, want 600", now, exp-now.Unix())
		}
		if now.Unix()-iat != 60 {
			t.Errorf("at %v: iat is %d seconds in the past, want 60", now, now.Unix()-iat)
		}
		if exp-iat != 660 {
			t.Errorf("at %v: the token is good for %d seconds", now, exp-iat)
		}
	}
}

func TestAppJWTReadsAPKCS8KeyToo(t *testing.T) {
	key := testRSAKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pem8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	token, err := AppJWT("7", pem8, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	_, _, signingInput, signature := decodeJWT(t, token)
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Errorf("a PKCS#8 key's signature does not verify: %v", err)
	}
}

func TestAppJWTRefusesWhatIsNotAnRSAKeyWithoutQuotingIt(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"not a PEM":      []byte("SECRET-MARKER not a pem at all"),
		"a PEM of bytes": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("SECRET-MARKER")}),
		"an EC key":      pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}),
		"empty":          nil,
	} {
		_, err := AppJWT("1", bad, time.Now())
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRET-MARKER") || strings.Contains(err.Error(), base64.StdEncoding.EncodeToString([]byte("SECRET-MARKER"))) {
			t.Errorf("%s: the error quotes the key: %v", name, err)
		}
	}
	if _, err := AppJWT("1", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}), time.Now()); err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Errorf("an EC key's refusal does not say RSA: %v", err)
	}
}

// --- the conversion ----------------------------------------------------------

const conversion = `{"id": 12345, "slug": "falconet-o-r", "name": "falconet-o-r", "html_url": "https://github.com/apps/falconet-o-r",
  "client_id": "Iv1.abc", "client_secret": "CLIENT-SECRET-MARKER", "webhook_secret": "WEBHOOK-SECRET-MARKER",
  "pem": "-----BEGIN RSA PRIVATE KEY-----\nMII\n-----END RSA PRIVATE KEY-----\n"}`

func TestConvertAppManifestSendsNoTokenWhenGivenNone(t *testing.T) {
	c, seen := serve(t, 201, conversion)
	app, err := c.ConvertAppManifest("a b/c", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 {
		t.Fatalf("%d requests", len(*seen))
	}
	got := (*seen)[0]
	if got.Method != "POST" || got.Path != "/app-manifests/a b/c/conversions" {
		t.Errorf("%s %s", got.Method, got.Path)
	}
	if _, has := got.Header["Authorization"]; has {
		t.Errorf("an Authorization header was sent: %q", got.Header.Get("Authorization"))
	}
	if got.Body != nil {
		t.Errorf("a body was sent: %v", got.Body)
	}
	want := &App{ID: 12345, Slug: "falconet-o-r", Name: "falconet-o-r", HTMLURL: "https://github.com/apps/falconet-o-r",
		ClientID: "Iv1.abc", PEM: "-----BEGIN RSA PRIVATE KEY-----\nMII\n-----END RSA PRIVATE KEY-----\n"}
	if !reflect.DeepEqual(app, want) {
		t.Errorf("got %+v, want %+v", *app, *want)
	}
}

func TestConvertAppManifestSendsTheTokenItIsGivenAndNotTheClients(t *testing.T) {
	c, seen := serve(t, 201, conversion)
	if _, err := c.ConvertAppManifest("code", "setup-token"); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].Header.Get("Authorization"); got != "Bearer setup-token" {
		t.Errorf("Authorization: %q", got)
	}
	// The client's own token is untouched by the call.
	if c.Token != "tok-123" {
		t.Errorf("the client's token became %q", c.Token)
	}
}

func TestConvertAppManifestA401IsAnErrorTheCallerCanRetryOn(t *testing.T) {
	c, _ := serve(t, 401, `{"message":"Requires authentication"}`)
	_, err := c.ConvertAppManifest("code", "")
	var e *Error
	if !errors.As(err, &e) || e.Status != 401 || e.Method != "POST" || e.Path != "/app-manifests/code/conversions" {
		t.Errorf("got %v", err)
	}
}

func TestTheTwoSecretsInTheConversionAreNotDecoded(t *testing.T) {
	typ := reflect.TypeOf(App{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "client_secret" || tag == "webhook_secret" {
			t.Errorf("App reads %s, which is discarded unused", tag)
		}
	}
}

// --- the installation ----------------------------------------------------------

func TestGetInstallationAuthenticatesWithTheJWTAsBearer(t *testing.T) {
	c, seen := serve(t, 200, `{"id": 777, "app_id": 12345, "account": {"login": "o", "type": "User"}}`)
	inst, err := c.GetInstallation("o", "r", "eyJ.abc.def")
	if err != nil {
		t.Fatal(err)
	}
	got := (*seen)[0]
	if got.Method != "GET" || got.Path != "/repos/o/r/installation" {
		t.Errorf("%s %s", got.Method, got.Path)
	}
	if h := got.Header.Get("Authorization"); h != "Bearer eyJ.abc.def" {
		t.Errorf("Authorization: %q", h)
	}
	if inst.ID != 777 || inst.AppID != 12345 || inst.Account.Login != "o" {
		t.Errorf("got %+v", *inst)
	}
	if c.Token != "tok-123" {
		t.Errorf("the client's token became %q", c.Token)
	}
}

func TestGetInstallationA404IsNotInstalled(t *testing.T) {
	c, _ := serve(t, 404, `{"message":"Not Found"}`)
	_, err := c.GetInstallation("o", "r", "jwt")
	var e *Error
	if !errors.As(err, &e) || e.Status != 404 {
		t.Errorf("got %v", err)
	}
}
