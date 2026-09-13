package runtime

import "github.com/cosi-project/runtime/pkg/resource"

//go:generate go tool github.com/siderolabs/deep-copy -type DevicesStatusSpec -o deep_copy.generated.go .

const NamespaceName resource.Namespace = "runtime"
