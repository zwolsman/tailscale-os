package main

import (
	"context"

	"github.com/cosi-project/runtime/pkg/controller"
	osruntime "github.com/cosi-project/runtime/pkg/controller/runtime"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/logging"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/v1alpha1"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/controllers/network"
	runtimecontrollers "github.com/zwolsman/tailscale-os/internal/app/machined/pkg/controllers/runtime"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Controller struct {
	controllerRuntime *osruntime.Runtime

	loggingManager  runtime.LoggingManager
	consoleLogLevel zap.AtomicLevel
	logger          *zap.Logger

	v1alpha1Runtime runtime.Runtime
	reboot          func(ctx context.Context) error
}

// NewController creates Controller.
func NewController(v1alpha1Runtime runtime.Runtime, reboot func(ctx context.Context) error) (*Controller, error) {
	ctrl := &Controller{
		consoleLogLevel: zap.NewAtomicLevelAt(zap.DebugLevel),
		loggingManager:  v1alpha1Runtime.Logging(),
		v1alpha1Runtime: v1alpha1Runtime,
		reboot:          reboot,
	}

	var err error

	ctrl.logger, err = ctrl.MakeLogger("controller-runtime")
	if err != nil {
		return nil, err
	}

	ctrl.controllerRuntime, err = osruntime.NewRuntime(v1alpha1Runtime.State(), ctrl.logger)

	return ctrl, err
}

// Run the controller runtime.
func (ctrl *Controller) Run(ctx context.Context, drainer *runtime.Drainer) error {
	for _, c := range []controller.Controller{
		network.NewDHCP4Controller(),
		&network.LinkStatusController{},
		&runtimecontrollers.DeviceStatusController{},
		&runtimecontrollers.UdevServiceController{V1Alpha1Services: system.Services(ctrl.v1alpha1Runtime)},
		&v1alpha1.ServiceController{V1Alpha1Events: ctrl.v1alpha1Runtime.Events()},
	} {
		if err := ctrl.controllerRuntime.RegisterController(c); err != nil {
			return err
		}
	}

	return ctrl.controllerRuntime.Run(ctx)
}

// MakeLogger creates a logger for a service.
func (ctrl *Controller) MakeLogger(serviceName string) (*zap.Logger, error) {
	logWriter, err := ctrl.loggingManager.ServiceLog(serviceName).Writer()
	if err != nil {
		return nil, err
	}

	return logging.ZapLogger(
		logging.NewLogDestination(
			logWriter, zapcore.DebugLevel,
		),
		logging.NewLogDestination(
			logging.StdWriter, ctrl.consoleLogLevel,
			logging.WithoutTimestamp(),
			logging.WithoutLogLevels(),
		),
	).With(logging.Component(serviceName)), nil
}
