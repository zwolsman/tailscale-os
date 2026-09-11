package runtime

import (
	"context"

	"github.com/cosi-project/runtime/pkg/state"
)

// Runtime interface describes the runtime environment.
type Runtime interface {
	Logging() LoggingManager
	Events() EventStream
	State() state.State
	ResetRestartBackoff()
}

// EventStream provides the runtime event stream.
type EventStream interface {
	Publish(ctx context.Context, msg any)
	EventCh() <-chan struct{}
}
