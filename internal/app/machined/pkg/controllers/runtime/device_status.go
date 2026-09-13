package runtime

import (
	"context"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/gen/optional"

	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/runtime"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/v1alpha1"

	"go.uber.org/zap"
)

type DeviceStatusController struct{}

var _ controller.Controller = (*DeviceStatusController)(nil)

// Name implements [controller.Controller].
func (d *DeviceStatusController) Name() string {
	return "runtime.DeviceStatusController"
}

// Inputs implements [controller.Controller].
func (d *DeviceStatusController) Inputs() []controller.Input {
	return []controller.Input{
		{
			Namespace: v1alpha1.NamespaceName,
			Type:      v1alpha1.ServiceType,
			ID:        optional.Some("udevd"),
			Kind:      controller.InputWeak,
		},
	}
}

// Outputs implements [controller.Controller].
func (d *DeviceStatusController) Outputs() []controller.Output {
	return []controller.Output{
		{
			Type: runtime.DevicesStatusType,
			Kind: controller.OutputExclusive,
		},
	}
}

// Run implements [controller.Controller].
func (d *DeviceStatusController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		service, err := safe.ReaderGetByID[*v1alpha1.Service](ctx, r, "udevd")
		if err != nil {
			if state.IsNotFoundError(err) {
				continue
			}

			return err
		}

		if !(service.TypedSpec().Running && service.TypedSpec().Healthy) {
			// condition not met
			continue
		}

		if err := safe.WriterModify(ctx, r, runtime.NewDevicesStatus(runtime.NamespaceName, runtime.DevicesID), func(status *runtime.DevicesStatus) error {
			status.TypedSpec().Ready = true
			return nil
		}); err != nil {
			return err
		}

		// everything is done, ready, stop the controller
		logger.Debug("Devices ready")
		return nil
	}
}
