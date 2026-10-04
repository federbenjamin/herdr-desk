package daemon

import "time"

// SetBackupTick shortens the daemon backup schedule for an external package test.
func SetBackupTick(d time.Duration) (restore func()) {
	previous := backupTick
	backupTick = d
	return func() { backupTick = previous }
}
