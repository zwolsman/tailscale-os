package v1alpha1

import "github.com/cosi-project/runtime/pkg/resource"

//go:generate go tool github.com/siderolabs/deep-copy -type ServiceSpec -o deep_copy.generated.go .
const NamespaceName resource.Namespace = "runtime"
