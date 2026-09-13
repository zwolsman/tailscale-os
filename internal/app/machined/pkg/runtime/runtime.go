package runtime

import (
	"github.com/cosi-project/runtime/pkg/state"
)

// Runtime interface describes the runtime environment.
type Runtime interface {
	Logging() LoggingManager
	Events() EventStream
	State() state.State
	ResetRestartBackoff()
}
