package helpers

import (
	"fmt"
	"time"

	"github.com/davidg238/porta/internal/st/store"
)

// WaitForVerb queues a command verb to a device and waits up to 5 seconds
// for the result to appear in the data log. Returns the result payload or error.
func WaitForVerb(st *store.Store, device, verb string) (string, error) {
	eui, err := st.ResolveDevice(device)
	if err != nil {
		return "", err
	}
	before := time.Now()
	if err := st.QueueCommand(eui, verb, nil); err != nil {
		return "", err
	}
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		rows, err := st.QueryData(eui, before, time.Now())
		if err != nil {
			return "", err
		}
		if len(rows) > 0 {
			return string(rows[len(rows)-1].Payload), nil
		}
	}
	return "", fmt.Errorf("timeout waiting for %s response from %s", verb, device)
}
