package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/androidbridge"
	"github.com/uvwt/agentdock/internal/config"
)

func TestAndroidExecutorRejectsUnpairedProxyAndRemoteRequests(t *testing.T) {
	for _, tc := range []struct {
		name, remote, host, token, forward string
		code                               int
	}{
		{"no credential", "127.0.0.1:4000", "127.0.0.1:8765", "", "", 401},
		{"remote", "192.0.2.1:4000", "127.0.0.1:8765", "secret", "", 403},
		{"proxy", "127.0.0.1:4000", "127.0.0.1:8765", "secret", "127.0.0.1", 403},
		{"public host", "127.0.0.1:4000", "public.example", "secret", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := androidbridge.New("node")
			defer b.Close()
			h := androidExecutorHandler(b, config.Config{AuthToken: "secret"})
			r := httptest.NewRequest("GET", androidExecutorPrefix+"status", nil)
			r.RemoteAddr = tc.remote
			r.Host = tc.host
			r.Header.Set("Authorization", "Bearer "+tc.token)
			if tc.forward != "" {
				r.Header.Set("X-Forwarded-For", tc.forward)
			}
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
func TestAndroidExecutorLeaseIsRestrictedToExchange(t *testing.T) {
	b := androidbridge.New("node")
	defer b.Close()
	h := androidExecutorHandler(b, config.Config{AuthToken: "secret"})
	send := func(action, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, androidExecutorPrefix+action, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:4000"
		r.Host = "127.0.0.1:8765"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	register := send("register", "secret", `{"protocol":1,"worker_id":"abcdefghijklmnop","backends":{}}`)
	if register.Code != 200 {
		t.Fatal(register.Code, register.Body.String())
	}
	var lease androidbridge.Registration
	if err := json.Unmarshal(register.Body.Bytes(), &lease); err != nil {
		t.Fatal(err)
	}
	if w := send("register", lease.LeaseToken, `{"protocol":1}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := send("exchange", lease.LeaseToken, `{"protocol":1,"events":[],"backends":{}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send("exchange", "secret", `{"protocol":1,"events":[],"backends":{}}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := send("exchange", lease.LeaseToken, `{"protocol":1,"events":[],"backends":{},"command":"id"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
