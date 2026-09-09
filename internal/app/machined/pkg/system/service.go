package system

import (
	"context"

	"github.com/siderolabs/talos/pkg/conditions"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/health"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner"
)

// Service is an interface describing a system service.
type Service interface {
	// ID is the service id.
	ID(runtime.Runtime) string
	// PreFunc is invoked before a runner is created
	PreFunc(context.Context, runtime.Runtime) error
	// Runner creates runner for the service
	Runner(runtime.Runtime) (runner.Runner, error)
	// PostFunc is invoked after a runner is closed.
	PostFunc(runtime.Runtime, interface{}) error
	// Condition describes the conditions under which a service should
	// start.
	Condition(runtime.Runtime) conditions.Condition
	// DependsOn returns list of service IDs this service depends on.
	DependsOn(runtime.Runtime) []string
	// Volumes returns a list of volume IDs the service needs.
	Volumes(runtime.Runtime) []string
}

// PreShutdownService is a service which performs work before node shutdown.
type PreShutdownService interface {
	PreShutdownFunc(context.Context, runtime.Runtime) error
}

// HealthcheckedService is a service which provides health check.
type HealthcheckedService interface {
	// HealthFunc provides function that checks health of the service
	HealthFunc(runtime.Runtime) health.Check
	// HealthSettings returns settings for the health check
	HealthSettings(runtime.Runtime) *health.Settings
}

// APIStartableService is a service which allows to be started via API.
type APIStartableService interface {
	APIStartAllowed(runtime.Runtime) bool
}

// APIStoppableService is a service which allows to be stopped via API.
type APIStoppableService interface {
	APIStopAllowed(runtime.Runtime) bool
}

// APIRestartableService is a service which allows to be restarted via API.
type APIRestartableService interface {
	APIRestartAllowed(runtime.Runtime) bool
}
