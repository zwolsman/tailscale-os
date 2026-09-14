package network

import (
	"context"
	"net"
	"time"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/network"
	"go.uber.org/zap"
	"go4.org/netipx"
)

var _ controller.Controller = (*DHCP4Controller)(nil)

type DHCP4Controller struct {
	state map[resource.ID]*dhcpClient
}

type dhcpClient struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDHCP4Controller() *DHCP4Controller {
	return &DHCP4Controller{
		state: make(map[resource.ID]*dhcpClient),
	}
}

// Name implements [controller.Controller].
func (d *DHCP4Controller) Name() string {
	return "network.DHCP4Controller"
}

// Inputs implements [controller.Controller].
func (d *DHCP4Controller) Inputs() []controller.Input {
	return []controller.Input{
		{
			Namespace: network.NamespaceName,
			Type:      network.LinkStatusType,
			Kind:      controller.InputWeak,
		},
	}
}

// Outputs implements [controller.Controller].
func (d *DHCP4Controller) Outputs() []controller.Output {
	return []controller.Output{
		{
			Type: network.AddressSpecType,
			Kind: controller.OutputShared,
		},
	}
}

// Run implements [controller.Controller].
func (ctrl *DHCP4Controller) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		if err := ctrl.reconcile(ctx, r, logger); err != nil {
			return err
		}

		r.ResetRestartBackoff()
	}
}

func (ctrl *DHCP4Controller) reconcile(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	list, err := safe.ReaderList[*network.LinkStatus](ctx, r, resource.NewMetadata(network.NamespaceName, network.LinkStatusType, "", resource.VersionUndefined))
	if err != nil {
		return err
	}

	desired := map[resource.ID]struct{}{}

	if err := list.ForEachErr(func(ls *network.LinkStatus) error {
		id := ls.Metadata().ID()
		desired[id] = struct{}{}

		if _, ok := ctrl.state[id]; ok {
			return nil // already running
		}

		if !wantsDHCP(ls) {
			return nil // doesn't want DHCP
		}

		linkCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		ctrl.state[id] = &dhcpClient{cancel: cancel, done: done}

		go func() {
			defer close(done)
			ctrl.runDHCP(linkCtx, r, logger, id)
		}()

		return nil
	}); err != nil {
		return err
	}

	for id, c := range ctrl.state {
		if _, ok := desired[id]; ok {
			continue
		}

		logger.Debug("stopping dhcp client", zap.String("link", id))
		c.cancel()
		<-c.done
		delete(ctrl.state, id)
	}

	return nil
}

func (ctrl *DHCP4Controller) runDHCP(ctx context.Context, r controller.Runtime, logger *zap.Logger, linkName resource.ID) error {
	logger = logger.With(zap.String("link", linkName))
	client, err := nclient4.New(linkName)
	defer client.Close()

	if err != nil {
		return err
	}

	logger.Debug("Starting DHCP loop")
	var ack *dhcpv4.DHCPv4
	for {
		if ack == nil {
			lease, err := client.Request(ctx)
			if err != nil {
				return err // ctx was cancelled
			}
			ack = lease.ACK
			ctrl.applyLease(ctx, r, logger, linkName, ack)
			t := computeTimers(ack)

			select {
			case <-ctx.Done():
				ctrl.applyLease(ctx, r, logger, linkName, ack)
				return nil
			case <-time.After(time.Until(t.t1)):
			}

			lease, err = client.Renew(ctx, lease)
			if err == nil {
				ack = lease.ACK
				ctrl.applyLease(ctx, r, logger, linkName, ack)
				continue
			}

			logger.Warn("dhcp renew failed, will attempt rebind", zap.Error(err))
			lease, err = client.RequestFromOffer(ctx, lease.Offer)
			if err == nil {
				ack = lease.ACK
				ctrl.applyLease(ctx, r, logger, linkName, ack)
				continue
			}
			logger.Warn("dhcp rebind failed, will wait for expiry", zap.Error(err))

			select {
			case <-ctx.Done():
				ctrl.destroy(ctx, r, linkName)
				return nil
			case <-time.After(time.Until(t.expiry)):
			}

			ctrl.destroy(ctx, r, linkName)
			ack = nil
		}

	}
}

// if the link is physical it wants DHCP
func wantsDHCP(ls *network.LinkStatus) bool {
	return ls.TypedSpec().Physical()
}

type leaseTimers struct {
	t1, t2, expiry time.Time
}

func computeTimers(ack *dhcpv4.DHCPv4) leaseTimers {
	now := time.Now()
	leaseTime := ack.IPAddressLeaseTime(time.Duration(0))
	t1Dur := ack.IPAddressRenewalTime(time.Duration(0))
	t2Dur := ack.IPAddressRebindingTime(time.Duration(0))

	return leaseTimers{
		t1:     now.Add(t1Dur),
		t2:     now.Add(t2Dur),
		expiry: now.Add(leaseTime),
	}
}

func (ctrl *DHCP4Controller) applyLease(ctx context.Context, r controller.Runtime, logger *zap.Logger, linkName resource.ID, ack *dhcpv4.DHCPv4) error {
	addrID := "dhcp4/" + linkName

	addr := network.NewAddressSpec(network.NamespaceName, addrID)
	if err := safe.WriterModify(ctx, r, addr, func(a *network.AddressSpec) error {
		spec := a.TypedSpec()

		addr, _ := netipx.FromStdIPNet(&net.IPNet{
			IP:   ack.YourIPAddr,
			Mask: ack.SubnetMask(),
		})

		spec.Address = addr
		spec.LinkName = linkName
		spec.Family = nethelpers.FamilyInet4
		spec.Scope = nethelpers.ScopeGlobal
		spec.Flags = nethelpers.AddressFlags(nethelpers.AddressPermanent)

		logger.Debug("Creating address spec", zap.Any("spec", spec))

		return nil
	}); err != nil {
		return err
	}

	// TODO: apply routes
	// for _, router := range ack.Router() {
	// 	gw, _ := netipx.FromStdIP(router)
	// }
	return nil
}

func (ctrl *DHCP4Controller) destroy(ctx context.Context, r controller.Runtime, linkName resource.ID) error {
	addrID := "dhcp4/" + linkName
	return r.Destroy(ctx, resource.NewMetadata(network.NamespaceName, network.AddressSpecType, addrID, resource.VersionUndefined))
}
