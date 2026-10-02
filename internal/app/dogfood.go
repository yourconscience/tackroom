package app

import (
	"fmt"
)

func runDogfood(opts runOptions) error {
	fmt.Println("tackroom dogfood")
	fmt.Println()

	fmt.Println("--- sync ---")
	if err := runSync(opts); err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}
	fmt.Println()

	fmt.Println("--- status ---")
	if err := runStatus(opts); err != nil {
		return fmt.Errorf("status check failed after sync: %w", err)
	}
	fmt.Println()

	fmt.Println("--- doctor ---")
	if err := runDoctor(opts); err != nil {
		return fmt.Errorf("doctor found issues: %w", err)
	}
	fmt.Println()

	fmt.Println("dogfood: all checks passed")
	return nil
}
