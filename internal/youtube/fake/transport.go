package fake

import (
	"net/http"
	"net/http/httptest"
)

// Endpoint is the base URL to pair with Client: requests never leave the process.
const Endpoint = "http://fake-youtube.invalid/"

// Client returns an HTTP client that answers every request with the fake.
func (a *API) Client() *http.Client {
	return &http.Client{Transport: roundTripper{a}}
}

type roundTripper struct{ h http.Handler }

func (rt roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rt.h.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

// Channels lists the fixture channels as id and title pairs.
func (a *API) Channels() [][2]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out [][2]string
	for _, ch := range a.channels {
		snip, _ := ch["snippet"].(map[string]any)
		title, _ := snip["title"].(string)
		out = append(out, [2]string{ch["id"].(string), title})
	}
	return out
}
