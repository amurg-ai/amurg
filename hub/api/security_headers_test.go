package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersAllowSameOriginMicrophone(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, req)

	if got := w.Header().Get("Permissions-Policy"); got != "camera=(), microphone=(self), geolocation=()" {
		t.Fatalf("Permissions-Policy = %q, want same-origin microphone access", got)
	}
}
