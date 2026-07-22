package kato

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The raw surface forwards method, path, query, and body verbatim, and returns
// kato's response bytes verbatim (no decode/re-encode).
func TestRaw_VerbatimForwarding(t *testing.T) {
	const katoJSON = `{"methods":[{"name":"pod_status","params":[{"Name":"pod","Required":true}]}]}`
	tests := []struct {
		name     string
		call     func(c *Client) ([]byte, error)
		wantMeth string
		wantURI  string // path + rawquery as kato receives it
		wantBody string
	}{
		{"list usecases", func(c *Client) ([]byte, error) { return c.RawListUseCases(context.Background()) },
			"GET", "/api/v1/usecases", ""},
		{"get usecase escapes name", func(c *Client) ([]byte, error) { return c.RawGetUseCase(context.Background(), "a b") },
			"GET", "/api/v1/usecases/a%20b", ""},
		{"run usecase with query+body", func(c *Client) ([]byte, error) {
			return c.RawRunUseCase(context.Background(), "pod-x", "includeOutputs=true", []byte(`{"inputs":{"ns":"a"}}`))
		}, "POST", "/api/v1/usecases/pod-x/run?includeOutputs=true", `{"inputs":{"ns":"a"}}`},
		{"list methods", func(c *Client) ([]byte, error) { return c.RawListMethods(context.Background()) },
			"GET", "/api/v1/methods", ""},
		{"run method", func(c *Client) ([]byte, error) {
			return c.RawRunMethod(context.Background(), "pod_status", []byte(`{"params":{"pod":"x"}}`))
		}, "POST", "/api/v1/methods/pod_status/run", `{"params":{"pod":"x"}}`},
		{"list runs with query", func(c *Client) ([]byte, error) { return c.RawListRuns(context.Background(), "usecase=pod-x") },
			"GET", "/api/v1/runs?usecase=pod-x", ""},
		{"get run", func(c *Client) ([]byte, error) { return c.RawGetRun(context.Background(), "run-1") },
			"GET", "/api/v1/runs/run-1", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotReq *http.Request
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotReq = r
				b := make([]byte, 4096)
				n, _ := r.Body.Read(b)
				gotBody = string(b[:n])
				_, _ = w.Write([]byte(katoJSON))
			}))
			defer srv.Close()
			c := New(srv.URL, 5*time.Second, false)

			raw, err := tc.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if string(raw) != katoJSON {
				t.Errorf("raw = %s, want verbatim %s", raw, katoJSON)
			}
			if gotReq.Method != tc.wantMeth {
				t.Errorf("method = %s, want %s", gotReq.Method, tc.wantMeth)
			}
			if got := gotReq.URL.RequestURI(); got != tc.wantURI {
				t.Errorf("uri = %s, want %s", got, tc.wantURI)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body = %q, want %q", gotBody, tc.wantBody)
			}
		})
	}
}

// Non-2xx surfaces as *APIError with kato's status and extracted message.
func TestRaw_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"too many concurrent method runs"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, 5*time.Second, false)

	_, err := c.RawRunMethod(context.Background(), "pod_status", []byte(`{}`))
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.Status != 429 || apiErr.Msg != "too many concurrent method runs" {
		t.Errorf("APIError = %d %q, want 429 + message", apiErr.Status, apiErr.Msg)
	}
}
