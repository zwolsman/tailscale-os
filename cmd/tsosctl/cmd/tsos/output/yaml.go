package output

import (
	"fmt"
	"io"

	"github.com/cosi-project/runtime/pkg/resource"
	yaml "go.yaml.in/yaml/v4"
)

// YAML outputs resources in YAML format.
type YAML struct {
	needDashes bool
	withEvents bool
	writer     io.Writer
}

// NewYAML initializes YAML resource output.
func NewYAML(writer io.Writer) *YAML {
	return &YAML{
		writer: writer,
	}
}

// WriteResource implements output.Writer interface.
func (y *YAML) WriteResource(r resource.Resource) error {
	out, err := resource.MarshalYAML(r)
	if err != nil {
		return err
	}

	if y.needDashes {
		fmt.Fprintln(y.writer, "---")
	}

	y.needDashes = true

	return yaml.NewEncoder(y.writer).Encode(out)
}
