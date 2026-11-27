package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sshOptions are the options every ssh call tackroom makes to host starts
// with. A call never prompts and gives up connecting after ten seconds. The
// first call to a host leaves its connection open for ten minutes behind a
// socket in the state directory, so the calls after it skip the handshake.
// Keepalives close a connection whose network has gone silent, so a later call
// reconnects instead of waiting on it.
func sshOptions(host string) []string {
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
	home, err := os.UserHomeDir()
	if err != nil {
		return opts
	}
	// The socket is named after everything ssh resolves for this host, so two
	// aliases share a connection only when their whole configuration matches,
	// and a changed ~/.ssh/config stops using the connection it opened before.
	resolved, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return opts
	}
	sum := sha256.Sum256(append([]byte(host+"\n"), resolved...))
	socket := filepath.Join(tackroomStateDir(home), "ssh", hex.EncodeToString(sum[:8]))
	// ssh binds the socket under a 17-byte temporary suffix, and a socket path
	// holds 104 bytes on macOS. A longer path would fail the call, so such a
	// machine connects each time.
	if len(socket)+17 >= 104 {
		return opts
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return opts
	}
	return append(opts,
		"-o", "ControlMaster=auto",
		// ssh expands percent tokens in the path; the directory is literal.
		"-o", "ControlPath="+strings.ReplaceAll(socket, "%", "%%"),
		"-o", "ControlPersist=10m",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
	)
}
