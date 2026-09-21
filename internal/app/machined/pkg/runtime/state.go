package runtime

import (
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/registry"
)

// V1Alpha2State defines the next generation (v2) interface binding into v1 runtime.
type State interface {
	Resources() state.State

	NamespaceRegistry() *registry.NamespaceRegistry
	ResourceRegistry() *registry.ResourceRegistry
}
