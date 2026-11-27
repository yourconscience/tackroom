package app

import (
	"os"
	"path/filepath"
)

// sshOptions are the options every ssh call tackroom makes starts with. A call
// never prompts and gives up connecting after ten seconds. The first call to a
// host leaves its connection open for ten minutes behind a socket in the state
// directory, so the calls after it skip the handshake. Keepalives close a
// connection whose network has gone silent, so a later call reconnects instead
// of waiting on it.
func sshOptions() []string {
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
	home, err := os.UserHomeDir()
	if err != nil {
		return opts
	}
	socket := filepath.Join(tackroomStateDir(home), "ssh", "%C")
	// ssh binds the socket under a 17-byte temporary suffix, %C expands to 40
	// characters, and a socket path holds 104 bytes on macOS. A longer path
	// would fail the call, so such a machine connects each time.
	if len(socket)-len("%C")+40+17 >= 104 {
		return opts
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return opts
	}
	return append(opts,
		"-o", "ControlMaster=auto",
		"-o", "ControlPath="+socket,
		"-o", "ControlPersist=10m",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
	)
}
