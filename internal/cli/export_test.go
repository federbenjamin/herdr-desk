package cli

import "time"

// SetInfoWait shortens how long `desk daemon run` waits for the running daemon's info file.
func SetInfoWait(d time.Duration) (restore func()) {
	previous := infoWait
	infoWait = d
	return func() { infoWait = previous }
}
