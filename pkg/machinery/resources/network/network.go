package network

import (
	"fmt"
	"net/netip"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
)

//go:generate go tool github.com/siderolabs/deep-copy -type LinkStatusSpec -type AddressSpecSpec -type RouteSpecSpec -o deep_copy.generated.go .

// NamespaceName contains resources related to networking.
const NamespaceName resource.Namespace = "network"

func RouteID(table nethelpers.RoutingTable, family nethelpers.Family, destination netip.Prefix, gateway netip.Addr, priority uint32, outLinkName string) string {
	dst, _ := destination.MarshalText() //nolint:errcheck
	gw, _ := gateway.MarshalText()      //nolint:errcheck

	prefix := ""

	if table != nethelpers.TableMain {
		prefix = fmt.Sprintf("%s/", table)
	}

	if family == nethelpers.FamilyInet6 {
		prefix += fmt.Sprintf("%s/", outLinkName)
	}

	return fmt.Sprintf("%s%s/%s/%s/%d", prefix, family, string(gw), string(dst), priority)
}
