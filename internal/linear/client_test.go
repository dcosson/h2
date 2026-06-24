package linear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient returns an OAuth Client pointed at the given test server URL.
func newTestClient(url string) *Client {
	c := NewOAuthClient("test-token")
	c.apiURL = url
	return c
}

func TestDoGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).Viewer(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want graphql error containing boom, got %v", err)
	}
}

func TestDoHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).Viewer(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want http 401 error, got %v", err)
	}
}
