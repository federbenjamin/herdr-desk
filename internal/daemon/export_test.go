package daemon

import (
	"context"
	"net"
	"net/http"
	"time"
)

// SetBackupTick shortens the daemon backup schedule for an external package test.
func SetBackupTick(d time.Duration) (restore func()) {
	previous := backupTick
	backupTick = d
	return func() { backupTick = previous }
}

// Serve makes a running daemon also serve ln, so a test can hand it a listener that fails.
func Serve(i *Instance, ln net.Listener) {
	i.serve(ln, http.NotFoundHandler())
}

// Wait is what Run does once Start returns.
func Wait(ctx context.Context, i *Instance) error { return i.wait(ctx) }
