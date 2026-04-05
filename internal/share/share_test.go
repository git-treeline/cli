package share

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestGenerateToken_Length(t *testing.T) {
	tok := GenerateToken()
	if len(tok) != 32 {
		t.Errorf("expected 32-char hex token, got %d chars: %q", len(tok), tok)
	}
}

func TestGenerateToken_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok := GenerateToken()
		if seen[tok] {
			t.Fatalf("duplicate token after %d iterations", i)
		}
		seen[tok] = true
	}
}

func TestCookieValue_Deterministic(t *testing.T) {
	tok := "abcdef1234567890abcdef1234567890"
	a := cookieValue(tok)
	b := cookieValue(tok)
	if a != b {
		t.Errorf("cookieValue not deterministic: %q vs %q", a, b)
	}
}

func TestCookieValue_DiffersByToken(t *testing.T) {
	a := cookieValue("aaaa")
	b := cookieValue("bbbb")
	if a == b {
		t.Error("cookieValue should differ for different tokens")
	}
}

func TestTokenHandler_TokenPath_SetsCookieAndRedirects(t *testing.T) {
	token := "deadbeef12345678deadbeef12345678"
	h := NewTokenHandler(token, 9999)

	req := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected Set-Cookie header")
	}
	found := false
	for _, c := range cookies {
		if c.Name == cookieName {
			found = true
			if c.Value != cookieValue(token) {
				t.Errorf("cookie value mismatch: got %q", c.Value)
			}
			if !c.HttpOnly {
				t.Error("expected HttpOnly")
			}
			if !c.Secure {
				t.Error("expected Secure")
			}
		}
	}
	if !found {
		t.Error("gtl_share cookie not found in response")
	}
}

func TestTokenHandler_ValidCookie_Proxies(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok from backend"))
	}))
	defer backend.Close()

	// Extract port from the test server URL.
	_, port, _ := extractHostPort(backend.URL)

	token := "cafebabe00112233cafebabe00112233"
	h := NewTokenHandler(token, port)

	req := httptest.NewRequest(http.MethodGet, "/some/path", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue(token)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "ok from backend" {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestTokenHandler_NoCookie_NoToken_404(t *testing.T) {
	token := "1111222233334444aaaabbbbccccdddd"
	h := NewTokenHandler(token, 9999)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestTokenHandler_WrongToken_404(t *testing.T) {
	token := "1111222233334444aaaabbbbccccdddd"
	h := NewTokenHandler(token, 9999)

	req := httptest.NewRequest(http.MethodGet, "/s/wrongtoken", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong token path, got %d", rec.Code)
	}
}

func TestTokenHandler_WrongCookie_404(t *testing.T) {
	token := "1111222233334444aaaabbbbccccdddd"
	h := NewTokenHandler(token, 9999)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "invalid"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong cookie, got %d", rec.Code)
	}
}

func TestTokenHandler_TokenPathWithTrailingSlash(t *testing.T) {
	token := "deadbeef12345678deadbeef12345678"
	h := NewTokenHandler(token, 9999)

	req := httptest.NewRequest(http.MethodGet, "/s/"+token+"/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 for token path with trailing slash, got %d", rec.Code)
	}
}

func TestFreePort(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	if port < 1 || port > 65535 {
		t.Errorf("port out of range: %d", port)
	}
}

func TestGenerateShortID_Length(t *testing.T) {
	id := generateShortID()
	if len(id) != 8 {
		t.Errorf("expected 8-char hex id, got %d chars: %q", len(id), id)
	}
}

func TestGenerateShortID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateShortID()
		if seen[id] {
			t.Fatalf("duplicate short ID after %d iterations", i)
		}
		seen[id] = true
	}
}

func extractHostPort(rawURL string) (string, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, err
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		return "", 0, err
	}
	return u.Hostname(), p, nil
}
