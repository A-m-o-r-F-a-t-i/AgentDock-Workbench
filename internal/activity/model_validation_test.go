package activity

import (
	"strings"
	"testing"
)

func TestActivityIdentifierValidators(t *testing.T) {
	identifierCases := map[string]bool{
		"a":                     true,
		"Call_01-xyZ":           true,
		strings.Repeat("a", 80): true,
		"":                      false,
		strings.Repeat("a", 81): false,
		"has.dot":               false,
		"has space":             false,
		"é":                     false,
	}
	for value, expected := range identifierCases {
		if actual := validIdentifier(value); actual != expected {
			t.Errorf("validIdentifier(%q) = %v, want %v", value, actual, expected)
		}
	}

	conversationCases := map[string]bool{
		"conv_0123456789abcdef0123456789abcdef": true,
		"conv_0123456789ABCDEF0123456789abcdef": false,
		"conv_0123456789abcdef0123456789abcde":  false,
		"call_0123456789abcdef0123456789abcdef": false,
		"":                                      false,
	}
	for value, expected := range conversationCases {
		if actual := validConversationIdentifier(value); actual != expected {
			t.Errorf("validConversationIdentifier(%q) = %v, want %v", value, actual, expected)
		}
	}

	kindCases := map[string]bool{
		"call.created":           true,
		"command_output.chunk-1": true,
		strings.Repeat("a", 80) + "." + strings.Repeat("b", 80): true,
		"":                                   false,
		"call":                               false,
		".created":                           false,
		"call.":                              false,
		"call.created.extra":                 false,
		strings.Repeat("a", 81) + ".created": false,
		"call.créated":                       false,
	}
	for value, expected := range kindCases {
		if actual := validKind(value); actual != expected {
			t.Errorf("validKind(%q) = %v, want %v", value, actual, expected)
		}
	}
}
