package health

import "time"

// Settings configures health check
//
// Fields are similar to k8s pod probe definitions.
type Settings struct {
	InitialDelay time.Duration
	Period       time.Duration
	Timeout      time.Duration
}

// DefaultSettings provides some default health check settings.
var DefaultSettings = Settings{
	InitialDelay: time.Second,
	Period:       5 * time.Second,
	Timeout:      500 * time.Millisecond,
}
