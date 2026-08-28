package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayRoutesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("user-42"))
	}))
	t.Cleanup(upstream.Close)

	g, err := New(map[string]string{"users": upstream.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/users/42", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.String() != "user-42" {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), "user-42")
	}
}

func TestGatewayUnknownRoute(t *testing.T) {
	g, err := New(map[string]string{"users": "http://users"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	g.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/products", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestParseRoutes(t *testing.T) {
	routes, err := ParseRoutes("users=http://users:8080,products=http://products:8080")
	if err != nil {
		t.Fatal(err)
	}
	if routes["users"] != "http://users:8080" {
		t.Fatalf("users route = %q", routes["users"])
	}
}
