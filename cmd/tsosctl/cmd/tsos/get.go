package tsos

import (
	"context"
	"log"
	"strings"

	"github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	cosiclient "github.com/cosi-project/runtime/pkg/state/protobuf/client"
	"github.com/siderolabs/gen/xslices"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var getCmdFlags struct {
	node      string
	namespace string
}

var getCmd = &cobra.Command{
	Use:        "get <type> [<id>]",
	Aliases:    []string{"g"},
	SuggestFor: []string{},
	Short:      "Get a specific resource or list of resources (use 'tsosctl get rd' to see all available resource types).",
	Long: `Similar to 'kubectl get', 'tsosctl get' returns a set of resources from the OS.
To get a list of all available resource definitions, issue 'tsosctl get rd'`,
	Example: "",
	Args:    cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		client, err := NewClient(ctx, getCmdFlags.node)
		if err != nil {
			return err
		}

		defer client.Close() //nolint:errcheck

		return getResources(ctx, args, client)
	},
}

func getResources(ctx context.Context, args []string, client *client) error {
	resourceType := args[0]

	var resourceID string

	if len(args) == 2 {
		resourceID = args[1]
	}

	rd, err := client.resolveResourceKind(ctx, &getCmdFlags.namespace, resourceType)
	if err != nil {
		return err
	}

	return listResources(ctx, client, rd, resourceID)
}

func listResources(ctx context.Context, client *client, rd *meta.ResourceDefinition, resourceID resource.ID) error {
	resourceType := rd.TypedSpec().Type

	if resourceID == "" {
		items, err := client.COSI.List(
			ctx,
			resource.NewMetadata(getCmdFlags.namespace, resourceType, "", resource.VersionUndefined),
			state.WithListUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
		)
		if err != nil {
			return err
		}

		for _, item := range items.Items {
			log.Printf("%v", item)
		}
	} else {
		r, err := client.COSI.Get(
			ctx,
			resource.NewMetadata(getCmdFlags.namespace, resourceType, resourceID, resource.VersionUndefined),
			state.WithGetUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
		)
		if err != nil {
			return err
		}
		log.Printf("%v", r)
	}

	return nil
}

type client struct {
	conn *grpc.ClientConn
	COSI state.State
}

func NewClient(ctx context.Context, target string) (*client, error) {
	conn, err := grpc.NewClient(target, grpc.WithInitialWindowSize(65535*32),
		grpc.WithInitialConnWindowSize(65535*16),
		grpc.WithTransportCredentials(insecure.NewCredentials()))

	if err != nil {
		return nil, err
	}

	return &client{
		conn: conn,
		COSI: state.WrapCore(cosiclient.NewAdapter(v1alpha1.NewStateClient(conn))),
	}, nil
}

func (c *client) Close() error {
	return c.conn.Close()
}

func (c *client) resolveResourceKind(ctx context.Context, resourceNamespace *resource.Namespace, resourceType resource.Type) (*meta.ResourceDefinition, error) {
	registeredResources, err := safe.StateListAll[*meta.ResourceDefinition](ctx, c.COSI)
	if err != nil {
		return nil, err
	}

	var matched []*meta.ResourceDefinition

	for rd := range registeredResources.All() {
		if strings.EqualFold(rd.Metadata().ID(), resourceType) {
			matched = append(matched, rd)

			continue
		}

		spec := rd.TypedSpec()

		for _, alias := range spec.AllAliases {
			if strings.EqualFold(alias, resourceType) {
				matched = append(matched, rd)

				break
			}
		}
	}

	switch {
	case len(matched) == 1:
		if *resourceNamespace == "" {
			*resourceNamespace = matched[0].TypedSpec().DefaultNamespace
		}

		return matched[0], nil
	case len(matched) > 1:
		matchedTypes := xslices.Map(matched, func(rd *meta.ResourceDefinition) string { return rd.Metadata().ID() })

		return nil, status.Errorf(codes.InvalidArgument, "resource type %q is ambiguous: %v", resourceType, matchedTypes)
	default:
		return nil, status.Errorf(codes.NotFound, "resource %q is not registered", resourceType)
	}
}

func init() {
	getCmd.Flags().StringVar(&getCmdFlags.namespace, "namespace", "", "resource namespace (default is to use default namespace per resource)")
	getCmd.Flags().StringVar(&getCmdFlags.node, "node", "", "")

	addCommand(getCmd)
}
