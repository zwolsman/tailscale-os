package network

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/jsimonetti/rtnetlink"
	"github.com/siderolabs/gen/value"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/network"
	"go.uber.org/zap"
)

var _ controller.Controller = (*RouteSpecController)(nil)

type RouteSpecController struct{}

// Name implements [controller.Controller].
func (r *RouteSpecController) Name() string {
	return "network.RouteSpecController"
}

// Inputs implements [controller.Controller].
func (r *RouteSpecController) Inputs() []controller.Input {
	return []controller.Input{
		{
			Namespace: network.NamespaceName,
			Type:      network.RouteSpecType,
			Kind:      controller.InputStrong,
		},
	}
}

// Outputs implements [controller.Controller].
func (r *RouteSpecController) Outputs() []controller.Output {
	return nil
}

// Run implements [controller.Controller].
func (ctrl *RouteSpecController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
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
		list, err := safe.ReaderListAll[*network.RouteSpec](ctx, r)
		if err != nil {
			return fmt.Errorf("error listing source network routes: %w", err)
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

		// list rtnetlink routes
		routes, err := conn.Route.List()
		if err != nil {
			return fmt.Errorf("error listing addresses: %w", err)
		}

		// loop over routes and make reconcile decision
		for route := range list.All() {
			if err = ctrl.syncRoute(ctx, r, logger, conn, links, routes, route); err != nil {
				return err // TODO: multi error
			}
		}

		r.ResetRestartBackoff()
	}
}

func (ctrl *RouteSpecController) syncRoute(ctx context.Context, r controller.Runtime, logger *zap.Logger, conn *rtnetlink.Conn, links []rtnetlink.LinkMessage, routes []rtnetlink.RouteMessage, route *network.RouteSpec) error {
	linkIndex := resolveLinkName(links, route.TypedSpec().OutLinkName)

	isMultipath := false // len(route.TypedSpec().NextHops) > 0

	if isMultipath {
		return errors.New("multi path is unsupported")
	}

	destinationStr := route.TypedSpec().Destination.String()
	if value.IsZero(route.TypedSpec().Destination) {
		destinationStr = "default"
	}

	sourceStr := route.TypedSpec().Source.String()
	if value.IsZero(route.TypedSpec().Source) {
		sourceStr = ""
	}

	gatewayStr := route.TypedSpec().Gateway.String()
	if value.IsZero(route.TypedSpec().Gateway) {
		gatewayStr = ""
	}

	switch route.Metadata().Phase() {
	case resource.PhaseTearingDown:
		for _, existing := range findMatchingRoutes(routes, route.TypedSpec()) {
			// delete route
			if err := conn.Route.Delete(existing); err != nil {
				return fmt.Errorf("error removing route: %w", err)
			}

			logger.Info(
				"deleted route",
				zap.String("destination", destinationStr),
				zap.String("gateway", gatewayStr),
				zap.Stringer("table", route.TypedSpec().Table),
				zap.String("link", route.TypedSpec().OutLinkName),
				zap.Uint32("priority", route.TypedSpec().Priority),
				zap.Stringer("family", route.TypedSpec().Family),
				zap.Stringer("type", route.TypedSpec().Type),
			)
		}

		// now remove finalizer as address was deleted
		if err := r.RemoveFinalizer(ctx, route.Metadata(), ctrl.Name()); err != nil {
			return fmt.Errorf("error removing finalizer: %w", err)
		}
	case resource.PhaseRunning:
		if linkIndex == 0 && route.TypedSpec().OutLinkName != "" {
			// route can't be created as link doesn't exist (yet), skip it
			return nil
		}

		matchFound := false

		for _, existing := range findMatchingRoutes(routes, route.TypedSpec()) {
			var existingMTU uint32

			if existing.Attributes.Metrics != nil {
				existingMTU = existing.Attributes.Metrics.MTU
			}

			// check if existing route matches the spec: if it does, skip update
			if routeScopeMatches(existing, route.TypedSpec()) &&
				nethelpers.RouteFlags(existing.Flags).Equal(route.TypedSpec().Flags) &&
				existing.Protocol == uint8(route.TypedSpec().Protocol) &&
				linkIndexMatches(existing.Attributes.OutIface, linkIndex) &&
				(value.IsZero(route.TypedSpec().Source) ||
					existing.Attributes.Src.Equal(route.TypedSpec().Source.AsSlice())) &&
				existingMTU == route.TypedSpec().MTU &&
				existing.Type == uint8(route.TypedSpec().Type) {
				matchFound = true

				continue
			}

			// delete the route, it doesn't match the spec
			if err := conn.Route.Delete(existing); err != nil {
				return fmt.Errorf("error removing route: %w", err)
			}

			logger.Debug(
				"removed route due to mismatch",
				zap.String("destination", destinationStr),
				zap.String("gateway", gatewayStr),
				zap.Stringer("table", route.TypedSpec().Table),
				zap.String("link", route.TypedSpec().OutLinkName),
				zap.Uint32("priority", route.TypedSpec().Priority),
				zap.Stringer("family", route.TypedSpec().Family),
				zap.Stringer("old_scope", nethelpers.Scope(existing.Scope)),
				zap.Stringer("new_scope", route.TypedSpec().Scope),
				zap.Stringer("old_flags", nethelpers.RouteFlags(existing.Flags)),
				zap.Stringer("new_flags", route.TypedSpec().Flags),
				zap.Stringer("old_protocol", nethelpers.RouteProtocol(existing.Protocol)),
				zap.Stringer("new_protocol", route.TypedSpec().Protocol),
				zap.Uint32("old_link_index", existing.Attributes.OutIface),
				zap.Uint32("new_link_index", linkIndex),
				zap.Stringer("old_source", existing.Attributes.Src),
				zap.String("new_source", sourceStr),
				zap.Uint32("old_mtu", existingMTU),
				zap.Uint32("new_mtu", route.TypedSpec().MTU),
				zap.Stringer("old_type", nethelpers.RouteType(existing.Type)),
				zap.Stringer("new_type", route.TypedSpec().Type),
			)
		}

		if matchFound {
			return nil
		}

		routeAttributes := rtnetlink.RouteAttributes{
			Dst:      route.TypedSpec().Destination.Addr().AsSlice(),
			Src:      route.TypedSpec().Source.AsSlice(),
			Priority: route.TypedSpec().Priority,
			Table:    uint32(route.TypedSpec().Table),
			OutIface: linkIndex,
			Gateway:  route.TypedSpec().Gateway.AsSlice(),
		}

		if route.TypedSpec().MTU != 0 {
			routeAttributes.Metrics = &rtnetlink.RouteMetrics{
				MTU: route.TypedSpec().MTU,
			}
		}

		// add route
		msg := &rtnetlink.RouteMessage{
			Family:     uint8(route.TypedSpec().Family),
			DstLength:  uint8(netipPrefixBitsCorrected(route.TypedSpec().Destination)),
			SrcLength:  0,
			Protocol:   uint8(route.TypedSpec().Protocol),
			Scope:      uint8(route.TypedSpec().Scope),
			Type:       uint8(route.TypedSpec().Type),
			Flags:      uint32(route.TypedSpec().Flags),
			Attributes: routeAttributes,
		}

		if err := conn.Route.Add(msg); err != nil {
			return fmt.Errorf("error adding route: %w, message %+v", err, *msg)
		}

		logger.Info(
			"created route",
			zap.String("destination", destinationStr),
			zap.String("gateway", gatewayStr),
			zap.Stringer("table", route.TypedSpec().Table),
			zap.String("link", route.TypedSpec().OutLinkName),
			zap.Uint32("priority", route.TypedSpec().Priority),
			zap.Stringer("family", route.TypedSpec().Family),
			zap.Stringer("type", route.TypedSpec().Type),
		)
	}

	return nil
}

func findMatchingRoutes(existingRoutes []rtnetlink.RouteMessage, expected *network.RouteSpecSpec) []*rtnetlink.RouteMessage {
	var result []*rtnetlink.RouteMessage //nolint:prealloc

	for _, route := range existingRoutes {
		if !routeFamilyMatches(&route, expected) {
			continue
		}

		if !routeDestionationMatches(&route, expected) {
			continue
		}

		if !routeGatewayMatches(&route, expected) {
			continue
		}

		if !routeTableMatches(&route, expected) {
			continue
		}

		if !routePriorityMatches(&route, expected) {
			continue
		}

		result = append(result, &route)
	}

	return result
}

func routeFamilyMatches(route *rtnetlink.RouteMessage, spec *network.RouteSpecSpec) bool {
	return route.Family == uint8(spec.Family)
}

func routeDestionationMatches(route *rtnetlink.RouteMessage, spec *network.RouteSpecSpec) bool {
	if int(route.DstLength) != netipPrefixBitsCorrected(spec.Destination) {
		return false
	}

	return route.DstLength == 0 || route.Attributes.Dst.Equal(spec.Destination.Addr().AsSlice())
}

func routeGatewayMatches(route *rtnetlink.RouteMessage, spec *network.RouteSpecSpec) bool {
	return route.Attributes.Gateway.Equal(spec.Gateway.AsSlice())
}

func routeTableMatches(route *rtnetlink.RouteMessage, spec *network.RouteSpecSpec) bool {
	return nethelpers.RoutingTable(route.Table) == spec.Table
}

func routePriorityMatches(route *rtnetlink.RouteMessage, spec *network.RouteSpecSpec) bool {
	if route.Attributes.Priority == spec.Priority {
		return true
	}

	// Linux assigns metric 1024 to IPv6 routes added without an explicit metric.
	return spec.Family == nethelpers.FamilyInet6 &&
		spec.Priority == 0 &&
		route.Attributes.Priority == network.DefaultRouteMetric
}

// routeScopeMatches compares the actual rtm scope (as reported by the kernel), with the expected scope, as defined in the RouteSpec.
//
// The kernel accepts any scope on RTM_NEWROUTE. However, the route scope is an IPv4-only concept.
// In the case of IPv6, the kernel ignores the provided scope, and the IPv6 FIB (fib6_info) doesn't even have an equivalent scope field.
// When the route is read back, the kernel always fills the returned route's scope with RT_SCOPE_UNIVERSE (nethelpers.ScopeGlobal), in
// rt6_fill_node(). That's why we only assert the scope in non-IPv6 scenarios.
func routeScopeMatches(route *rtnetlink.RouteMessage, expected *network.RouteSpecSpec) bool {
	if expected.Family == nethelpers.FamilyInet6 {
		return true
	}

	return route.Scope == uint8(expected.Scope)
}

// linkIndexMatches reports whether the egress link the kernel reports matches the one the spec asked for.
//
// A spec with no out-link (link index zero, e.g. a route learned from a numbered BGP peer) matches
// whatever egress device the kernel resolved from the gateway: the kernel always reports a resolved
// interface index back, so comparing it verbatim would never match and the route would be deleted
// and re-added on every reconcile.
func linkIndexMatches(actual, expected uint32) bool {
	return expected == 0 || actual == expected
}

// netipPrefixBitsCorrected returns the number of bits in the prefix, corrected for zero value to have bits of 0.
//
// Go stdlib returns -1 for zero value, which is not what we want.
func netipPrefixBitsCorrected(p netip.Prefix) int {
	if p.Addr().AsSlice() == nil {
		return 0
	}

	return p.Bits()
}
