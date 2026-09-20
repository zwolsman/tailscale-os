package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/services"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/runtime"
	"go.uber.org/zap"
)

// ApidServiceManager is the interface to the v1alpha1 service subsystem.
type ApidServiceManager interface {
	IsRunning(id string) (system.Service, bool, error)
	Load(services ...system.Service) []string
	Start(serviceIDs ...string) error
}

// ApidServiceController owns apid service startup.
type ApidServiceController struct {
	V1Alpha1Services ApidServiceManager
	WaitForService   func(ctx context.Context, serviceID string) error

	started bool
}

var _ controller.Controller = (*ApidServiceController)(nil)

// Name implements [controller.Controller].
func (a *ApidServiceController) Name() string {
	return "runtime.ApidServiceController"
}

// Inputs implements [controller.Controller].
func (a *ApidServiceController) Inputs() []controller.Input {
	return []controller.Input{
		{
			Namespace: runtime.NamespaceName,
			Type:      runtime.DevicesStatusType,
			Kind:      controller.InputStrong,
		},
	}
}

// Outputs implements [controller.Controller].
func (a *ApidServiceController) Outputs() []controller.Output {
	return nil
}

// Run implements [controller.Controller].
func (ctrl *ApidServiceController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		status, err := safe.ReaderGetByID[*runtime.DevicesStatus](ctx, r, runtime.DevicesID)
		if err != nil {
			if state.IsNotFoundError(err) {
				continue
			}

			return err
		}

		if !status.TypedSpec().Ready {
			continue
		}

		if err := ctrl.ensureStarted(ctx, r, logger); err != nil {
			return err
		}

		r.ResetRestartBackoff()
	}
}

func (ctrl *ApidServiceController) ensureStarted(ctx context.Context, _ controller.Reader, logger *zap.Logger) error {
	if ctrl.started {
		return nil
	}

	service := &services.APID{}

	serviceID := service.ID(nil)

	ctrl.V1Alpha1Services.Load(service)

	_, running, err := ctrl.V1Alpha1Services.IsRunning(serviceID)
	if err != nil {
		return fmt.Errorf("failed to check aipd service state: %w", err)
	}

	if !running {
		if err = ctrl.V1Alpha1Services.Start(serviceID); err != nil {
			return fmt.Errorf("failed to start aipd service: %w", err)
		}
	}

	logger.Debug("apid service started")

	waitForService := ctrl.WaitForService
	if waitForService == nil {
		waitForService = func(ctx context.Context, serviceID string) error {
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()

			return system.WaitForService(system.StateEventUp, serviceID).Wait(waitCtx)
		}
	}

	if err = waitForService(ctx, serviceID); err != nil {
		return fmt.Errorf("failed waiting for apid service: %w", err)
	}

	ctrl.started = true

	return nil
}
