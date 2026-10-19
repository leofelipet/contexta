package pushover

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSendPostsPushoverForm(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("request = %s %q", r.Method, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		for key, want := range map[string]string{"token": "app", "user": "user", "message": "hello", "title": "Contexta", "priority": "1", "url": "https://example.com"} {
			if got := form.Get(key); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
		if form.Has("retry") || form.Has("expire") {
			t.Fatalf("non-emergency form has retry/expire: %v", form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":1,"request":"request-id"}`))
	}))
	defer server.Close()
	client := &Client{endpoint: server.URL, http: server.Client()}

	result, err := client.Send(context.Background(), Credentials{AppToken: "app", UserKey: "user"}, SendParams{
		Message: "hello", Title: "Contexta", Priority: 1, URL: "https://example.com",
	})
	if err != nil || result.Request != "request-id" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestSendReturnsPushoverError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":0,"errors":["user identifier is invalid"]}`))
	}))
	defer server.Close()
	client := &Client{endpoint: server.URL, http: server.Client()}

	_, err := client.Send(context.Background(), Credentials{}, SendParams{Message: "hello"})
	if err == nil || !strings.Contains(err.Error(), "user identifier is invalid") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendEmergencyPostsRetryExpireAndReturnsReceipt(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		for key, want := range map[string]string{"priority": "2", "retry": "60", "expire": "3600"} {
			if got := form.Get(key); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":1,"request":"request-id","receipt":"receipt-id"}`))
	}))
	defer server.Close()
	client := &Client{endpoint: server.URL, http: server.Client()}

	result, err := client.Send(context.Background(), Credentials{AppToken: "app", UserKey: "user"}, SendParams{
		Message: "hello", Priority: 2, Retry: 60, Expire: 3600,
	})
	if err != nil || result.Request != "request-id" || result.Receipt != "receipt-id" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}
