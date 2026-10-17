package core

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBoundedMillisecondsLimitsBeforeDurationConversion(t *testing.T) {
	for _, test := range []struct {
		name                     string
		value, fallback, maximum int
		want                     time.Duration
	}{
		{name: "negative uses fallback", value: -1, fallback: 15000, maximum: 120000, want: 15 * time.Second},
		{name: "custom", value: 2500, fallback: 15000, maximum: 120000, want: 2500 * time.Millisecond},
		{name: "capped", value: math.MaxInt, fallback: 15000, maximum: 120000, want: 2 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := BoundedMilliseconds(test.value, test.fallback, test.maximum); got != test.want {
				t.Fatalf("BoundedMilliseconds() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestIntArgAcceptsOnlyIntegralInRangeValues(t *testing.T) {
	const fallback = 77
	tests := []struct {
		name  string
		value any
		want  int
	}{
		{name: "int", value: 12, want: 12},
		{name: "float integer", value: float64(12), want: 12},
		{name: "json number", value: json.Number("12"), want: 12},
		{name: "trimmed string", value: " 12 ", want: 12},
		{name: "fraction", value: 12.5, want: fallback},
		{name: "nan", value: math.NaN(), want: fallback},
		{name: "positive infinity", value: math.Inf(1), want: fallback},
		{name: "negative infinity", value: math.Inf(-1), want: fallback},
		{name: "huge float", value: math.MaxFloat64, want: fallback},
		{name: "fractional json number", value: json.Number("12.5"), want: fallback},
		{name: "invalid string", value: "twelve", want: fallback},
	}
	if strconv.IntSize == 64 {
		tests = append(tests, struct {
			name  string
			value any
			want  int
		}{name: "overflowing float64", value: float64(1 << 63), want: fallback})
	} else {
		tests = append(tests, struct {
			name  string
			value any
			want  int
		}{name: "overflowing float64", value: float64(1 << 31), want: fallback})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IntArg(map[string]any{"value": test.value}, "value", fallback); got != test.want {
				t.Fatalf("IntArg(%T(%v)) = %d, want %d", test.value, test.value, got, test.want)
			}
		})
	}
}

func TestJSONArgumentsPreserveNumbersAndTypedBounds(t *testing.T) {
	var decoded struct {
		Limit int            `json:"limit"`
		Args  map[string]any `json:"arguments"`
	}
	input := `{"limit":7,"arguments":{"id":9007199254740993,"amount":0.10000000000000001,"items":[18446744073709551615]}}`
	if err := DecodeJSON(strings.NewReader(input), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Limit != 7 || decoded.Args["id"] != json.Number("9007199254740993") || decoded.Args["amount"] != json.Number("0.10000000000000001") || decoded.Args["items"].([]any)[0] != json.Number("18446744073709551615") {
		t.Fatalf("numbers changed at the JSON boundary: %#v", decoded)
	}
	var cloned map[string]any
	if err := Remarshal(decoded.Args, &cloned); err != nil {
		t.Fatal(err)
	}
	decoded.Args["items"].([]any)[0] = json.Number("0")
	if cloned["id"] != json.Number("9007199254740993") || cloned["items"].([]any)[0] != json.Number("18446744073709551615") {
		t.Fatalf("JSON clone changed or aliased the accepted arguments: %#v", cloned)
	}
	for _, invalid := range []string{input + ` {}`, input + ` trailing`, `{"limit":0.5}`, `{"limit":18446744073709551615}`} {
		if err := DecodeJSON(strings.NewReader(invalid), &decoded); err == nil {
			t.Fatalf("accepted invalid typed input or trailing JSON: %s", invalid)
		}
	}
}

func TestJSONTextEquivalentRequiresExactNumbersAndCompleteValue(t *testing.T) {
	structured := map[string]any{"id": json.Number("9007199254740993"), "label": "board"}
	for _, test := range []struct {
		name string
		text string
		want bool
	}{
		{"identical value", ` { "label":"board", "id":9007199254740993 } `, true},
		{"adjacent large integer", `{"id":9007199254740992,"label":"board"}`, false},
		{"different receipt", `{"structuredContent":true,"id":9007199254740993}`, false},
		{"trailing value", `{"label":"board","id":9007199254740993} {}`, false},
		{"trailing junk", `{"label":"board","id":9007199254740993} receipt`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := JSONTextEquivalent(test.text, structured); got != test.want {
				t.Fatalf("equivalent = %v, want %v", got, test.want)
			}
		})
	}
}
