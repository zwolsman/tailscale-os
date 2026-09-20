// Command machined is a minimal PID 1 replacement for an immutable,
// single-purpose appliance OS. It mounts the filesystems Linux userspace
// expects, reaps zombie processes, and supervises services using the
// talos-inspired service system.
//
// This is milestone 1: prove PID 1 works end to end under QEMU with
// the talos-style service system for udevd.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime/logging"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime/v1alpha1"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"golang.org/x/sys/unix"
)

func main() {
	ctx := context.Background()
	if os.Getpid() != 1 {
		log.Fatalf("machined must run as PID 1, got pid %d", os.Getpid())
	}

	log.SetPrefix("[machined] ")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Println("machined starting")

	mounts := essentialMounts()
	if err := mountAll(mounts); err != nil {
		log.Fatalf("mount setup failed: %v ; continuing in degraded mode", err)
	}

	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh,
		unix.SIGCHLD,
		unix.SIGTERM,
		unix.SIGINT,
		unix.SIGUSR1,
		unix.SIGUSR2,
	)

	l := &logging.NullLoggingManager{}
	e := v1alpha1.NewEvents(1000, 10)
	rt := NewRuntime(l, e)

	ctrl, err := NewController(rt, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		log.Fatalf("could not create root controller: %v", err)
	}

	go ctrl.Run(ctx, nil)

	log.Println("entering main loop")

	for {
		sig := <-sigCh
		switch sig {

		case unix.SIGCHLD:
			// handled by the service system's runner

		case unix.SIGTERM, unix.SIGINT:
			log.Printf("received %v, shutting down", sig)
			shutdown(mounts, false)
			return

		case unix.SIGUSR2:
			log.Println("received poweroff request")
			shutdown(mounts, true)
			return

		default:
			log.Printf("received unhandled signal: %v", sig)
		}
	}
}

// shutdown stops all services, flushes and unmounts filesystems,
// then reboots or powers off the machine. As PID 1, we are the only
// process that can meaningfully do this.
func shutdown(mounts []mountSpec, poweroff bool) {
	svc := system.Services(nil)
	svc.Shutdown(context.Background())
	syncAndUnmountAll(mounts)

	cmd := unix.LINUX_REBOOT_CMD_RESTART
	verb := "reboot"
	if poweroff {
		cmd = unix.LINUX_REBOOT_CMD_POWER_OFF
		verb = "poweroff"
	}

	log.Printf("issuing %s", verb)
	if err := unix.Reboot(cmd); err != nil {
		log.Fatalf("reboot syscall failed: %v", err)
		select {}
	}
}

var _ runtime.Runtime = (*Runtime)(nil)

func NewRuntime(l runtime.LoggingManager, e runtime.EventStream) runtime.Runtime {
	return &Runtime{
		l: l,
		s: NewState(),
		e: e,
	}
}

func NewState() state.State {
	// TODO: register all resources for api to use
	// reference:  siderolabs/talos/internal/app/machined/pkg/runtime/v1alpha2/v1alpha2_state.go
	return state.WrapCore(namespaced.NewState(inmem.Build))
}

// Runtime implements the Runtime interface.
type Runtime struct {
	l runtime.LoggingManager
	s state.State
	e runtime.EventStream
}

// Logging implements the Runtime interface.
func (r *Runtime) Logging() runtime.LoggingManager {
	return r.l
}

func (r *Runtime) State() state.State {
	return r.s
}

// Events returns a simple event stream for the runtime.
func (r *Runtime) Events() runtime.EventStream {
	return r.e
}

// ResetRestartBackoff is a no-op for the simple runtime.
func (r *Runtime) ResetRestartBackoff() {}
