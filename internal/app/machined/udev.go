package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// waitForUdev waits for udevd to be ready to handle netlink device events.
// It checks for the udev control socket and then runs udevadm settle.
func waitForUdev(ctx context.Context) error {
	// 1. Wait for the udev control socket to exist
	if err := waitForFile("/run/udev/control", ctx); err != nil {
		return fmt.Errorf("waiting for udev control socket: %w", err)
	}

	// 2. Reload udev config
	if err := runUdevadm("control", "--reload"); err != nil {
		// non-fatal, just log
		logf("warning: udevadm control --reload failed: %v\n", err)
	}

	// 3. Trigger device events (if needed)
	if err := runUdevadm("trigger", "--type=devices", "--action=add"); err != nil {
		logf("warning: udevadm trigger failed: %v\n", err)
	}

	// 4. Wait for all pending uevents to be processed
	if err := runUdevadm("settle", "--timeout=10"); err != nil {
		return fmt.Errorf("udevadm settle: %w", err)
	}

	logf("udev settled")
	return nil
}

func waitForFile(path string, ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return nil
			}
		}
	}
}

func runUdevadm(args ...string) error {
	cmd := exec.Command("/sbin/udevadm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
