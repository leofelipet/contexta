package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type Middleware struct {
	token string
}

func NewMiddleware(token string) Middleware {
	return Middleware{token: token}
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !SecureEqual(provided, m.token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="contexta"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SecureEqual(provided, expected string) bool {
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return "", false
	}
	return token, true
}
