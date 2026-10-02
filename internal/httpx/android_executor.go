package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/uvwt/agentdock/internal/androidbridge"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
)

const androidExecutorPrefix = "/internal/runtime/android-executor/"

type androidExecutorRuntime interface{ AndroidExecutor() *androidbridge.Broker }

func androidExecutorHandler(broker *androidbridge.Broker, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !directLoopbackRequest(r) {
			writeRuntimeAPIError(w, 403, "LOCAL_ONLY", "Android provider traffic requires a direct loopback connection")
			return
		}
		if broker == nil {
			writeRuntimeAPIError(w, 503, "ANDROID_UNAVAILABLE", "Android provider transport is unavailable")
			return
		}
		action := strings.TrimPrefix(r.URL.Path, androidExecutorPrefix)
		if r.URL.RawQuery != "" || r.URL.Path != androidExecutorPrefix+action {
			writeRuntimeAPIError(w, 400, "INVALID_ARGUMENT", "query parameters are not accepted")
			return
		}
		rootAuth := (auth.Bearer{Token: cfg.AuthToken})
		if action == "register" || action == "status" {
			// A provider is privileged even on a Core configured for otherwise
			// unauthenticated local reads. Never bootstrap with an empty secret.
			if cfg.AuthToken == "" || !rootAuth.Authorized(r) {
				writeRuntimeAPIError(w, 401, "UNAUTHORIZED", "authenticated local Core pairing required")
				return
			}
		}
		if action == "status" {
			if r.Method != http.MethodGet {
				writeRuntimeAPIError(w, 405, "METHOD_NOT_ALLOWED", "GET required")
				return
			}
			writeJSON(w, broker.Status())
			return
		}
		if r.Method != http.MethodPost {
			writeRuntimeAPIError(w, 405, "METHOD_NOT_ALLOWED", "POST required")
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			writeRuntimeAPIError(w, 415, "JSON_REQUIRED", "application/json required")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, androidbridge.MaxRequestBytes))
		decoder.DisallowUnknownFields()
		decode := func(value any) error {
			if err := decoder.Decode(value); err != nil {
				return androidbridge.ErrProtocol
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return androidbridge.ErrProtocol
			}
			return nil
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var result any
		switch action {
		case "register":
			var value androidbridge.RegisterRequest
			err = decode(&value)
			if err == nil {
				result, err = broker.Register(value)
			}
		case "exchange":
			var value androidbridge.ExchangeRequest
			err = decode(&value)
			if err == nil {
				result, err = broker.Exchange(token, value)
			}
		case "disconnect":
			var value struct{}
			err = decode(&value)
			if err == nil {
				err = broker.Disconnect(token)
				result = map[string]any{"ok": err == nil}
			}
		default:
			writeRuntimeAPIError(w, 404, "NOT_FOUND", "Android provider route not found")
			return
		}
		if err != nil {
			status, code := 400, "ANDROID_PROTOCOL_ERROR"
			switch {
			case errors.Is(err, androidbridge.ErrUnauthorized):
				status, code = 401, "ANDROID_LEASE_INVALID"
			case errors.Is(err, androidbridge.ErrConflict):
				status, code = 409, "ANDROID_LEASE_CONFLICT"
			case errors.Is(err, androidbridge.ErrOffline):
				status, code = 503, "ANDROID_OFFLINE"
			}
			writeRuntimeAPIError(w, status, code, "Android provider request was rejected; no command was replayed")
			return
		}
		writeJSON(w, result)
	}
}
