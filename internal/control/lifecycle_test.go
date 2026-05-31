package control

import (
	"database/sql"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func delivered(ts int64) sql.NullInt64 { return sql.NullInt64{Int64: ts, Valid: true} }

// observed decodes a node's observed_state JSON the same way production does.
func observed(t *testing.T, blob string) map[string]map[string]any {
	t.Helper()
	return ConfigFromObserved(blob)
}

func TestLifecycleOf(t *testing.T) {
	const maxOffline = 300
	now := int64(10_000)
	setArgs := `{"app":"vin","key":"gain","value":5}`

	tests := []struct {
		name string
		cmd  store.Command
		obs  map[string]map[string]any
		want Lifecycle
	}{
		{
			name: "queued: undelivered, fresh",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - 10},
			want: LifecycleQueued,
		},
		{
			name: "expired: undelivered past max_offline",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - maxOffline - 1},
			want: LifecycleExpired,
		},
		{
			name: "delivered: set, observed does not match yet",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - 100, DeliveredAt: delivered(now - 50)},
			obs:  observed(t, `{"config":{"vin":{"gain":4}}}`),
			want: LifecycleDelivered,
		},
		{
			name: "converged: set, observed matches desired",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - 100, DeliveredAt: delivered(now - 50)},
			obs:  observed(t, `{"config":{"vin":{"gain":5}}}`),
			want: LifecycleConverged,
		},
		{
			name: "delivered terminal: non-set verb never converges",
			cmd:  store.Command{Verb: "set-console", Args: `{"on":true}`, IssuedAt: now - 100, DeliveredAt: delivered(now - 50)},
			obs:  observed(t, `{"config":{"vin":{"gain":5}}}`),
			want: LifecycleDelivered,
		},
		{
			name: "expiry boundary: exactly max_offline is expired",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - maxOffline},
			want: LifecycleExpired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LifecycleOf(tt.cmd, tt.obs, maxOffline, now); got != tt.want {
				t.Errorf("LifecycleOf = %q, want %q", got, tt.want)
			}
		})
	}
}
