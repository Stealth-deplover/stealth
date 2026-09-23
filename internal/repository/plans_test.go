package repository

import "testing"

func TestAppPlanLimits(t *testing.T) {
	tests := []struct {
		plan string
		want int64
	}{
		{plan: "free", want: 3},
		{plan: "pro", want: 25},
		{plan: "enterprise", want: -1},
	}
	for _, test := range tests {
		t.Run(test.plan, func(t *testing.T) {
			if got := planDefinitionForKey(test.plan).Limits.Apps; got != test.want {
				t.Fatalf("Apps limit for %s = %d, want %d", test.plan, got, test.want)
			}
		})
	}
}
