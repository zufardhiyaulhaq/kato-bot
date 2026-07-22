// Package api is kato-bot's REST proxy front door: kato's API surface,
// prefixed by /api/v1/clusters/{cluster}, passing requests and responses
// through verbatim. All logic lives in internal/gateway.
package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zufardhiyaulhaq/kato-bot/internal/gateway"
)

// maxBodyBytes bounds proxied request bodies; kato inputs/params are tiny.
const maxBodyBytes = 1 << 20

// Register mounts the proxy routes on mux.
func Register(mux *http.ServeMux, g *gateway.Gateway) {
	mux.HandleFunc("GET /api/v1/clusters", func(w http.ResponseWriter, _ *http.Request) {
		writeRaw(w, http.StatusOK, g.ClustersJSON())
	})
	proxy := func(pattern string, call func(cl gateway.Client, r *http.Request) (json.RawMessage, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("cluster")
			cl, ok := g.Get(name)
			if !ok {
				writeGatewayErr(w, gateway.UnknownCluster(name))
				return
			}
			raw, err := call(cl, r)
			if err != nil {
				writeGatewayErr(w, gateway.Normalize(name, err))
				return
			}
			writeRaw(w, http.StatusOK, raw)
		})
	}

	proxy("GET /api/v1/clusters/{cluster}/usecases", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawListUseCases(r.Context())
	})
	proxy("GET /api/v1/clusters/{cluster}/usecases/{name}", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawGetUseCase(r.Context(), r.PathValue("name"))
	})
	proxy("POST /api/v1/clusters/{cluster}/usecases/{name}/run", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		return cl.RawRunUseCase(r.Context(), r.PathValue("name"), r.URL.RawQuery, body)
	})
	proxy("GET /api/v1/clusters/{cluster}/methods", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawListMethods(r.Context())
	})
	proxy("POST /api/v1/clusters/{cluster}/methods/{name}/run", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		return cl.RawRunMethod(r.Context(), r.PathValue("name"), body)
	})
	proxy("GET /api/v1/clusters/{cluster}/runs", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawListRuns(r.Context(), r.URL.RawQuery)
	})
	proxy("GET /api/v1/clusters/{cluster}/runs/{name}", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawGetRun(r.Context(), r.PathValue("name"))
	})
}

// readBody reads a bounded request body ([] for none).
func readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeGatewayErr(w http.ResponseWriter, e *gateway.Error) {
	b, _ := json.Marshal(map[string]string{"error": e.Msg})
	writeRaw(w, e.Status, b)
}
