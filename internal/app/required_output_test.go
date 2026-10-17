package app

import "testing"

func TestRequiredSourceReadOnlyWhenRecoveryIsNeeded(t *testing.T) {
	for _, test := range []struct {
		name           string
		hasMore        bool
		sourceComplete bool
		page           Result
		want           bool
	}{
		{name: "complete single page", sourceComplete: true, page: Result{}, want: false},
		{name: "remaining required page", hasMore: true, sourceComplete: true, page: Result{}, want: true},
		{name: "partial source", sourceComplete: false, page: Result{}, want: true},
		{name: "indivisible unit", sourceComplete: true, page: Result{"required_unit_unavailable": true}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := requiredSourceReadNeeded(test.hasMore, test.sourceComplete, test.page); got != test.want {
				t.Fatalf("requiredSourceReadNeeded() = %t, want %t", got, test.want)
			}
		})
	}
}
