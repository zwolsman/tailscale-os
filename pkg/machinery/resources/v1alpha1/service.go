package v1alpha1

import (
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/resource/typed"
)

// ServiceType is type of Service resource.
const ServiceType = resource.Type("Services.v1alpha1.tsos.dev")

// Service describes running service state.
type Service = typed.Resource[ServiceSpec, ServiceExtension]

// ServiceSpec describe service state.
//
//gotagsrewrite:gen
type ServiceSpec struct {
	Running bool `yaml:"running" protobuf:"1"`
	Healthy bool `yaml:"healthy" protobuf:"2"`
	Unknown bool `yaml:"unknown" protobuf:"3"`
}

// NewService initializes a Service resource.
func NewService(id resource.ID) *Service {
	return typed.NewResource[ServiceSpec, ServiceExtension](
		resource.NewMetadata(NamespaceName, ServiceType, id, resource.VersionUndefined),
		ServiceSpec{},
	)
}

// ServiceExtension provides auxiliary methods for Service.
type ServiceExtension struct{}

// ResourceDefinition implements meta.ResourceDefinitionProvider interface.
func (ServiceExtension) ResourceDefinition() meta.ResourceDefinitionSpec {
	return meta.ResourceDefinitionSpec{
		Type:             ServiceType,
		Aliases:          []resource.Type{"svc"},
		DefaultNamespace: NamespaceName,
		PrintColumns: []meta.PrintColumn{
			{
				Name:     "Running",
				JSONPath: "{.running}",
			},
			{
				Name:     "Healthy",
				JSONPath: "{.healthy}",
			},
			{
				Name:     "Health Unknown",
				JSONPath: "{.unknown}",
			},
		},
	}
}
