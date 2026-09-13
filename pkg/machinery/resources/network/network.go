package network

import "github.com/cosi-project/runtime/pkg/resource"

//go:generate go tool github.com/siderolabs/deep-copy -type LinkStatusSpec -o deep_copy.generated.go .

// NamespaceName contains resources related to networking.
const NamespaceName resource.Namespace = "network"
