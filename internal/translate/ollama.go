// Package translate turns a video's title and description into other
// languages with a local model: protect the parts that must not change,
// translate, check the result, restore.
package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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

// Health tells the three states apart: not reachable, model missing, ready.
func (o *Ollama) Health(ctx context.Context) (ModelState, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return ModelDown, err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return ModelDown, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelDown, fmt.Errorf("ollama answered %s", resp.Status)
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return ModelDown, fmt.Errorf("ollama tags: %w", err)
	}
	for _, m := range tags.Models {
		if sameModel(m.Name, o.Model) || sameModel(m.Model, o.Model) {
			return ModelReady, nil
		}
	}
	return ModelMissing, nil
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
