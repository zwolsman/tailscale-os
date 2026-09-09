package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/runtime"
)

var _ runtime.LoggingManager = (*SimpleLoggingManager)(nil)

type SimpleLoggingManager struct {
	fallbackLogger *log.Logger
}

func NewSimpleLoggerManager(fallbackLogger *log.Logger) *SimpleLoggingManager {
	return &SimpleLoggingManager{
		fallbackLogger: fallbackLogger,
	}
}

// RegisteredLogs implements [runtime.LoggingManager].
func (s *SimpleLoggingManager) RegisteredLogs() []string {
	panic("unimplemented")
}

// ServiceLog implements [runtime.LoggingManager].
func (s *SimpleLoggingManager) ServiceLog(service string) runtime.LogHandler {
	return &handler{
		manager: s,
		id:      service,
		fields: map[string]any{
			// use field name that is not used by anything else
			"talos-service": service,
		},
	}
}

type handler struct {
	manager *SimpleLoggingManager
	id      string
	fields  map[string]any
}

// Reader implements [runtime.LogHandler].
func (h *handler) Reader(opt ...runtime.LogOption) (io.ReadCloser, error) {
	panic("unimplemented")
}

// Writer implements [runtime.LogHandler].
func (h *handler) Writer() (io.WriteCloser, error) {
	return &timeStampWriter{
		w: os.Stdout,
	}, nil
}

// SetLineWriter implements [runtime.LoggingManager].
func (s *SimpleLoggingManager) SetLineWriter(w runtime.LogWriter) {
	panic("unimplemented")
}

// SetSenders implements [runtime.LoggingManager].
func (s *SimpleLoggingManager) SetSenders(senders []runtime.LogSender) []runtime.LogSender {
	panic("unimplemented")
}

// timeStampWriter is a writer that adds a timestamp to each line.
type timeStampWriter struct {
	w io.WriteCloser
}

// Write implements the io.Writer interface.
func (t *timeStampWriter) Write(p []byte) (int, error) {
	buf := make([]byte, 0, len(p)+27)

	// Current log.Logger implementation always adds a newline to the message, so we don't need to wait for it.
	buf = time.Now().AppendFormat(buf, "2006/01/02 15:04:05.000000")
	buf = append(buf, ' ')
	buf = append(buf, p...)

	n, err := t.w.Write(buf)

	switch {
	case err == nil && n == len(buf):
		return len(p), nil // success, return original length
	case err == nil && n != len(buf):
		return n, fmt.Errorf("time stamp writer error: %w", io.ErrShortWrite)
	default:
		return n, fmt.Errorf("time stamp writer internal error: %w", err)
	}
}

// Close implements the io.Closer interface.
func (t *timeStampWriter) Close() error { return t.w.Close() }
