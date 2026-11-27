package app

import (
	"os"
	"path/filepath"
	"testing"
)

// No test may install into or read the real scheduler, or reach a running
// Ouroboros through the settings location it exports to its children. Detect
// Ouroboros at a scratch path: unsetting the variables below would make
// detection fall back to ~/Ouroboros/data/settings.json — the live install on a
// machine that runs Ouroboros — and break the detection tests.
func TestMain(m *testing.M) {
	os.Setenv("OUROBOROS_DATA_DIR", filepath.Join(os.TempDir(), "tackroom-test", "ouroboros"))
	os.Unsetenv("OUROBOROS_SETTINGS_PATH")
	os.Unsetenv("OUROBOROS_APP_ROOT")
	os.Unsetenv("OUROBOROS_SETTINGS_SHA256")
	installPeriodicSync = func(cronOptions) error { return nil }
	readPeriodicSync = func() (*cronJob, bool) { return nil, false }
	os.Exit(m.Run())
}
