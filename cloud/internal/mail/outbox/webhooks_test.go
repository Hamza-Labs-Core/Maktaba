package outbox

import (
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
)

func TestRateAlerts(t *testing.T) {
	cases := []struct {
		name        string
		sent        int
		hardBounced int
		complained  int
		want        []string
	}{
		{"nothing sent", 0, 5, 1, nil},
		{"healthy", 1000, 10, 0, nil},
		{"single typo at small volume does not false-alarm", 10, 1, 0, nil},
		{"single hard bounce at >= 20 sent nearing 4 pct", 30, 1, 0, []string{mail.AlertBounceRate}},
		{"two hard bounces at small volume alerts", 10, 2, 0, []string{mail.AlertBounceRate}},
		{"below 3 pct bounce", 100, 2, 0, nil},
		{"at 3 pct bounce", 100, 3, 0, []string{mail.AlertBounceRate}},
		{"complaint below 0.06 pct", 2000, 0, 1, nil},
		{"complaint at 0.06 pct", 1600, 0, 1, []string{mail.AlertComplaintRate}},
		{"complaint at 0.08 pct", 1250, 0, 1, []string{mail.AlertComplaintRate}},
		{"both alerts", 100, 4, 1, []string{mail.AlertBounceRate, mail.AlertComplaintRate}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RateAlerts(tc.sent, tc.hardBounced, tc.complained)
			if !slices.Equal(got, tc.want) {
				t.Errorf("RateAlerts(%d, %d, %d) = %v, want %v", tc.sent, tc.hardBounced, tc.complained, got, tc.want)
			}
		})
	}
}
