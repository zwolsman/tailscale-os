package events

import (
	"time"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/health"
)

const MaxEventsToKeep = 64

// ServiceState is enum of service run states.
type ServiceState int

// ServiceState constants.
const (
	StateInitialized ServiceState = iota
	StatePreparing
	StateWaiting
	StateRunning
	StateStopping
	StateFinished
	StateFailed
	StateSkipped
	StateStarting
)

func (state ServiceState) String() string {
	switch state {
	case StateInitialized:
		return "Initialized"
	case StateStarting:
		return "Starting"
	case StatePreparing:
		return "Preparing"
	case StateWaiting:
		return "Waiting"
	case StateRunning:
		return "Running"
	case StateStopping:
		return "Stopping"
	case StateFinished:
		return "Finished"
	case StateFailed:
		return "Failed"
	case StateSkipped:
		return "Skipped"
	default:
		return "Unknown"
	}
}

// ServiceEvent describes state change of the running service.
type ServiceEvent struct {
	Message   string
	State     ServiceState
	Health    health.Status
	Timestamp time.Time
}

// ServiceEvents is a fixed length history of events.
type ServiceEvents struct {
	events    []ServiceEvent
	pos       int
	discarded uint
}

// Push appends new event to the history popping out oldest event on overflow.
func (events *ServiceEvents) Push(event ServiceEvent) {
	if events.events == nil {
		events.events = make([]ServiceEvent, MaxEventsToKeep)
	}

	if events.events[events.pos].Message != "" {
		// overwriting some entry
		events.discarded++
	}

	events.events[events.pos] = event
	events.pos = (events.pos + 1) % len(events.events)
}

// Get return a copy of event history, with most recent event being the last one.
func (events *ServiceEvents) Get(count int) (result []ServiceEvent) {
	if events.events == nil {
		return result
	}

	if count > MaxEventsToKeep {
		count = MaxEventsToKeep
	}

	n := len(events.events)

	for i := (events.pos - count + n) % n; count > 0; i = (i + 1) % n {
		if events.events[i].Message != "" {
			result = append(result, events.events[i])
		}

		count--
	}

	return result
}

// Recorder adds new event to the history of events, formatting message with args using Sprintf.
type Recorder func(newstate ServiceState, message string, args ...any)

// NullRecorder discards events.
func NullRecorder(newstate ServiceState, message string, args ...any) {
}
