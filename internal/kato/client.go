// Package kato is the REST client for the kato operator's HTTP API.
package kato

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// Client talks to kato's REST API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a Client. timeout bounds each request (incl. the slow /run).
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// APIError is a non-2xx response from kato. Status is the HTTP code; Msg is kato's
// {"error":...} body when present, else the raw body.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("kato %d: %s", e.Status, e.Msg) }

// HTTPStatus and Detail satisfy core.HTTPStatusError so the core can map kato failures
// to friendly, status-aware messages without importing this package.
func (e *APIError) HTTPStatus() int { return e.Status }
func (e *APIError) Detail() string  { return e.Msg }

// asAPIError is errors.As specialized for *APIError (kept here so tests can use it).
func asAPIError(err error, target **APIError) bool { return errors.As(err, target) }

// do performs a request and returns the body on 2xx, or an *APIError otherwise.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err // transport/timeout: surfaced as a plain error
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Status: resp.StatusCode, Msg: extractErr(raw)}
	}
	return raw, nil
}

// extractErr pulls {"error":"..."} from a kato error body, falling back to the raw text.
func extractErr(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(raw))
}

// wire types mirror kato's JSON exactly.
type wireInput struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}
type wireUseCase struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Inputs      []wireInput `json:"inputs"`
	Ready       bool        `json:"ready"`
}

func (w wireUseCase) toContract() core.Contract {
	c := core.Contract{Name: w.Name, Description: w.Description}
	for _, in := range w.Inputs {
		c.Inputs = append(c.Inputs, core.InputDecl{Name: in.Name, Required: in.Required})
	}
	return c
}

func (c *Client) ListUseCases(ctx context.Context) ([]core.UseCase, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/usecases", nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		UseCases []wireUseCase `json:"usecases"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode usecases: %w", err)
	}
	out := make([]core.UseCase, 0, len(body.UseCases))
	for _, w := range body.UseCases {
		out = append(out, core.UseCase{Name: w.Name, Description: w.Description, Ready: w.Ready})
	}
	return out, nil
}

func (c *Client) GetUseCase(ctx context.Context, name string) (core.Contract, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/usecases/"+url.PathEscape(name), nil)
	if err != nil {
		return core.Contract{}, err
	}
	var w wireUseCase
	if err := json.Unmarshal(raw, &w); err != nil {
		return core.Contract{}, fmt.Errorf("decode usecase: %w", err)
	}
	return w.toContract(), nil
}

func (c *Client) Run(ctx context.Context, name string, inputs map[string]string) (core.RunResult, error) {
	if inputs == nil {
		inputs = map[string]string{}
	}
	reqBody, err := json.Marshal(struct {
		Inputs map[string]string `json:"inputs"`
	}{Inputs: inputs})
	if err != nil {
		return core.RunResult{}, err
	}
	// includeOutputs=false: the bot shows the summary, not raw step outputs (v1).
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/usecases/"+url.PathEscape(name)+"/run?includeOutputs=false", bytes.NewReader(reqBody))
	if err != nil {
		return core.RunResult{}, err
	}
	var w struct {
		Run     string `json:"run"`
		Phase   string `json:"phase"`
		Summary string `json:"summary"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return core.RunResult{}, fmt.Errorf("decode run: %w", err)
	}
	return core.RunResult{Run: w.Run, Phase: w.Phase, Summary: w.Summary, Warning: w.Warning}, nil
}

var _ core.KatoClient = (*Client)(nil)
