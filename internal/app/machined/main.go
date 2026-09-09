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
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/jsimonetti/rtnetlink"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime/logging"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/services"
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

	if err := unix.Sethostname([]byte("machined-dev")); err != nil {
		log.Printf("warning: sethostname failed: %v", err)
	}

	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh,
		unix.SIGCHLD,
		unix.SIGTERM,
		unix.SIGINT,
		unix.SIGUSR1,
		unix.SIGUSR2,
	)

	l := logging.NewSimpleLoggerManager(log.New(os.Stdout, "[machined] ", log.LstdFlags|log.Lmicroseconds))
	rt := NewRuntime(l)

	// Initialize the system services using the talos-inspired runtime/service pattern
	svc := system.Services(rt)

	// Load and start udevd using the service system
	udevd := &services.Udevd{}
	svc.Load(udevd)

	if err := svc.Start(udevd.ID(rt)); err != nil {
		log.Printf("warning: failed to start udevd service: %v", err)
	}

	// Wait for udev to settle
	log.Println("waiting for udev to settle")
	if err := waitForUdev(ctx, udevd.ID(rt)); err != nil {
		log.Printf("warning: wait for udev failed: %v", err)
	}

	log.Println("setting up interfaces")
	if err := setupNetworkInterfaces(ctx); err != nil {
		log.Printf("warning: setup network interfaces failed: %v", err)
	}

	if err := os.MkdirAll("/run/tailscale-logs", 0700); err != nil {
		log.Printf("mkdir log dir: %w", err)
	}

	// Start tailscaled using the service system
	tailscaled := &services.Tailscaled{}
	svc.Load(tailscaled)
	if err := svc.Start(tailscaled.ID(nil)); err != nil {
		log.Printf("warning: failed to start tailscaled service: %v", err)
	}

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

func setupNetworkInterfaces(ctx context.Context) error {
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	links, err := conn.Link.List()
	if err != nil {
		return err
	}

	for _, link := range links {
		iface, err := net.InterfaceByIndex(int(link.Index))
		if err != nil {
			log.Printf("could not fetch iface %v: %v (skipping)", link.Index, err)
			continue
		}
		log.Printf("bringin up %v (%v)", iface.Name, iface.Index)

		err = conn.Link.Set(&rtnetlink.LinkMessage{
			Family: unix.AF_UNSPEC,
			Type:   link.Type,
			Index:  link.Index,
			Flags:  unix.IFF_UP,
			Change: unix.IFF_UP,
		})

		if err != nil {
			log.Printf("could not bring %v up: %v", iface.Name, err)
			continue
		}

		if iface.Name == "lo" {
			continue
		}

		log.Printf("fetching dhcp config for %v", iface.Name)

		client, err := nclient4.New(iface.Name)
		if err != nil {
			log.Printf("could not create dhcpv4 client for %v: %v", iface.Name, err)
			continue
		}

		lease, err := client.Request(ctx)
		if err != nil {
			log.Printf("could not get leae for %v: %v", iface.Name, err)
			continue
		}

		if err := applyLease(conn, iface, lease.Offer); err != nil {
			log.Printf("could not apply lease for %v: %v", iface.Name, err)
			continue
		}
	}

	return nil
}

func applyLease(conn *rtnetlink.Conn, iface *net.Interface, lease *dhcpv4.DHCPv4) error {
	ip := lease.YourIPAddr.To4()
	if ip == nil {
		return fmt.Errorf("invalid IPv4 lease address")
	}
	mask := lease.SubnetMask()
	ones, bits := mask.Size()

	if ones == 0 && bits == 0 || ones > 32 {
		return fmt.Errorf("invalid or missing subnet mask in lease: %v", mask)
	}

	log.Printf("applying lease: ip=%s prefixlen=%d", ip, ones)
	if err := conn.Address.New(&rtnetlink.AddressMessage{
		Family:       unix.AF_INET,
		Index:        uint32(iface.Index),
		PrefixLength: uint8(ones),
		Attributes:   &rtnetlink.AddressAttributes{Address: ip, Local: ip},
		Scope:        unix.RT_SCOPE_UNIVERSE,
	}); err != nil {
		return fmt.Errorf("address: %w", err)
	}

	for _, gw := range lease.Router() {
		if err := conn.Route.Add(&rtnetlink.RouteMessage{
			Family: unix.AF_INET,
			Attributes: rtnetlink.RouteAttributes{
				Gateway:  gw,
				OutIface: uint32(iface.Index),
				Table:    unix.RT_TABLE_MAIN,
			},
			Type:     unix.RTN_UNICAST,
			Protocol: unix.RTPROT_BOOT,
		}); err != nil {
			return fmt.Errorf("route: %w", err)
		}
	}

	if len(lease.Router()) > 0 {
		if err := conn.Route.Replace(&rtnetlink.RouteMessage{
			Family: unix.AF_INET,
			Attributes: rtnetlink.RouteAttributes{
				Dst:      net.IPv4zero,
				Gateway:  lease.Router()[0],
				OutIface: uint32(iface.Index),
				Table:    unix.RT_TABLE_MAIN,
			},
			Type:     unix.RTN_UNICAST,
			Protocol: unix.RTPROT_BOOT,
		}); err != nil {
			return fmt.Errorf("default route: %w", err)
		}
	}

	if mtu, _ := dhcpv4.GetUint16(dhcpv4.OptionInterfaceMTU, lease.Options); mtu > 0 {
		if err := conn.Link.Set(&rtnetlink.LinkMessage{
			Index:      uint32(iface.Index),
			Attributes: &rtnetlink.LinkAttributes{MTU: uint32(mtu)},
		}); err != nil {
			return fmt.Errorf("mtu: %w", err)
		}
	}

	return nil
}

var _ runtime.Runtime = (*Runtime)(nil)

func NewRuntime(l runtime.LoggingManager) runtime.Runtime {
	return &Runtime{
		l: l,
	}
}

// Runtime implements the Runtime interface.
type Runtime struct {
	l runtime.LoggingManager
}

// Logging implements the Runtime interface.
func (r *Runtime) Logging() runtime.LoggingManager {
	return r.l
}

// Events returns a simple event stream for the runtime.
func (r *Runtime) Events() runtime.EventStream {
	return &simpleEventStream{}
}

// ResetRestartBackoff is a no-op for the simple runtime.
func (r *Runtime) ResetRestartBackoff() {}

// simpleEventStream is a minimal EventStream implementation.
type simpleEventStream struct {
	ch chan struct{}
}

func (s *simpleEventStream) Publish(ctx context.Context, msg any) {
	// no-op for simple runtime
}

func (s *simpleEventStream) EventCh() <-chan struct{} {
	return s.ch
}

// WaitForUdevd waits for the controller-owned udevd service to become healthy.
func waitForUdev(ctx context.Context, serviceID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	return system.WaitForService(system.StateEventUp, serviceID).Wait(waitCtx)
}
