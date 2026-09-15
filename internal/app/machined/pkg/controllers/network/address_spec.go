package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/jsimonetti/rtnetlink"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/network"
	"go.uber.org/zap"
	"go4.org/netipx"
)

var _ controller.Controller = (*AddressSpecController)(nil)

type AddressSpecController struct{}

// Name implements [controller.Controller].
func (a *AddressSpecController) Name() string {
	return "network.AddressSpecController"
}

// Inputs implements [controller.Controller].
func (a *AddressSpecController) Inputs() []controller.Input {
	return []controller.Input{
		{
			Type:      network.AddressSpecType,
			Namespace: network.NamespaceName,
			Kind:      controller.InputStrong,
		},
	}
}

// Outputs implements [controller.Controller].
func (a *AddressSpecController) Outputs() []controller.Output {
	return nil
}

// Run implements [controller.Controller].
func (ctrl *AddressSpecController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {

	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return fmt.Errorf("error dialing rtnetlink socket: %w", err)
	}

	defer conn.Close() //nolint:errcheck
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		// list source network configuration resources
		list, err := safe.ReaderList[*network.AddressSpec](ctx, r, resource.NewMetadata(network.NamespaceName, network.AddressSpecType, "", resource.VersionUndefined))
		if err != nil {
			return fmt.Errorf("error listing source network addresses: %w", err)
		}

		// add finalizers for all live resources
		for res := range list.All() {
			if res.Metadata().Phase() != resource.PhaseRunning {
				continue
			}

			if err = r.AddFinalizer(ctx, res.Metadata(), ctrl.Name()); err != nil {
				return fmt.Errorf("error adding finalizer: %w", err)
			}
		}

		// list rtnetlink links (interfaces)
		links, err := conn.Link.List()
		if err != nil {
			return fmt.Errorf("error listing links: %w", err)
		}

		// list rtnetlink addresses
		addrs, err := conn.Address.List()
		if err != nil {
			return fmt.Errorf("error listing addresses: %w", err)
		}

		// loop over addresses and make reconcile decision
		for address := range list.All() {
			if err = ctrl.syncAddress(ctx, r, logger, conn, links, addrs, address); err != nil {
				return err
			}
		}

		r.ResetRestartBackoff()
	}
}

func resolveLinkName(links []rtnetlink.LinkMessage, linkName string) uint32 {
	if linkName == "" {
		return 0 // should never match
	}

	// lookup by name, no support for alias yet
	for _, link := range links {
		if link.Attributes.Name == linkName {
			return link.Index
		}
	}

	return 0
}

func findAddress(addrs []rtnetlink.AddressMessage, linkIndex uint32, ipPrefix netip.Prefix) *rtnetlink.AddressMessage {
	for i, addr := range addrs {
		if addr.Index != linkIndex {
			continue
		}

		if int(addr.PrefixLength) != ipPrefix.Bits() {
			continue
		}

		if !addr.Attributes.Address.Equal(ipPrefix.Addr().AsSlice()) {
			continue
		}

		return &addrs[i]
	}

	return nil
}

// addressFlags returns the flags of the address as reported by the kernel.
//
// The kernel reports the flags twice: truncated to 8 bits in the message header, and in full
// in the IFA_FLAGS attribute (which might be missing on old kernels).
func addressFlags(addr *rtnetlink.AddressMessage) nethelpers.AddressFlags {
	return nethelpers.AddressFlags(addr.Attributes.Flags) | nethelpers.AddressFlags(addr.Flags)
}

func (ctrl *AddressSpecController) syncAddress(ctx context.Context, r controller.Runtime, logger *zap.Logger, conn *rtnetlink.Conn,
	links []rtnetlink.LinkMessage, addrs []rtnetlink.AddressMessage, address *network.AddressSpec,
) error {
	linkIndex := resolveLinkName(links, address.TypedSpec().LinkName)

	switch address.Metadata().Phase() {
	case resource.PhaseTearingDown:
		if linkIndex == 0 {
			// address should be deleted, but link is gone, so assume address is gone
			if err := r.RemoveFinalizer(ctx, address.Metadata(), ctrl.Name()); err != nil {
				return fmt.Errorf("error removing finalizer: %w", err)
			}

			if existing := findAddress(addrs, linkIndex, address.TypedSpec().Address); existing != nil {
				// delete address
				if err := conn.Address.Delete(existing); err != nil {
					return fmt.Errorf("error removing address: %w", err)
				}

				logger.Sugar().Infof("removed address %s from %q", address.TypedSpec().Address, address.TypedSpec().LinkName)
			}

			return nil
		}

		// now remove finalizer as address was deleted
		if err := r.RemoveFinalizer(ctx, address.Metadata(), ctrl.Name()); err != nil {
			return fmt.Errorf("error removing finalizer: %w", err)
		}
	case resource.PhaseRunning:
		if linkIndex == 0 {
			// address can't be assigned as link doesn't exist (yet), skip it
			return nil
		}

		expectedFlags := address.TypedSpec().Flags.Managed()

		if existing := findAddress(addrs, linkIndex, address.TypedSpec().Address); existing != nil {
			// compare only the flags managed by Talos (as we are inspired by them): the rest of the flags are set and cleared by the kernel
			// on its own (e.g. IFA_F_SECONDARY for the IPv4 addresses sharing the subnet, or IFA_F_TENTATIVE
			// while IPv6 DAD is in progress), so enforcing them would delete and re-create the address in a loop
			existingFlags := addressFlags(existing).Managed()

			// check if existing matches the spec: if it does, skip update
			if existing.Scope == uint8(address.TypedSpec().Scope) && existingFlags == expectedFlags {
				return nil
			}

			logger.Debug(
				"replacing address",
				zap.Stringer("address", address.TypedSpec().Address),
				zap.String("link", address.TypedSpec().LinkName),
				zap.Stringer("old_scope", nethelpers.Scope(existing.Scope)),
				zap.Stringer("new_scope", address.TypedSpec().Scope),
				zap.Stringer("old_flags", existingFlags),
				zap.Stringer("new_flags", expectedFlags),
			)

			// delete address to get new one assigned below
			if err := conn.Address.Delete(existing); err != nil {
				return fmt.Errorf("error removing address: %w", err)
			}

			logger.Info("removed address", zap.Stringer("address", address.TypedSpec().Address), zap.String("link", address.TypedSpec().LinkName))
		}

		// add address
		if err := conn.Address.New(&rtnetlink.AddressMessage{
			Family:       uint8(address.TypedSpec().Family),
			PrefixLength: uint8(address.TypedSpec().Address.Bits()),
			Flags:        uint8(expectedFlags),
			Scope:        uint8(address.TypedSpec().Scope),
			Index:        linkIndex,
			Attributes: &rtnetlink.AddressAttributes{
				Address:   address.TypedSpec().Address.Addr().AsSlice(),
				Local:     address.TypedSpec().Address.Addr().AsSlice(),
				Broadcast: broadcastAddr(address.TypedSpec().Address),
				Flags:     uint32(expectedFlags),
			},
		}); err != nil {
			// ignore EEXIST error
			if !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("error adding address %s to %q: %w", address.TypedSpec().Address, address.TypedSpec().LinkName, err)
			}
		}

		logger.Info("assigned address", zap.Stringer("address", address.TypedSpec().Address), zap.String("link", address.TypedSpec().LinkName))

	}

	return nil
}

// broadcastAddr calculates the broadcast address for the given IPv4 prefix.
//
// If the address is not IPv4 or the prefix length is 31 or 32, nil is returned.
func broadcastAddr(addr netip.Prefix) net.IP {
	if !addr.Addr().Is4() {
		return nil
	}

	if addr.Bits() >= 31 {
		return nil
	}

	ipnet := netipx.PrefixIPNet(addr)

	ip := ipnet.IP.To4()
	if ip == nil {
		return nil
	}

	mask := net.IP(ipnet.Mask).To4()

	n := len(ip)
	if n != len(mask) {
		return nil
	}

	out := make(net.IP, n)

	for i := range n {
		out[i] = ip[i] | ^mask[i]
	}

	return out
}
