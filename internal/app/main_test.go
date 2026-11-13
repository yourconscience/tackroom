package app

import (
	"os"
	"testing"
)

// No test may install into or read the real scheduler.
func TestMain(m *testing.M) {
	installPeriodicSync = func(cronOptions) error { return nil }
	readPeriodicSync = func() (*cronJob, bool) { return nil, false }
	os.Exit(m.Run())
}
