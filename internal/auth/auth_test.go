package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware(t *testing.T) {
	t.Parallel()

	handler := NewMiddleware("correct-token").Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "valid", header: "Bearer correct-token", want: http.StatusNoContent},
		{name: "wrong", header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic correct-token", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", tt.header)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.want {
				t.Fatalf("status = %d, want %d", res.Code, tt.want)
			}
		})
	}
}
