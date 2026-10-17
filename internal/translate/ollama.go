// Package translate turns a video's title and description into other
// languages with a local model: protect the parts that must not change,
// translate, check the result, restore.
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// ModelState is what the helper knows about the local model.
type ModelState string

const (
	ModelDown    ModelState = "down"          // Ollama is not reachable
	ModelMissing ModelState = "model-missing" // Ollama answers, the model is not pulled
	ModelReady   ModelState = "ready"
)

// Ollama talks to a local Ollama server.
type Ollama struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// NewOllama returns a client with sensible timeouts for a local server.
func NewOllama(baseURL, model string) *Ollama {
	return &Ollama{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

// Preferred lists the models used when none is set, best first: the larger
// Gemma 4 when it is downloaded (as fast when warm, clearly better
// translations in our measurements), otherwise the small one.
var Preferred = []string{"gemma4:26b", "gemma4:e4b"}

// Fallback is the model to download when none of the preferred ones is there.
const Fallback = "gemma4:e4b"

// installed lists the models Ollama has downloaded.
func (o *Ollama) installed(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama answered %s", resp.Status)
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("ollama tags: %w", err)
	}
	var out []string
	for _, m := range tags.Models {
		out = append(out, m.Name, m.Model)
	}
	return out, nil
}

func has(installed []string, model string) bool {
	for _, m := range installed {
		if sameModel(m, model) {
			return true
		}
	}
	return false
}

// Health tells the three states apart: not reachable, model missing, ready.
func (o *Ollama) Health(ctx context.Context) (ModelState, error) {
	installed, err := o.installed(ctx)
	if err != nil {
		return ModelDown, err
	}
	if has(installed, o.Model) {
		return ModelReady, nil
	}
	return ModelMissing, nil
}

// PickModel sets o.Model to the first preferred model that is downloaded,
// or to Fallback when none is (so the person is told to pull that one).
func (o *Ollama) PickModel(ctx context.Context) ModelState {
	installed, err := o.installed(ctx)
	if err != nil {
		if o.Model == "" {
			o.Model = Fallback
		}
		return ModelDown
	}
	for _, m := range Preferred {
		if has(installed, m) {
			o.Model = m
			return ModelReady
		}
	}
	o.Model = Fallback
	return ModelMissing
}

// sameModel compares tags, treating a missing tag as ":latest".
func sameModel(a, b string) bool {
	norm := func(s string) string {
		if s != "" && !strings.Contains(s, ":") {
			return s + ":latest"
		}
		return s
	}
	return a != "" && norm(a) == norm(b)
}

// Stats describes one model call, for the log and the measurements.
type Stats struct {
	NumCtx       int           `json:"numCtx"`
	PromptTokens int           `json:"promptTokens"` // Ollama's prompt_eval_count
	OutputTokens int           `json:"outputTokens"` // eval_count
	Load         time.Duration `json:"load"`
	PromptEval   time.Duration `json:"promptEval"` // reading the prompt
	Eval         time.Duration `json:"eval"`       // writing the answer: output tokens per second is OutputTokens / Eval
	Total        time.Duration `json:"total"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []chatMessage   `json:"messages"`
	Stream    bool            `json:"stream"`
	Format    json.RawMessage `json:"format,omitempty"`
	Think     bool            `json:"think"`
	KeepAlive string          `json:"keep_alive"`
	Options   map[string]any  `json:"options"`
}

type chatResponse struct {
	Message            chatMessage `json:"message"`
	Done               bool        `json:"done"`
	PromptEvalCount    int         `json:"prompt_eval_count"`
	EvalCount          int         `json:"eval_count"`
	LoadDuration       int64       `json:"load_duration"`
	PromptEvalDuration int64       `json:"prompt_eval_duration"`
	EvalDuration       int64       `json:"eval_duration"`
	TotalDuration      int64       `json:"total_duration"`
	Error              string      `json:"error"`
}

// numCtx picks the context size. It stays at 8192 for every normal video,
// because changing it makes Ollama reload the model.
func numCtx(messages []chatMessage) int {
	runes := 0
	for _, m := range messages {
		runes += len([]rune(m.Content))
	}
	// Worst case about one token per character for non-Latin output, plus
	// the prompt itself at about two characters per token.
	switch est := runes/2 + runes + 512; {
	case est > 15000:
		return 32768
	case est > 7500:
		return 16384
	}
	return 8192
}

// numPredict caps the answer at twice the longest text in the conversation
// (in characters) plus room for the JSON around it. On a retry that is the
// earlier answer, which the model writes again in full. A real answer stays
// far below; a model that loses its way in a language it barely knows
// otherwise writes until the context is full, which took minutes per language.
func numPredict(messages []chatMessage) int {
	runes := 0
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			runes = max(runes, len([]rune(m.Content)))
		}
	}
	return 2*runes + 256
}

// chat sends one non-streaming chat request with a JSON schema. It retries
// once on a connection error.
func (o *Ollama) chat(ctx context.Context, messages []chatMessage, schema json.RawMessage, temperature float64) (string, Stats, error) {
	n := numCtx(messages)
	body, err := json.Marshal(chatRequest{
		Model:     o.Model,
		Messages:  messages,
		Stream:    false,
		Format:    schema,
		Think:     false,
		KeepAlive: "30m",
		Options:   map[string]any{"num_ctx": n, "temperature": temperature, "num_predict": numPredict(messages)},
	})
	if err != nil {
		return "", Stats{}, err
	}
	var resp chatResponse
	for attempt := 0; ; attempt++ {
		resp, err = o.post(ctx, "/api/chat", body)
		if err == nil || attempt == 1 || ctx.Err() != nil || !isConnErr(err) {
			break
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return "", Stats{}, ctx.Err()
		}
	}
	if err != nil {
		return "", Stats{}, err
	}
	st := Stats{
		NumCtx:       n,
		PromptTokens: resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
		Load:         time.Duration(resp.LoadDuration),
		PromptEval:   time.Duration(resp.PromptEvalDuration),
		Eval:         time.Duration(resp.EvalDuration),
		Total:        time.Duration(resp.TotalDuration),
	}
	return resp.Message.Content, st, nil
}

// ErrModelDown means Ollama stopped answering during a call.
var ErrModelDown = fmt.Errorf("the model is not answering; start Ollama and try again")

// isConnErr is true when Ollama could not be reached or dropped the
// connection; a slow answer that timed out is not one.
func isConnErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	var oe *net.OpError
	return errors.As(err, &oe) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF)
}

func (o *Ollama) post(ctx context.Context, path string, body []byte) (chatResponse, error) {
	var out chatResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("ollama answered %s: %s", resp.Status, truncateBytes(data, 200))
	}
	if resp.StatusCode == http.StatusNotFound && strings.Contains(out.Error, "not found") {
		return out, fmt.Errorf("the model %s is not downloaded; run: ollama pull %s", o.Model, o.Model)
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		return out, fmt.Errorf("ollama answered %s: %s", resp.Status, out.Error)
	}
	return out, nil
}

func truncateBytes(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// Translate asks the model for one language. With a previous answer and a
// problem, the conversation continues so the model fixes exactly that.
func (o *Ollama) Translate(ctx context.Context, req Request) (Output, Stats, error) {
	messages := []chatMessage{
		{Role: "system", Content: SystemPrompt(req.SourceLang, req.Lang, req.Kind, req.TitleLimit, utf8.RuneCountInString(req.Source.Title))},
		{Role: "user", Content: UserMessage(req.Source)},
	}
	if req.Previous != nil && req.Problem != nil {
		messages = append(messages,
			chatMessage{Role: "assistant", Content: marshalPlain(toWire(*req.Previous))},
			chatMessage{Role: "user", Content: RetryMessage(req.Problem)},
		)
	}
	lines := strings.Count(req.Source.Description, "\n") + 1
	content, st, err := o.chat(ctx, messages, schemaFor(lines), 0.2)
	if err != nil {
		if isConnErr(err) {
			return Output{}, st, ErrModelDown
		}
		return Output{}, st, err
	}
	var w wire
	if err := json.Unmarshal([]byte(content), &w); err != nil {
		return Output{}, st, &BadAnswer{Raw: content}
	}
	return w.output(), st, nil
}

// BadAnswer is a reply that is not the JSON that was asked for.
type BadAnswer struct{ Raw string }

func (b *BadAnswer) Error() string { return "the model's answer was not in the expected shape" }

// DetectLanguage asks the model which language a text is in (one short call).
func (o *Ollama) DetectLanguage(ctx context.Context, text string) (string, Stats, error) {
	if r := []rune(text); len(r) > 500 {
		text = string(r[:500])
	}
	messages := []chatMessage{
		{Role: "system", Content: detectSystem},
		{Role: "user", Content: text},
	}
	content, st, err := o.chat(ctx, messages, detectSchema, 0)
	if err != nil {
		return "", st, err
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return "", st, &BadAnswer{Raw: content}
	}
	return strings.ToLower(strings.TrimSpace(out.Code)), st, nil
}

// Unload asks Ollama to drop the model from memory, so the next call is a cold
// start. Used only by `tongues translate --measure --cold`.
func (o *Ollama) Unload(ctx context.Context) error {
	body, _ := json.Marshal(map[string]any{"model": o.Model, "keep_alive": 0})
	_, err := o.post(ctx, "/api/generate", body)
	return err
}

// ModelInfo is what Ollama reports about the model, for the measurements.
type ModelInfo struct {
	Name          string `json:"name"`
	Parameters    string `json:"parameters,omitempty"`   // "25.2B"
	Quantization  string `json:"quantization,omitempty"` // "Q4_K_M"
	DiskBytes     int64  `json:"diskBytes,omitempty"`    // the download, from /api/tags
	MemoryBytes   int64  `json:"memoryBytes,omitempty"`  // in memory, from /api/ps
	GPUBytes      int64  `json:"gpuBytes,omitempty"`     // of that on the GPU, from /api/ps
	ContextLength int    `json:"contextLength,omitempty"`
	Loaded        bool   `json:"loaded"`
}

// Info asks Ollama about o.Model: its download from /api/tags, and its size
// in memory and on the GPU from /api/ps when it is loaded.
func (o *Ollama) Info(ctx context.Context) (ModelInfo, error) {
	info := ModelInfo{Name: o.Model}
	type entry struct {
		Name          string `json:"name"`
		Model         string `json:"model"`
		Size          int64  `json:"size"`
		SizeVRAM      int64  `json:"size_vram"`
		ContextLength int    `json:"context_length"`
		Details       struct {
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
		} `json:"details"`
	}
	var tags, ps struct {
		Models []entry `json:"models"`
	}
	if err := o.getJSON(ctx, "/api/tags", &tags); err != nil {
		return info, err
	}
	for _, m := range tags.Models {
		if sameModel(m.Name, o.Model) || sameModel(m.Model, o.Model) {
			info.DiskBytes = m.Size
			info.Parameters, info.Quantization = m.Details.ParameterSize, m.Details.QuantizationLevel
		}
	}
	if err := o.getJSON(ctx, "/api/ps", &ps); err != nil {
		return info, err
	}
	for _, m := range ps.Models {
		if sameModel(m.Name, o.Model) || sameModel(m.Model, o.Model) {
			info.Loaded = true
			info.MemoryBytes, info.GPUBytes, info.ContextLength = m.Size, m.SizeVRAM, m.ContextLength
		}
	}
	return info, nil
}

func (o *Ollama) getJSON(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama answered %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
