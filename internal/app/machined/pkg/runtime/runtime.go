package runtime

import (
	"context"
)

// Runtime interface describes the runtime environment.
type Runtime interface {
	Logging() LoggingManager
	Events() EventStream
	ResetRestartBackoff()
}

// EventStream provides the runtime event stream.
type EventStream interface {
	Publish(ctx context.Context, msg any)
	EventCh() <-chan struct{}
}
