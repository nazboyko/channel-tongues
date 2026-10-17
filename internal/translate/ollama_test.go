package translate

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthStates(t *testing.T) {
	tags := `{"models":[{"name":"gemma4:e4b","model":"gemma4:e4b"},{"name":"other"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(tags))
	}))
	defer srv.Close()

	ready, err := NewOllama(srv.URL, "gemma4:e4b").Health(context.Background())
	if ready != ModelReady || err != nil {
		t.Fatalf("ready: %v %v", ready, err)
	}
	latest, _ := NewOllama(srv.URL, "other:latest").Health(context.Background())
	if latest != ModelReady {
		t.Fatalf("a model without a tag is :latest, got %v", latest)
	}
	missing, _ := NewOllama(srv.URL, "gemma4:31b").Health(context.Background())
	if missing != ModelMissing {
		t.Fatalf("missing: %v", missing)
	}

	// A closed port is "down".
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	down, _ := NewOllama("http://"+addr, "gemma4:e4b").Health(context.Background())
	if down != ModelDown {
		t.Fatalf("down: %v", down)
	}
}
