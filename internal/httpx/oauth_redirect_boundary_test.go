package httpx

import (
	"github.com/uvwt/agentdock/internal/auth"
	"testing"
)

func TestSecurityOAuthRedirectUsesExactRegisteredValue(t *testing.T) {
	registered := "https://client.example/callback?original=1"
	registration := auth.OAuthClientRegistration{RedirectURIs: []string{registered}}
	if got, ok := registeredOAuthRedirect(registration, " "+registered+" "); !ok || got != registered {
		t.Fatalf("registered redirect: %q %v", got, ok)
	}
	for _, raw := range []string{"https://client.example.attacker.invalid/callback?original=1", "https://client.example/callback?original=2", "http://client.example/callback?original=1", "//client.example/callback", "https://client.example:444/callback?original=1", "https://client.example/callback/../other"} {
		if got, ok := registeredOAuthRedirect(registration, raw); ok || got != "" {
			t.Errorf("unregistered redirect accepted: %q", raw)
		}
	}
}
