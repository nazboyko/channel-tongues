package translate

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestChatCapsTheAnswerLength(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Options map[string]any `json:"options"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		got = body.Options
		w.Write([]byte(`{"message":{"role":"assistant","content":"{}"},"done":true}`))
	}))
	defer srv.Close()
	o := NewOllama(srv.URL, "gemma4:e4b")
	text := strings.Repeat("rain ", 100) // 500 characters to translate
	if _, _, err := o.chat(context.Background(), []chatMessage{{Role: "system", Content: "long instructions " + text}, {Role: "user", Content: text}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	// Twice the text plus room for the JSON: a real answer fits, a runaway
	// one stops long before the 8192-token context is full.
	if got["num_predict"] != float64(2*500+256) || got["num_ctx"] != float64(8192) {
		t.Fatalf("options %v", got)
	}
}

func TestAnswerCapOnARetryCoversTheEarlierAnswer(t *testing.T) {
	answer := strings.Repeat("ฝน", 1000) // the earlier answer, 2,000 characters
	got := numPredict([]chatMessage{
		{Role: "system", Content: "instructions"},
		{Role: "user", Content: "short source"},
		{Role: "assistant", Content: answer},
		{Role: "user", Content: "Line 3 went missing. Translate it."},
	})
	if got != 2*2000+256 {
		t.Fatalf("the retry has to write the whole answer again; cap %d", got)
	}
}

func TestInfoReadsTagsAndPs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"gemma4:26b","model":"gemma4:26b","size":18730000000,"details":{"parameter_size":"25.2B","quantization_level":"Q4_K_M"}},{"name":"gemma4:e4b","size":6580000000}]}`))
		case "/api/ps":
			w.Write([]byte(`{"models":[{"name":"gemma4:26b","model":"gemma4:26b","size":1235998472,"size_vram":1235998472,"context_length":8192}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	info, err := NewOllama(srv.URL, "gemma4:26b").Info(context.Background())
	if err != nil || !info.Loaded || info.DiskBytes != 18730000000 || info.MemoryBytes != 1235998472 || info.GPUBytes != info.MemoryBytes || info.Parameters != "25.2B" || info.ContextLength != 8192 {
		t.Fatalf("got %+v %v", info, err)
	}
	cold, _ := NewOllama(srv.URL, "gemma4:e4b").Info(context.Background())
	if cold.Loaded || cold.DiskBytes != 6580000000 {
		t.Fatalf("not loaded: %+v", cold)
	}
}

func TestChatKeepsTheTimings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"role":"assistant","content":"{\"code\":\"es\"}"},"done":true,"prompt_eval_count":120,"eval_count":6,"load_duration":4000000000,"prompt_eval_duration":90000000,"eval_duration":60000000,"total_duration":4200000000}`))
	}))
	defer srv.Close()
	code, st, err := NewOllama(srv.URL, "gemma4:26b").DetectLanguage(context.Background(), "Lluvia")
	if err != nil || code != "es" || st.PromptTokens != 120 || st.OutputTokens != 6 || st.Load.Seconds() != 4 || st.Eval.Milliseconds() != 60 || st.PromptEval.Milliseconds() != 90 {
		t.Fatalf("got %q %+v %v", code, st, err)
	}
}
