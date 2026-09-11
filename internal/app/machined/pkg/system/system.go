package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/siderolabs/talos/pkg/conditions"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/events"
)

// singleton the system services API interface.
type singleton struct {
	runtime runtime.Runtime

	// State of running services by ID
	state map[string]*ServiceRunner

	// List of running services at the moment.
	//
	// Service might be in any state, but service ID in the map
	// implies ServiceRunner.Start() method is running at the momemnt
	runningMu sync.Mutex
	running   map[string]struct{}

	mu sync.Mutex
	wg sync.WaitGroup

	// denyNewServices is set on DenyNewServices, and it rejects any further Load/Start calls, used on Talos shutdown/reboot paths.
	denyNewServices bool
	// terminating is set on Shutdown, and it rejects any further Load/Start/Stop calls, and allows Shutdown to proceed without deadlock.
	terminating bool
}

var (
	instance *singleton
	once     sync.Once
)

func newServices(runtime runtime.Runtime) *singleton {
	return &singleton{
		runtime: runtime,
		state:   map[string]*ServiceRunner{},
		running: map[string]struct{}{},
	}
}

// Services returns the instance of the system services API.
//
//nolint:revive
func Services(runtime runtime.Runtime) *singleton {
	once.Do(func() {
		instance = newServices(runtime)
	})

	return instance
}

// Load adds service to the list of services managed by the runner.
//
// Load returns service IDs for each of the services.
func (s *singleton) Load(services ...Service) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.terminating || s.denyNewServices {
		return nil
	}

	ids := make([]string, 0, len(services))

	for _, service := range services {
		id := service.ID(s.runtime)
		ids = append(ids, id)

		if _, exists := s.state[id]; exists {
			// service already loaded, ignore
			continue
		}

		svcrunner := NewServiceRunner(s, service, s.runtime)
		s.state[id] = svcrunner
	}

	return ids
}

// Start will invoke the service's Pre, Condition, and Type funcs. If any
// error occurs in the Pre or Condition invocations, it is up to the caller to
// restart the service.
func (s *singleton) Start(serviceIDs ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.terminating || s.denyNewServices {
		return nil
	}

	var multiErr error

	for _, id := range serviceIDs {
		svcrunner := s.state[id]
		if svcrunner == nil {
			multiErr = fmt.Errorf("service %q not defined: %w", id, multiErr)
			continue
		}

		s.runningMu.Lock()
		_, running := s.running[id]
		if !running {
			s.running[id] = struct{}{}
		}
		s.runningMu.Unlock()

		if running {
			// service already running, skip
			continue
		}

		runNotify := make(chan struct{})

		s.wg.Add(1)

		go func(id string, svcrunner *ServiceRunner) {
			err := func() error {
				defer func() {
					s.runningMu.Lock()
					delete(s.running, id)
					s.runningMu.Unlock()
				}()
				defer s.wg.Done()

				return svcrunner.Run(runNotify)
			}()

			switch {
			case err == nil:
				svcrunner.UpdateState(nil, events.StateFinished, "Service finished successfully")
			case errors.Is(err, ErrSkip):
				svcrunner.UpdateState(nil, events.StateSkipped, "Service skipped")
			default:
				msg := err.Error()
				if len(msg) > 0 {
					msg = strings.ToUpper(msg[:1]) + msg[1:]
				}
				svcrunner.UpdateState(nil, events.StateFailed, "%s", msg)
			}
		}(id, svcrunner)

		// wait for svcrunner.Run to enter the running phase, and then return
		<-runNotify
	}

	return multiErr
}

// Shutdown all the services.
func (s *singleton) Shutdown(ctx context.Context) {
	s.mu.Lock()

	if s.terminating {
		s.mu.Unlock()
		return
	}

	s.terminating = true

	_ = s.stopServices(ctx, nil, true) //nolint:errcheck
}

// Stop will initiate a shutdown of the specified service.
func (s *singleton) Stop(ctx context.Context, serviceIDs ...string) (err error) {
	if len(serviceIDs) == 0 {
		return nil
	}

	s.mu.Lock()

	if s.terminating {
		s.mu.Unlock()
		return nil
	}

	return s.stopServices(ctx, serviceIDs, false)
}

func (s *singleton) stopServices(ctx context.Context, services []string, waitForRevDependencies bool) error {
	servicesToStop := map[string]*ServiceRunner{}

	if services == nil {
		for name := range s.state {
			servicesToStop[name] = s.state[name]
		}
	} else {
		for _, name := range services {
			if _, ok := s.state[name]; !ok {
				continue
			}
			servicesToStop[name] = s.state[name]
		}
	}

	s.mu.Unlock()

	var shutdownWg sync.WaitGroup

	stoppedConds := make([]conditions.Condition, 0, len(servicesToStop))

	for name, svcrunner := range servicesToStop {
		shutdownWg.Add(1)

		stoppedConds = append(stoppedConds, waitForService(s, []StateEvent{StateEventDown}, name))

		go func(svcrunner *ServiceRunner) {
			defer shutdownWg.Done()
			svcrunner.Shutdown()
		}(svcrunner)
	}

	shutdownWg.Wait()

	return conditions.WaitForAll(stoppedConds...).Wait(ctx)
}

// List returns snapshot of ServiceRunner instances.
func (s *singleton) List() (result []*ServiceRunner) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, svcrunner := range s.state {
		result = append(result, svcrunner)
	}

	return result
}

// IsRunning checks service status (started/stopped).
func (s *singleton) IsRunning(id string) (Service, bool, error) {
	s.mu.Lock()
	runner, exists := s.state[id]
	s.mu.Unlock()

	if !exists {
		return nil, false, fmt.Errorf("service %q not defined", id)
	}

	s.runningMu.Lock()
	_, running := s.running[id]
	s.runningMu.Unlock()

	return runner.service, running, nil
}

// DenyNewServices sets the denyNewServices flag, which prevents any new services from being loaded or started.
func (s *singleton) DenyNewServices() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.denyNewServices = true
}

// ErrSkip is returned by Run when service is skipped.
var ErrSkip = errors.New("service skipped")
