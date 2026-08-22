package main

import (
	"context"
	"os"
)

type keepAliveController interface {
	SetKeepAlivesEnabled(bool)
}

// runKeepAliveDrainSignal disables HTTP keep-alive after SIGUSR1 without
// canceling requests that are already running. The deployment switcher first
// redirects newly-created proxy connections to a healthy candidate slot, then
// sends SIGUSR1 to the old slot. Existing HTTP/1.1 upstream connections finish
// their current request and close instead of accepting another request, which
// gives the switcher a bounded, observable drain without interrupting billing.
func runKeepAliveDrainSignal(
	ctx context.Context,
	server keepAliveController,
	signals <-chan os.Signal,
	onDrain func(),
) {
	if ctx == nil || server == nil || signals == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-signals:
			if !ok {
				return
			}
			server.SetKeepAlivesEnabled(false)
			if onDrain != nil {
				onDrain()
			}
		}
	}
}
