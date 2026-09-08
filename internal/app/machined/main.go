// Command machined is a minimal PID 1 replacement for an immutable,
// single-purpose appliance OS. It mounts the filesystems Linux userspace
// expects, reaps zombie processes, and supervises a single child process
// (a shell for now, tailscaled later).
//
// This is milestone 1: prove PID 1 works end to end under QEMU. Config
// parsing, networking, and the tailscaled supervision are deliberately
// not here yet.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"
)

func main() {
	ctx := context.Background()
	if os.Getpid() != 1 {
		// Running as PID 1 is a hard assumption throughout: reboot/poweroff,
		// zombie reaping, and mount ownership all depend on it. Refusing to
		// run otherwise avoids confusing half-broken behavior when testing
		// the binary by hand in a dev shell.
		logf("FATAL: machined must run as PID 1, got pid %d", os.Getpid())
		os.Exit(1)
	}

	logf("machined starting")

	mounts := essentialMounts()
	if err := mountAll(mounts); err != nil {
		// We can't do much useful without these mounts. Log and keep going
		// rather than exiting - PID 1 exiting panics the kernel, and a wedged
		// system you can inspect over serial beats an instant kernel panic.
		logf("FATAL: mount setup failed: %v ; continuing in degraded mode", err)
	}

	if err := unix.Sethostname([]byte("machined-dev")); err != nil {
		logf("warning: sethostname failed: %v", err)
	}

	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh,
		unix.SIGCHLD,
		unix.SIGTERM,
		unix.SIGINT,
		unix.SIGUSR1, // reserved: reboot request, wired up later
		unix.SIGUSR2, // reserved: poweroff request, wired up later
	)

	// start udev deamon
	udevd := newSupervisor(supervisorConfig{
		path: "/sbin/udevd",
		args: []string{"--resolve-names=never"},
	})
	udevd.start()

	// wait for udev to be up and settle
	logf("waiting for udev to settle")
	if err := waitForUdev(ctx); err != nil {
		logf("warning: wait for udev failed: %v", err)
	}

	logf("setting up interfaces")
	if err := setupNetworkInterfaces(ctx); err != nil {
		logf("warning: setup network interfaces failed: %v", err)
	}

	if err := os.MkdirAll("/run/tailscale-logs", 0700); err != nil {
		logf("mkdir log dir: %w", err)
	}

	sup := newSupervisor(supervisorConfig{
		path: "/usr/sbin/tailscaled",
		args: []string{"--statedir=/run", "--state=mem:"},
		env:  []string{"PATH=/usr/sbin", "TS_LOGS_DIR=/run/tailscale-logs"},
	})

	sup.start()

	logf("entering main loop")

	for {
		sig := <-sigCh
		switch sig {

		case unix.SIGCHLD:
			exited, status := reapChildren(sup.pid())
			if exited {
				logf("supervised process exited: %s", describeWaitStatus(status))
				sup.onChildExited(status)
			}

		case unix.SIGTERM, unix.SIGINT:
			logf("received %v, shutting down", sig)
			shutdown(mounts, sup, false)
			return

		case unix.SIGUSR2:
			logf("received poweroff request")
			shutdown(mounts, sup, true)
			return

		default:
			logf("received unhandled signal: %v", sig)
		}
	}
}

// shutdown stops the supervised process, flushes and unmounts filesystems,
// then reboots or powers off the machine. As PID 1, we are the only
// process that can meaningfully do this - init systems exist partly to
// own this exact responsibility.
func shutdown(mounts []mountSpec, sup *supervisor, poweroff bool) {
	sup.stop(5 * time.Second)
	syncAndUnmountAll(mounts)

	cmd := unix.LINUX_REBOOT_CMD_RESTART
	verb := "reboot"
	if poweroff {
		cmd = unix.LINUX_REBOOT_CMD_POWER_OFF
		verb = "poweroff"
	}

	logf("issuing %s", verb)
	if err := unix.Reboot(cmd); err != nil {
		logf("FATAL: reboot syscall failed: %v", err)
		// Nothing sensible left to do - avoid a busy-loop spin.
		select {}
	}
}

// supervisorConfig describes the single child process machined runs and
// keeps alive. Only one workload is supported right now; this becomes a
// list once there's more than one service to manage.
type supervisorConfig struct {
	path string
	args []string
	env  []string
}

type supervisor struct {
	cfg        supervisorConfig
	cmd        *exec.Cmd
	restarts   int
	lastStart  time.Time
	terminated bool // set once we've asked it to stop; suppresses auto-restart
}

func newSupervisor(cfg supervisorConfig) *supervisor {
	return &supervisor{cfg: cfg}
}

func (s *supervisor) start() {
	s.cmd = exec.Command(s.cfg.path, s.cfg.args...)
	s.cmd.Stdin = os.Stdin
	s.cmd.Stdout = os.Stdout
	s.cmd.Stderr = os.Stderr
	// Put the child in its own process group so signals we send it later
	// (or that the kernel sends on Ctrl-C from a console) don't also land
	// on machined itself.
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	s.cmd.Env = s.cfg.env

	s.lastStart = time.Now()
	if err := s.cmd.Start(); err != nil {
		logf("FATAL: failed to start supervised process %s: %v", s.cfg.path, err)
		return
	}
	logf("started supervised process %s (pid %d)", s.cfg.path, s.cmd.Process.Pid)
}

func (s *supervisor) pid() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return -1
	}
	return s.cmd.Process.Pid
}

// onChildExited decides whether to restart the supervised process. A
// simple backoff avoids a crash-looping child from burning CPU forever;
// this should grow into exponential backoff with a cap once this sees
// real-world crash patterns.
func (s *supervisor) onChildExited(status unix.WaitStatus) {
	if s.terminated {
		return
	}

	uptime := time.Since(s.lastStart)
	if uptime < 2*time.Second {
		s.restarts++
	} else {
		s.restarts = 0
	}

	if s.restarts > 5 {
		logf("supervised process crash-looping (%d restarts), backing off 30s", s.restarts)
		time.Sleep(30 * time.Second)
	} else {
		time.Sleep(500 * time.Millisecond)
	}

	logf("restarting supervised process")
	s.start()
}

// stop asks the supervised process to exit gracefully, escalating to
// SIGKILL if it doesn't within timeout. Marks the supervisor as
// terminated first so a resulting SIGCHLD doesn't trigger a restart.
func (s *supervisor) stop(timeout time.Duration) {
	s.terminated = true

	if s.cmd == nil || s.cmd.Process == nil {
		return
	}

	pid := s.cmd.Process.Pid
	logf("stopping supervised process (pid %d)", pid)

	_ = unix.Kill(pid, unix.SIGTERM)

	done := make(chan struct{})
	go func() {
		_, _ = s.cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
		logf("supervised process exited cleanly")
	case <-time.After(timeout):
		logf("supervised process did not exit in time, sending SIGKILL")
		_ = unix.Kill(pid, unix.SIGKILL)
		<-done
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
			logf("could not fetch iface %v: %v (skipping)", link.Index, err)
			continue
		}
		logf("bringin up %v (%v)", iface.Name, iface.Index)

		err = conn.Link.Set(&rtnetlink.LinkMessage{
			Family: unix.AF_UNSPEC,
			Type:   link.Type,
			Index:  link.Index,
			Flags:  unix.IFF_UP,
			Change: unix.IFF_UP,
		})

		if err != nil {
			logf("could not bring %v up: %v", iface.Name, err)
			continue
		}

		if iface.Name == "lo" {
			continue
		}

		logf("fetching dhcp config for %v", iface.Name)

		client, err := nclient4.New(iface.Name)
		if err != nil {
			logf("could not create dhcpv4 client for %v: %v", iface.Name, err)
			continue
		}

		lease, err := client.Request(ctx)
		if err != nil {
			logf("could not get leae for %v: %v", iface.Name, err)
			continue
		}

		if err := applyLease(conn, iface, lease.Offer); err != nil {
			logf("could not apply lease for %v: %v", iface.Name, err)
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

	logf("applying lease: ip=%s prefixlen=%d", ip, ones)
	// Add IP address
	if err := conn.Address.New(&rtnetlink.AddressMessage{
		Family:       unix.AF_INET,
		Index:        uint32(iface.Index),
		PrefixLength: uint8(ones),
		Attributes:   &rtnetlink.AddressAttributes{Address: ip, Local: ip},
		Scope:        unix.RT_SCOPE_UNIVERSE,
	}); err != nil {
		return fmt.Errorf("address: %w", err)
	}

	// Add routes for each gateway
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

	// Add default route
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

	// Apply MTU if present
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
