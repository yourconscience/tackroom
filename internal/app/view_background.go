package app

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// `tackroom view` makes sure a view runs in the background, then opens it.
// The background view is `tackroom view --foreground` in its own session with
// viewDetachedEnv set, which turns on the idle exit.

const (
	defaultViewAddr        = "127.0.0.1:8765"
	defaultViewIdleTimeout = "30m"
	viewDetachedEnv        = "TACKROOM_VIEW_DETACHED"
	viewStartTimeout       = 10 * time.Second
)

// viewFiles are the state files of one view, named after its token file:
// view.token, view.addr, view.pid, view.log and view.lock by default.
type viewFiles struct {
	token, addr, pid, log, lock string
}

func viewStateFiles(tokenFile string) viewFiles {
	stem := strings.TrimSuffix(tokenFile, filepath.Ext(tokenFile))
	return viewFiles{token: tokenFile, addr: stem + ".addr", pid: stem + ".pid", log: stem + ".log", lock: stem + ".lock"}
}

// viewJSON is `tackroom view --json`. Field names are part of the CLI contract.
type viewJSON struct {
	URL         string `json:"url"`
	Addr        string `json:"addr"`
	PID         int    `json:"pid"`
	Started     bool   `json:"started"`
	IdleTimeout string `json:"idle_timeout"`
}

// applyViewSettings applies ui.view to opts: its addr when --addr was not
// given, and open_browser: false as --no-open. It returns the idle timeout a
// background view gets.
func applyViewSettings(opts *configServeOptions, ui *uiConfig) string {
	if ui == nil || ui.View == nil {
		return defaultViewIdleTimeout
	}
	view := ui.View
	if !opts.AddrSet && view.Addr != "" {
		opts.Addr = view.Addr
	}
	if view.OpenBrowser != nil && !*view.OpenBrowser {
		opts.NoOpen = true
	}
	if view.IdleTimeout != "" {
		return view.IdleTimeout
	}
	return defaultViewIdleTimeout
}

// runByServiceManager reports whether launchd or systemd runs this process.
// launchd sets XPC_SERVICE_NAME to the job's label; terminals set "0", and
// apps it opens get "application.<bundle id>...", which the programs they run
// inherit. systemd sets INVOCATION_ID.
func runByServiceManager(getenv func(string) string) bool {
	if name := getenv("XPC_SERVICE_NAME"); name != "" && name != "0" && !strings.HasPrefix(name, "application.") {
		return true
	}
	return getenv("INVOCATION_ID") != ""
}

func parseViewIdleTimeout(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("want a duration such as 30m, or 0 to never exit, got %q", value)
	}
	return d, nil
}

// ensureView reports the running view, starting one in the background first
// when none answers, then opens it unless told not to.
func ensureView(doc *configDocument, opts configServeOptions, files viewFiles, token, idle string) error {
	info, started, err := runOrStartView(doc, opts, files, token)
	if err != nil {
		return err
	}
	if info.IdleTimeout == "" {
		info.IdleTimeout = idle
	}
	if opts.JSON {
		return encodeJSON(os.Stdout, viewJSON{URL: info.URL, Addr: info.Addr, PID: info.PID, Started: started, IdleTimeout: info.IdleTimeout})
	}
	lifetime := "it runs until tackroom view --stop"
	if d, err := parseViewIdleTimeout(info.IdleTimeout); err == nil && d > 0 {
		lifetime = "it exits after " + info.IdleTimeout + " without requests, or on tackroom view --stop"
	}
	if started {
		fmt.Fprintf(os.Stdout, "\nStarted tackroom view in the background (pid %d); %s.\n", info.PID, lifetime)
	} else {
		fmt.Fprintf(os.Stdout, "\ntackroom view is already running (pid %d); %s.\n", info.PID, lifetime)
	}
	announceView(os.Stdout, info.URL, opts, os.Getenv("SSH_CONNECTION") != "", resolveSSHHost(opts.SSHHost, os.Getenv))
	return nil
}

// runOrStartView returns the running view, or starts one and returns it. The
// lock makes check-and-start one step, so concurrent calls start one view.
func runOrStartView(doc *configDocument, opts configServeOptions, files viewFiles, token string) (runningViewInfo, bool, error) {
	if info, ok := runningView(opts, files.addr, token); ok {
		return info, false, nil
	}
	unlock, err := lockViewStart(files.lock)
	if err != nil {
		return runningViewInfo{}, false, err
	}
	defer unlock()
	// Another call may have started one while this one waited for the lock.
	if info, ok := runningView(opts, files.addr, token); ok {
		return info, false, nil
	}
	return startView(doc, opts, files, token)
}

// startView runs `tackroom view --foreground` detached, in a new session with
// its output in the log file, and waits until it answers. A view that does not
// answer in time is stopped, so a failed start leaves no process behind.
func startView(doc *configDocument, opts configServeOptions, files viewFiles, token string) (runningViewInfo, bool, error) {
	self, err := os.Executable()
	if err != nil {
		return runningViewInfo{}, false, fmt.Errorf("find the tackroom binary: %w", err)
	}
	args := []string{"view", "--foreground", "--no-open", "--config", doc.sharedPath, "--token-file", files.token}
	if opts.AddrSet {
		args = append(args, "--addr", opts.Addr)
	}
	if opts.SecureCookie {
		args = append(args, "--secure-cookie")
	}
	// Appended, so a failed start does not wipe the log of a view that runs
	// elsewhere; logStart is where this start's lines begin.
	logFile, err := os.OpenFile(files.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return runningViewInfo{}, false, err
	}
	var logStart int64
	if info, err := logFile.Stat(); err == nil {
		logStart = info.Size()
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Dir = doc.home
	cmd.Env = append(os.Environ(), viewDetachedEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	logFile.Close()
	if err != nil {
		return runningViewInfo{}, false, fmt.Errorf("start tackroom view: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	deadline := time.After(viewStartTimeout)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if info, ok := runningView(opts, files.addr, token); ok {
			return info, info.PID == cmd.Process.Pid, nil
		}
		select {
		case <-exited:
			// It exits at once when another view already answers.
			if info, ok := runningView(opts, files.addr, token); ok {
				return info, false, nil
			}
			removeViewPID(files.pid, cmd.Process.Pid)
			return runningViewInfo{}, false, viewStartError(files.log, logStart, "tackroom view exited before it answered")
		case <-deadline:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				_ = cmd.Process.Kill()
				<-exited
			}
			removeViewPID(files.pid, cmd.Process.Pid)
			return runningViewInfo{}, false, viewStartError(files.log, logStart, fmt.Sprintf("tackroom view did not answer within %s and was stopped", viewStartTimeout))
		case <-tick.C:
		}
	}
}

// viewStartError is reason followed by the last lines this start wrote to
// the view's log.
func viewStartError(logPath string, from int64, reason string) error {
	data, _ := os.ReadFile(logPath)
	if from > 0 && from <= int64(len(data)) {
		data = data[from:]
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	tail := strings.TrimSpace(strings.Join(lines, "\n"))
	if tail == "" {
		return errors.New(reason)
	}
	return fmt.Errorf("%s; the end of %s:\n%s", reason, logPath, tail)
}

// stopView stops the view that answers for this token, by the process ID it
// reports. It is not an error when none runs.
func stopView(opts configServeOptions, files viewFiles) error {
	data, err := os.ReadFile(files.token)
	token := strings.TrimSpace(string(data))
	info, ok := runningViewInfo{}, false
	if err == nil && token != "" {
		info, ok = runningView(opts, files.addr, token)
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "No tackroom view is running; nothing to stop.")
		return nil
	}
	if info.PID == 0 {
		fmt.Fprintf(os.Stdout, "The tackroom view at %s does not report its process ID (an older tackroom started it); stop it where it runs.\n", info.Addr)
		return nil
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop tackroom view (pid %d): %w", info.PID, err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if _, ok := runningView(opts, files.addr, token); !ok {
			fmt.Fprintf(os.Stdout, "Stopped tackroom view at %s (pid %d).\n", info.Addr, info.PID)
			return nil
		}
	}
	return fmt.Errorf("tackroom view (pid %d) still answers at %s 5s after SIGTERM", info.PID, info.Addr)
}

// lockViewStart takes the exclusive lock on path. The holder removes the file
// when it releases it; a waiter that then holds the removed file retries on a
// new one, so no lock file stays behind and no two holders overlap.
func lockViewStart(path string) (func(), error) {
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		held, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		current, err := os.Stat(path)
		if err == nil && os.SameFile(held, current) {
			return func() {
				_ = os.Remove(path)
				f.Close()
			}, nil
		}
		f.Close()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
}

// removeViewPID removes the pid file when it still names pid.
func removeViewPID(path string, pid int) {
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(pid) {
		_ = os.Remove(path)
	}
}

// exitWhenIdle wraps h so that srv closes once no request has started or
// finished for idle and none is in flight.
func exitWhenIdle(srv *http.Server, h http.Handler, idle time.Duration) http.Handler {
	var last, active atomic.Int64
	last.Store(time.Now().UnixNano())
	go func() {
		for {
			wait := idle - time.Since(time.Unix(0, last.Load()))
			if wait <= 0 && active.Load() == 0 {
				fmt.Fprintf(os.Stderr, "no requests for %s; tackroom view exits\n", idle)
				_ = srv.Close()
				return
			}
			if wait <= 0 {
				wait = idle
			}
			time.Sleep(wait)
		}
	}()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		last.Store(time.Now().UnixNano())
		defer func() {
			last.Store(time.Now().UnixNano())
			active.Add(-1)
		}()
		h.ServeHTTP(w, r)
	})
}
