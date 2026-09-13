package network

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/jsimonetti/rtnetlink"
	"github.com/mdlayher/ethtool"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/controllers/runtime"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/network"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type LinkStatusController struct{}

var _ controller.Controller = (*LinkStatusController)(nil)

// Name implements [controller.Controller].
func (l *LinkStatusController) Name() string {
	return "network.LinkStatusController"
}

// Inputs implements [controller.Controller].
func (l *LinkStatusController) Inputs() []controller.Input {
	return nil
}

// Outputs implements [controller.Controller].
func (l *LinkStatusController) Outputs() []controller.Output {
	return []controller.Output{
		{
			Type: network.LinkStatusType,
			Kind: controller.OutputExclusive,
		},
	}
}

// Run implements [controller.Controller].
func (ctrl *LinkStatusController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	// wait for udevd to be healthy, which implies that all link renames are done
	if err := runtime.WaitForDevicesReady(
		ctx, r,
		[]controller.Input{},
	); err != nil {
		return err
	}

	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return fmt.Errorf("error dialing rtnetlink socket: %w", err)
	}

	defer conn.Close() //nolint:errcheck

	ethClient, err := ethtool.New()
	if err != nil {
		logger.Warn("error dialing ethtool socket", zap.Error(err))
	} else {
		defer ethClient.Close() //nolint:errcheck
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.EventCh():
		}

		if err = ctrl.reconcile(ctx, r, logger, conn, ethClient); err != nil {
			return err
		}

		r.ResetRestartBackoff()
	}
}

func (ctrl *LinkStatusController) reconcile(ctx context.Context, r controller.Runtime, logger *zap.Logger, conn *rtnetlink.Conn, ethClient *ethtool.Client) error {
	// list the existing LinkStatus resources and mark them all to be deleted, as the actual link is discovered via netlink, resource ID is removed from the list
	list, err := r.List(ctx, resource.NewMetadata(network.NamespaceName, network.LinkStatusType, "", resource.VersionUndefined))
	if err != nil {
		return fmt.Errorf("error listing resources: %w", err)
	}

	itemsToDelete := map[resource.ID]struct{}{}
	for _, r := range list.Items {
		itemsToDelete[r.Metadata().ID()] = struct{}{}
	}

	links, err := conn.Link.List()
	if err != nil {
		return err
	}

	for _, link := range links {
		if err := ctrl.syncLink(ctx, r, logger, conn, ethClient, &link); err != nil {
			logger.Warn("could not sync link", zap.String("link", link.Attributes.Name))
			continue
		}
		delete(itemsToDelete, link.Attributes.Name)
	}

	for id := range itemsToDelete {
		if err = r.Destroy(ctx, resource.NewMetadata(network.NamespaceName, network.LinkStatusType, id, resource.VersionUndefined)); err != nil {
			return fmt.Errorf("error deleting link status %q: %w", id, err)
		}
	}

	return nil
}

func (ctrl *LinkStatusController) syncLink(ctx context.Context, r controller.Runtime, logger *zap.Logger, conn *rtnetlink.Conn, ethClient *ethtool.Client, link *rtnetlink.LinkMessage) error {
	logger = logger.With(zap.String("link", link.Attributes.Name))

	var (
		ethState *ethtool.LinkState
		ethInfo  *ethtool.LinkInfo
		ethMode  *ethtool.LinkMode
		err      error
	)

	// Bring up physical interfaces
	if link.Type == uint16(nethelpers.LinkEther) {
		if err := conn.Link.Set(&rtnetlink.LinkMessage{
			Family: unix.AF_UNSPEC,
			Type:   link.Type,
			Index:  link.Index,
			Flags:  unix.IFF_UP,
			Change: unix.IFF_UP,
		}); err != nil {
			return err
		}
	}

	if ethClient != nil {
		// query additional information via ethtool (if supported)
		ethState, err = ethClient.LinkState(ethtool.Interface{
			Index: int(link.Index),
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("error querying ethtool link state", zap.String("link", link.Attributes.Name), zap.Error(err))
		}

		// skip if previous call failed (e.g. not supported)
		if err == nil {
			ethInfo, err = ethClient.LinkInfo(ethtool.Interface{
				Index: int(link.Index),
			})
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				logger.Warn("error querying ethtool link info", zap.String("link", link.Attributes.Name), zap.Error(err))
			}
		}

		// skip if previous call failed (e.g. not supported)
		if err == nil {
			ethMode, err = ethClient.LinkMode(ethtool.Interface{
				Index: int(link.Index),
			})
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				logger.Warn("error querying ethtool link mode", zap.String("link", link.Attributes.Name), zap.Error(err))
			}
		}
	}

	if err := safe.WriterModify(ctx, r, network.NewLinkStatus(network.NamespaceName, link.Attributes.Name), func(r *network.LinkStatus) error {
		status := r.TypedSpec()
		prevUp := status.LinkState

		status.Index = link.Index
		status.HardwareAddr = nethelpers.HardwareAddr(link.Attributes.Address)
		// status.PermanentAddr = nethelpers.HardwareAddr(permanentAddr)
		status.BroadcastAddr = nethelpers.HardwareAddr(link.Attributes.Broadcast)
		status.LinkIndex = link.Attributes.Type
		status.Flags = nethelpers.LinkFlags(link.Flags)
		status.Type = nethelpers.LinkType(link.Type)
		status.QueueDisc = link.Attributes.QueueDisc

		status.MTU = link.Attributes.MTU

		status.OperationalState = nethelpers.OperationalState(link.Attributes.OperationalState)
		if ethState != nil {
			status.LinkState = ethState.Link
		} else {
			status.LinkState = false
		}

		if prevUp != status.LinkState && status.Physical() {
			logger.Info("link state changed", zap.String("link", link.Attributes.Name), zap.Bool("up", status.LinkState))
		}

		if ethInfo != nil {
			status.Port = nethelpers.Port(ethInfo.Port)
		} else {
			status.Port = nethelpers.Port(ethtool.Other)
		}

		if ethMode != nil {
			status.SpeedMegabits = ethMode.SpeedMegabits
			status.Duplex = nethelpers.Duplex(ethMode.Duplex)
		} else {
			status.SpeedMegabits = 0
			status.Duplex = nethelpers.Duplex(ethtool.Unknown)
		}

		var deviceInfo *nethelpers.DeviceInfo

		deviceInfo, err := nethelpers.GetDeviceInfo(link.Attributes.Name)
		if err != nil {
			logger.Warn("failure getting device information from /sys/class/net/*", zap.Error(err), zap.String("link", link.Attributes.Name))
		}

		if deviceInfo != nil {
			status.BusPath = deviceInfo.BusPath
			status.Driver = deviceInfo.Driver
			status.PCIID = deviceInfo.PCIID
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}
