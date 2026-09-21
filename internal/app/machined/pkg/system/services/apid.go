package services

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/cosi-project/runtime/pkg/state/protobuf/server"
	"github.com/siderolabs/talos/pkg/conditions"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/health"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner/goroutine"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner/restart"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

var _ system.HealthcheckedService = (*APID)(nil)

const protocol, endpoint = "tcp", ":6666" // TODO: bind to localhost, workaround for now with QEMU

type APID struct{}

// ID implements [system.HealthcheckedService].
func (a *APID) ID(runtime.Runtime) string {
	return "apid"
}

// Volumes implements [system.HealthcheckedService].
func (a *APID) Volumes(runtime.Runtime) []string {
	return nil
}

func (a *APID) PreFunc(ctx context.Context, r runtime.Runtime) error {
	return nil
}

func (a *APID) PostFunc(runtime.Runtime, interface{}) error {
	return nil
}

// Condition implements [system.HealthcheckedService].
func (a *APID) Condition(runtime.Runtime) conditions.Condition {
	return nil
}

func (a *APID) Runner(r runtime.Runtime) (runner.Runner, error) {
	return restart.New(
		goroutine.NewRunner(r,
			a.ID(r),
			a.runAPID,
			runner.WithLoggingManager(r.Logging()),
		),
		restart.WithType(restart.Forever),
	), nil
}

func (a *APID) DependsOn(runtime.Runtime) []string {
	return nil
}

// HealthFunc implements [system.HealthcheckedService].
func (a *APID) HealthFunc(r runtime.Runtime) health.Check {
	return func(ctx context.Context) error {
		var d net.Dialer

		conn, err := d.DialContext(ctx, protocol, endpoint)
		if err != nil {
			return err
		}

		return conn.Close()
	}
}

// HealthSettings implements [system.HealthcheckedService].
func (a *APID) HealthSettings(runtime.Runtime) *health.Settings {
	return &health.DefaultSettings
}

func (a *APID) runAPID(ctx context.Context, r runtime.Runtime, logOutput io.Writer) error {
	listener, err := net.Listen(protocol, endpoint)
	if err != nil {
		return err
	}

	s := grpc.NewServer()
	v1alpha1.RegisterStateServer(s, server.NewState(r.State()))

	errGroup, ctx := errgroup.WithContext(ctx)

	errGroup.Go(func() error {
		fmt.Println("serving apid grpc server")
		return s.Serve(listener)
	})

	errGroup.Go(func() error {
		<-ctx.Done()
		fmt.Println("graceful stop grpc apid")
		s.GracefulStop()

		return listener.Close()
	})

	return errGroup.Wait()
}
