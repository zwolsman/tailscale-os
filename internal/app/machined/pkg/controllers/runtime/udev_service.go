package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/services"
	"go.uber.org/zap"
)

// UdevServiceManager is the interface to the v1alpha1 service subsystem.
type UdevServiceManager interface {
	IsRunning(id string) (system.Service, bool, error)
	Load(services ...system.Service) []string
	Start(serviceIDs ...string) error
}

// UdevServiceController owns udevd service startup.
type UdevServiceController struct {
	V1Alpha1Services UdevServiceManager
	WaitForUdevd     func(ctx context.Context, serviceID string) error

	started bool
}

var _ controller.Controller = (*UdevServiceController)(nil)

// Name implements [controller.Controller].
func (u *UdevServiceController) Name() string {
	return "runtime.UdevServiceController"
}

// Inputs implements [controller.Controller].
func (u *UdevServiceController) Inputs() []controller.Input {
	return nil
}

// Outputs implements [controller.Controller].
func (u *UdevServiceController) Outputs() []controller.Output {
	return nil
}

// Run implements [controller.Controller].
func (ctrl *UdevServiceController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		if err := ctrl.ensureStarted(ctx, r, logger); err != nil {
			return err
		}

		r.ResetRestartBackoff()
	}
}

func (ctrl *UdevServiceController) ensureStarted(ctx context.Context, _ controller.Reader, logger *zap.Logger) error {
	if ctrl.started {
		return nil
	}

	service := &services.Udevd{}

	serviceID := service.ID(nil)

	ctrl.V1Alpha1Services.Load(service)

	_, running, err := ctrl.V1Alpha1Services.IsRunning(serviceID)
	if err != nil {
		return fmt.Errorf("failed to check udevd service state: %w", err)
	}

	if !running {
		if err = ctrl.V1Alpha1Services.Start(serviceID); err != nil {
			return fmt.Errorf("failed to start udevd service: %w", err)
		}
	}

	logger.Debug("udev service started")

	waitForUdevd := ctrl.WaitForUdevd
	if waitForUdevd == nil {
		waitForUdevd = func(ctx context.Context, serviceID string) error {
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()

			return system.WaitForService(system.StateEventUp, serviceID).Wait(waitCtx)
		}
	}

	if err = waitForUdevd(ctx, serviceID); err != nil {
		return fmt.Errorf("failed waiting for udevd service: %w", err)
	}

	ctrl.started = true

	return nil
}
