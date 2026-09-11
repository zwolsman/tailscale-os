package network

import (
	"context"
	"fmt"

	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/resource/typed"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"

	"go.uber.org/zap"
)

type LinkSpecController struct{}

var _ controller.Controller = (*LinkSpecController)(nil)

// Name implements [controller.Controller].
func (l *LinkSpecController) Name() string {
	return "network.LinkSpecController"
}

// Inputs implements [controller.Controller].
func (l *LinkSpecController) Inputs() []controller.Input {
	return nil
}

// Outputs implements [controller.Controller].
func (l *LinkSpecController) Outputs() []controller.Output {
	return []controller.Output{
		{
			Type: LinkSpecType,
			Kind: controller.OutputShared,
		},
	}
}

// Run implements [controller.Controller].
func (ctrl *LinkSpecController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	// wait for udevd to be healthy, which implies that all link renames are done
	// if err := runtime.WaitForDevicesReady(
	// 	ctx, r,
	// 	[]controller.Input{
	// 		{
	// 			Namespace: NamespaceName,
	// 			Type:      LinkSpecType,
	// 			Kind:      controller.InputStrong,
	// 		},
	// 	},
	// ); err != nil {
	// 	return err
	// }

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

		if err = ctrl.reconcile(ctx, r, logger, conn); err != nil {
			return err
		}
	}
}

func (ctrl *LinkSpecController) reconcile(ctx context.Context, r controller.Runtime, logger *zap.Logger, conn *rtnetlink.Conn) error {
	links, err := conn.Link.List()
	if err != nil {
		return err
	}

	for _, link := range links {
		if err := ctrl.syncLink(ctx, r, logger, conn, &link); err != nil {
			logger.Warn("could not sync link", zap.String("link", link.Attributes.Name))
			continue
		}
	}

	return nil
}

func (ctrl *LinkSpecController) syncLink(ctx context.Context, r controller.Runtime, logger *zap.Logger, conn *rtnetlink.Conn, link *rtnetlink.LinkMessage) error {
	logger = logger.With(zap.String("link", link.Attributes.Name))

	if err := conn.Link.Set(&rtnetlink.LinkMessage{
		Family: unix.AF_UNSPEC,
		Type:   link.Type,
		Index:  link.Index,
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
	}); err != nil {
		return err
	}

	if err := safe.WriterModify(ctx, r, NewLink("network", link.Attributes.Name), func(r *LinkSpec) error {
		status := r.TypedSpec()

		status.Index = link.Index
		status.Name = link.Attributes.Name
		status.MTU = int(link.Attributes.MTU)

		return nil
	}); err != nil {
		return err
	}

	return nil
}

// LinkSpecType is type of LinkSpec resource.
const LinkSpecType = resource.Type("LinkSpecs.net.talos.dev")

// LinkSpec resource holds physical network link status.
type LinkSpec = typed.Resource[LinkSpecSpec, LinkSpecSpecExtension]

type LinkSpecSpec struct {
	Index uint32
	Name  string
	MTU   int
}

func (a LinkSpecSpec) DeepCopy() LinkSpecSpec {
	var b LinkSpecSpec

	b.Index = a.Index
	b.Name = a.Name
	b.MTU = a.MTU

	return b
}

type LinkSpecSpecExtension struct{}

// ResourceDefinition implements [typed.Extension] interface.
func (LinkSpecSpecExtension) ResourceDefinition() meta.ResourceDefinitionSpec {
	return meta.ResourceDefinitionSpec{
		Type:         LinkSpecType,
		Aliases:      []resource.Type{},
		PrintColumns: []meta.PrintColumn{},
	}
}

func NewLink(namespace resource.Namespace, id resource.ID) *LinkSpec {
	return typed.NewResource[LinkSpecSpec, LinkSpecSpecExtension](
		resource.NewMetadata(namespace, LinkSpecType, id, resource.VersionUndefined),
		LinkSpecSpec{},
	)
}
