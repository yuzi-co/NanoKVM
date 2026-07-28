package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/service/apikey"
)

// issueKey points the key store at a temporary file and returns a usable key.
func issueKey(t *testing.T) string {
	t.Helper()

	original := apikey.File
	apikey.File = filepath.Join(t.TempDir(), "api_keys.json")
	t.Cleanup(func() { apikey.File = original })

	secret, _, err := apikey.Create("automation")
	if err != nil {
		t.Fatalf("failed to issue a key: %s", err)
	}

	return secret
}

func keyedRequest(t *testing.T, header string, value string, origin string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/vm/system/reboot", nil)
	req.Host = "nanokvm.local"
	if header != "" {
		req.Header.Set(header, value)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	w := httptest.NewRecorder()
	protectedEngine().ServeHTTP(w, req)

	return w
}

func TestAPIKeyAuthenticatesWithoutASession(t *testing.T) {
	// The point of the feature: a script holds a key, not a login session.
	secret := issueKey(t)

	w := keyedRequest(t, "X-API-Key", secret, "")

	if w.Code != http.StatusOK {
		t.Fatalf("a valid api key should be accepted, got %d", w.Code)
	}
}

func TestAPIKeyIsAcceptedAsABearerToken(t *testing.T) {
	secret := issueKey(t)

	w := keyedRequest(t, "Authorization", "Bearer "+secret, "")

	if w.Code != http.StatusOK {
		t.Fatalf("a bearer api key should be accepted, got %d", w.Code)
	}
}

func TestWrongAPIKeyIsRejected(t *testing.T) {
	issueKey(t)

	for _, value := range []string{"nkvm_not-a-real-key", "", "Bearer nkvm_nope"} {
		w := keyedRequest(t, "X-API-Key", value, "")

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("api key %q should be rejected, got %d", value, w.Code)
		}
	}
}

func TestAPIKeyCannotReachSessionOnlyRoutes(t *testing.T) {
	// Managing keys is session work. A stolen key already has the run of the
	// API, but it must not be able to quietly mint more keys that survive the
	// password change made to shut it out.
	secret := issueKey(t)

	r := gin.New()
	r.POST("/api/auth/api-keys", CheckSession(), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/api-keys", nil)
	req.Host = "nanokvm.local"
	req.Header.Set("X-API-Key", secret)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an api key should not manage keys, got %d", w.Code)
	}
}

func TestSessionStillReachesSessionOnlyRoutes(t *testing.T) {
	token, err := GenerateJWT("admin")
	if err != nil {
		t.Fatalf("failed to generate token: %s", err)
	}

	r := gin.New()
	r.POST("/api/auth/api-keys", CheckSession(), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/api-keys", nil)
	req.Host = "nanokvm.local"
	req.AddCookie(&http.Cookie{Name: "nano-kvm-token", Value: token})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("a logged in session should manage keys, got %d", w.Code)
	}
}

func TestAPIKeyDoesNotBypassTheOriginCheck(t *testing.T) {
	// A key sent from a hostile page would need that page to already know the
	// key, but the origin rule is what the rest of the API relies on and this
	// path must not become the hole in it.
	secret := issueKey(t)

	w := keyedRequest(t, "X-API-Key", secret, "https://evil.example")

	if w.Code != http.StatusForbidden {
		t.Fatalf("a cross-origin request should be refused, got %d", w.Code)
	}
}
