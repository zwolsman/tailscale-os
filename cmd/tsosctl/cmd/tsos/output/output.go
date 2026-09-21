package output

import (
	"fmt"
	"io"

	"github.com/cosi-project/runtime/pkg/resource"
)

// Writer interface.
type Writer interface {
	WriteResource(r resource.Resource) error
}

func NewWriter(format string, out io.Writer) (Writer, error) {
	switch format {
	case "yaml":
		return NewYAML(out), nil
	default:
		return nil, fmt.Errorf("output format %q is not supported", format)
	}
}
