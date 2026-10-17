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

func TestPickModelPrefersTheLargerGemma(t *testing.T) {
	serve := func(tags string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tags)) }))
	}
	both := serve(`{"models":[{"name":"gemma4:e4b"},{"name":"gemma4:26b"}]}`)
	defer both.Close()
	o := NewOllama(both.URL, "")
	if st := o.PickModel(context.Background()); st != ModelReady || o.Model != "gemma4:26b" {
		t.Fatalf("got %v %s", st, o.Model)
	}
	small := serve(`{"models":[{"name":"gemma4:e4b"}]}`)
	defer small.Close()
	o = NewOllama(small.URL, "")
	if st := o.PickModel(context.Background()); st != ModelReady || o.Model != "gemma4:e4b" {
		t.Fatalf("got %v %s", st, o.Model)
	}
	none := serve(`{"models":[{"name":"llama3:8b"}]}`)
	defer none.Close()
	o = NewOllama(none.URL, "")
	if st := o.PickModel(context.Background()); st != ModelMissing || o.Model != Fallback {
		t.Fatalf("got %v %s", st, o.Model)
	}
}
