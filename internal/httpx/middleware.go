package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/uvwt/agentdock/internal/requesttrace"
)

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	writeFailed bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	// Informational headers do not commit the final status.
	if status >= 100 && status < 200 {
		r.ResponseWriter.WriteHeader(status)
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(data)
	r.bytes += n
	if err != nil {
		r.writeFailed = true
	}
	return n, err
}
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		tracked := r.URL.Path == "/mcp" || r.URL.Path == "/context"
		if tracked {
			candidate := ""
			if values := r.Header.Values(requesttrace.Header); len(values) == 1 {
				candidate = values[0]
			}
			ctx, err := requesttrace.Ensure(r.Context(), candidate)
			if err != nil {
				http.Error(w, "request correlation unavailable", http.StatusInternalServerError)
				return
			}
			r = r.WithContext(ctx)
			w.Header().Set(requesttrace.Header, requesttrace.ID(ctx))
			slog.Info("http request received", "request_id", requesttrace.ID(ctx), "method", r.Method, "path", r.URL.Path)
		}
		recorder := &statusRecorder{ResponseWriter: w}
		panicked := true
		defer func() {
			status := recorder.status
			if status == 0 && !panicked {
				status = http.StatusOK
			}
			attrs := []any{"method", r.Method, "path", r.URL.Path, "status", status, "bytes", recorder.bytes, "duration_ms", time.Since(started).Milliseconds(), "remote", r.RemoteAddr}
			if tracked {
				trace := requesttrace.Read(r.Context())
				attrs = append(attrs, "request_id", trace.RequestID, "call_id", trace.CallID, "stage", trace.Stage,
					"handler_dispatched", trace.HandlerDispatched, "request_cancelled", r.Context().Err() != nil,
					"response_write_failed", recorder.writeFailed, "handler_panicked", panicked)
			}
			// Never log headers, body, panic values, tokens or callback error text.
			slog.Info("http request", attrs...)
		}()
		next.ServeHTTP(recorder, r)
		panicked = false
	})
}
