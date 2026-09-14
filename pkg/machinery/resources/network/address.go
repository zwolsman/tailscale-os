package network

import (
	"net/netip"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/resource/typed"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
)

// LinkStatusType is type of LinkStatus resource.
const AddressSpecType = resource.Type("Addresses.net.tsos.dev")

// LinkStatus resource holds physical network link status.
type AddressSpec = typed.Resource[AddressSpecSpec, AddressSpecExtension]

type AddressSpecSpec struct {
	Address  netip.Prefix            `yaml:"address" protobuf:"1"`
	LinkName string                  `yaml:"linkName" protobuf:"2"`
	Family   nethelpers.Family       `yaml:"family" protobuf:"3"`
	Scope    nethelpers.Scope        `yaml:"scope" protobuf:"4"`
	Flags    nethelpers.AddressFlags `yaml:"flags" protobuf:"5"`
}

// NewAddressSpec initializes a AddressSpec resource.
func NewAddressSpec(namespace resource.Namespace, id resource.ID) *AddressSpec {
	return typed.NewResource[AddressSpecSpec, AddressSpecExtension](
		resource.NewMetadata(namespace, AddressSpecType, id, resource.VersionUndefined),
		AddressSpecSpec{},
	)
}

type AddressSpecExtension struct{}

// ResourceDefinition implements [typed.Extension] interface.
func (AddressSpecExtension) ResourceDefinition() meta.ResourceDefinitionSpec {
	return meta.ResourceDefinitionSpec{
		Type:             AddressSpecType,
		Aliases:          []resource.Type{},
		DefaultNamespace: NamespaceName,
		PrintColumns:     []meta.PrintColumn{},
	}
}
