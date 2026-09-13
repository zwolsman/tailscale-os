package v1alpha1

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/pkg/machinery/api/machine"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/v1alpha1"
	"go.uber.org/zap"
)

// ServiceController manages v1alpha1.Service based on services subsystem state.
type ServiceController struct {
	V1Alpha1Events runtime.Watcher
}

// Name implements controller.Controller interface.
func (ctrl *ServiceController) Name() string {
	return "v1alpha1.ServiceController"
}

// Inputs implements controller.Controller interface.
func (ctrl *ServiceController) Inputs() []controller.Input {
	return nil
}

// Outputs implements controller.Controller interface.
func (ctrl *ServiceController) Outputs() []controller.Output {
	return []controller.Output{
		{
			Type: v1alpha1.ServiceType,
			Kind: controller.OutputExclusive,
		},
	}
}

// Run implements controller.Controller interface.
func (ctrl *ServiceController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	var wg sync.WaitGroup

	wg.Add(1)

	if err := ctrl.V1Alpha1Events.Watch(func(eventCh <-chan runtime.EventInfo) {
		defer wg.Done()

		for {
			var (
				event runtime.EventInfo
				ok    bool
			)

			select {
			case <-ctx.Done():
				return
			case event, ok = <-eventCh:
				if !ok {
					return
				}
			}
			logger.Debug("Received event", zap.Any("event", event))
			var msg machine.ServiceStateEvent
			if err := json.Unmarshal(event.Payload, &msg); err == nil {
				service := v1alpha1.NewService(msg.Service)

				switch msg.Action { //nolint:exhaustive
				case machine.ServiceStateEvent_RUNNING:
					if err := safe.WriterModify(ctx, r, service, func(svc *v1alpha1.Service) error {
						*svc.TypedSpec() = v1alpha1.ServiceSpec{
							Running: true,
							Healthy: msg.Health.Healthy,
							Unknown: msg.Health.Unknown,
						}

						return nil
					}); err != nil {
						logger.Info("failed creating service resource", zap.String("id", service.Metadata().ID()), zap.Error(err))
					}
				default:
					if err := r.Destroy(ctx, service.Metadata()); err != nil && !state.IsNotFoundError(err) {
						logger.Info("failed destroying service resource", zap.String("id", service.Metadata().ID()), zap.Error(err))
					}
				}
			} else {
				logger.Debug("could not deserialize event", zap.Error(err))
			}
		}
	}, runtime.WithTailEvents(-1)); err != nil {
		return err
	}

	wg.Wait()

	return nil
}
