package v1alpha1

import (
	"context"

	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/cosi-project/runtime/pkg/state/registry"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/network"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/runtime"
	"github.com/zwolsman/tailscale-os/pkg/machinery/resources/v1alpha1"
)

type State struct {
	resources state.State

	namespaceRegistry *registry.NamespaceRegistry
	resourceRegistry  *registry.ResourceRegistry
}

func NewState() (*State, error) {
	s := &State{}
	ctx := context.TODO()

	s.resources = state.WrapCore(namespaced.NewState(inmem.Build))
	s.namespaceRegistry = registry.NewNamespaceRegistry(s.resources)
	s.resourceRegistry = registry.NewResourceRegistry(s.resources)

	if err := s.namespaceRegistry.RegisterDefault(ctx); err != nil {
		return nil, err
	}

	if err := s.resourceRegistry.RegisterDefault(ctx); err != nil {
		return nil, err
	}

	// register Talos namespaces
	for _, ns := range []struct {
		name        string
		description string
	}{
		{v1alpha1.NamespaceName, "Tailscale OS v1alpha1 subsystems glue resources."},
	} {
		if err := s.namespaceRegistry.Register(ctx, ns.name, ns.description); err != nil {
			return nil, err
		}
	}

	for _, r := range []meta.ResourceWithRD{
		&network.AddressSpec{},
		&network.LinkStatus{},
		&network.RouteSpec{},
		&runtime.DevicesStatus{},
		&v1alpha1.Service{},
	} {
		if err := s.resourceRegistry.Register(ctx, r); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// NamespaceRegistry implements [runtime.State].
func (s *State) NamespaceRegistry() *registry.NamespaceRegistry {
	return s.namespaceRegistry
}

// ResourceRegistry implements [runtime.State].
func (s *State) ResourceRegistry() *registry.ResourceRegistry {
	return s.resourceRegistry
}

// Resources implements [runtime.State].
func (s *State) Resources() state.State {
	return s.resources
}
