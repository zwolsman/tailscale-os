package services

import (
	"context"
	"fmt"
	"time"

	"github.com/siderolabs/go-cmd/pkg/cmd"

	"github.com/siderolabs/talos/pkg/conditions"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/health"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner/process"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner/restart"
)

var _ system.HealthcheckedService = (*Udevd)(nil)

// Udevd implements the Service interface. It serves as the concrete type with
// the required methods.
type Udevd struct {
	ExtraSettleTime time.Duration

	triggered        bool
	extraSettleStart time.Time
}

// ID implements the Service interface.
func (c *Udevd) ID(runtime.Runtime) string {
	return "udevd"
}

// PreFunc implements the Service interface.
func (c *Udevd) PreFunc(ctx context.Context, r runtime.Runtime) error {
	_, err := cmd.RunWithOptions(ctx, "/sbin/udevadm", []string{"hwdb", "--update", "--root=/run"})

	return err
}

// PostFunc implements the Service interface.
func (c *Udevd) PostFunc(runtime.Runtime, interface{}) error {
	return nil
}

// Condition implements the Service interface.
func (c *Udevd) Condition(runtime.Runtime) conditions.Condition {
	return nil
}

// DependsOn implements the Service interface.
func (c *Udevd) DependsOn(runtime.Runtime) []string {
	return nil
}

// Volumes implements the Service interface.
func (c *Udevd) Volumes(runtime.Runtime) []string {
	return nil
}

// Runner implements the Service interface.
func (c *Udevd) Runner(r runtime.Runtime) (runner.Runner, error) {
	// Set the process arguments.
	args := &runner.Args{
		ID: c.ID(r),
		ProcessArgs: []string{
			"/sbin/udevd",
			"--resolve-names=never",
		},
	}

	debug := false

	return restart.New(
		process.NewRunner(
			debug,
			args,
			runner.WithLoggingManager(r.Logging()),
			runner.WithCgroupPath(constants.CgroupUdevd),
			runner.WithSelinuxLabel(constants.SelinuxLabelUdevd),
			runner.WithDroppedCapabilities(constants.UdevdDroppedCapabilities),
			runner.WithEnv([]string{
				constants.EnvXDGRuntimeDir,
			}),
		),
		restart.WithType(restart.Forever),
	), nil
}

// HealthFunc implements the HealthcheckedService interface.
//
//nolint:gocyclo
func (c *Udevd) HealthFunc(runtime.Runtime) health.Check {
	return func(ctx context.Context) error {
		if err := conditions.WaitForFileToExist("/run/udev/control").Wait(ctx); err != nil {
			return err
		}

		if _, err := cmd.RunWithOptions(ctx, "/sbin/udevadm", []string{"control", "--reload"}); err != nil {
			return err
		}

		if !c.triggered {
			if _, err := cmd.RunWithOptions(ctx, "/sbin/udevadm", []string{"trigger", "--type=devices", "--action=add"}); err != nil {
				return err
			}

			if _, err := cmd.RunWithOptions(ctx, "/sbin/udevadm", []string{"trigger", "--type=subsystems", "--action=add"}); err != nil {
				return err
			}

			c.triggered = true
		}

		_, err := cmd.RunWithOptions(ctx, "/sbin/udevadm", []string{"settle", "--timeout=50"})
		if err != nil {
			return err
		}

		if c.extraSettleStart.IsZero() {
			c.extraSettleStart = time.Now()
		}

		if c.ExtraSettleTime <= 0 {
			return nil
		}

		settleEnd := c.extraSettleStart.Add(c.ExtraSettleTime)

		if time.Now().After(settleEnd) {
			return nil
		}

		if deadline, ok := ctx.Deadline(); ok {
			if settleEnd.Before(deadline) {
				time.Sleep(time.Until(settleEnd))
				return nil
			}
		}

		return fmt.Errorf("waiting for udevd for extra settle timeout")
	}
}

// HealthSettings implements the HealthcheckedService interface.
func (c *Udevd) HealthSettings(runtime.Runtime) *health.Settings {
	return &health.Settings{
		InitialDelay: 100 * time.Millisecond,
		Period:       time.Minute,
		Timeout:      55 * time.Second,
	}
}
