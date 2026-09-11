package services

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/siderolabs/talos/pkg/conditions"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/health"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner/process"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner/restart"
)

// Tailscaled implements the Service interface for tailscaled.
type Tailscaled struct{}

// ID implements the Service interface.
func (c *Tailscaled) ID(runtime.Runtime) string {
	return "tailscaled"
}

// PreFunc implements the Service interface.
func (c *Tailscaled) PreFunc(ctx context.Context, r runtime.Runtime) error {
	if err := os.MkdirAll("/run/tailscale-logs", 0700); err != nil {
		log.Printf("mkdir log dir: %w", err)
		return err
	}
	return nil
}

// PostFunc implements the Service interface.
func (c *Tailscaled) PostFunc(runtime.Runtime, interface{}) error {
	return nil
}

// Condition implements the Service interface.
func (c *Tailscaled) Condition(runtime.Runtime) conditions.Condition {
	return nil
}

// DependsOn implements the Service interface.
func (c *Tailscaled) DependsOn(runtime.Runtime) []string {
	return []string{"udevd"}
}

// Volumes implements the Service interface.
func (c *Tailscaled) Volumes(runtime.Runtime) []string {
	return nil
}

// Runner implements the Service interface.
func (c *Tailscaled) Runner(r runtime.Runtime) (runner.Runner, error) {
	args := &runner.Args{
		ID: c.ID(r),
		ProcessArgs: []string{
			"/usr/sbin/tailscaled",
			"--statedir=/run",
			"--state=mem:",
		},
	}

	return restart.New(
		process.NewRunner(
			false,
			args,
			runner.WithLoggingManager(r.Logging()),
			runner.WithEnv([]string{
				"PATH=/usr/sbin",
				"TS_LOGS_DIR=/run/tailscale-logs",
			}),
		),
		restart.WithType(restart.Forever),
	), nil
}

// HealthFunc implements the HealthcheckedService interface.
func (c *Tailscaled) HealthFunc(runtime.Runtime) health.Check {
	return func(ctx context.Context) error {
		return nil
	}
}

// HealthSettings implements the HealthcheckedService interface.
func (c *Tailscaled) HealthSettings(runtime.Runtime) *health.Settings {
	return &health.Settings{
		InitialDelay: 1 * time.Second,
		Period:       10 * time.Second,
		Timeout:      5 * time.Second,
	}
}
