package process

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/events"
	"github.com/zwolsman/tailscale-os/internal/app/machined/pkg/system/runner"
)

// processRunner is a runner.Runner that runs a process on the host.
type processRunner struct {
	args  *runner.Args
	opts  *runner.Options
	debug bool
}

type commandWrapper struct {
	// launcher *cap.Launcher
	// ctty         optional.Optional[int]
	// selinuxLabel string
	// cgroupFile *os.File
	stdin  *os.File
	stdout *os.File
	stderr *os.File
	// afterStart func()
}

// NewRunner creates runner.Runner that runs a process on the host.
func NewRunner(debug bool, args *runner.Args, setters ...runner.Option) runner.Runner {
	r := &processRunner{
		args:  args,
		opts:  runner.DefaultOptions(),
		debug: debug,
	}

	for _, setter := range setters {
		setter(r.opts)
	}

	return r
}

// Close implements [runner.Runner].
func (p *processRunner) Close() error {
	return nil
}

// Open implements [runner.Runner].
func (p *processRunner) Open() error {
	return nil
}

// Run implements [runner.Runner].
func (p *processRunner) Run(ctx context.Context, eventSink events.Recorder, onStart runner.OnStart) (runner.Status, error) {
	var status runner.Status

	if len(p.args.ProcessArgs) == 0 {
		return status, fmt.Errorf("no process args specified")
	}

	wrapper, err := p.build()
	if err != nil {
		return status, err
	}

	cmd := exec.Command(p.args.ProcessArgs[0], p.args.ProcessArgs[1:]...)
	cmd.Env = p.opts.Env

	cmd.Stdin = wrapper.stdin
	cmd.Stdout = wrapper.stdout
	cmd.Stderr = wrapper.stderr

	if err := cmd.Start(); err != nil {
		return status, fmt.Errorf("error starting process: %w", err)
	}

	status.Started = true

	if onStart != nil {
		onStart(int32(cmd.Process.Pid))
	}

	eventSink(events.StateRunning, "Process %s started with PID %d", p, cmd.Process.Pid)

	waitCh := make(chan error, 1)

	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		// process exited on its own
		status.ExitCode = cmd.ProcessState.ExitCode()

		return status, err
	case <-ctx.Done():
		// graceful stop requested
		eventSink(events.StateStopping, "Sending SIGTERM to %s", p)

		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			eventSink(events.StateStopping, "Failed to send SIGTERM to %s: %s", p, err)
		}
	}

	select {
	case err := <-waitCh:
		// process exited after SIGTERM
		status.ExitCode = cmd.ProcessState.ExitCode()

		return status, err
	case <-time.After(p.opts.GracefulShutdownTimeout):
		// still running, escalate
		eventSink(events.StateStopping, "Sending SIGKILL to %s", p)

		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
			eventSink(events.StateStopping, "Failed to send SIGKILL to %s: %s", p, err)
		}
	}

	// wait for the process to actually terminate
	err = <-waitCh
	status.ExitCode = cmd.ProcessState.ExitCode()

	return status, err
}

func (p *processRunner) build() (commandWrapper, error) {
	wrapper := commandWrapper{}
	// Setup logging.
	logSink, err := p.opts.LoggingManager.ServiceLog(p.args.ID).Writer()
	if err != nil {
		return commandWrapper{}, fmt.Errorf("service log handler: %w", err)
	}

	logWriter := io.MultiWriter(logSink)
	pr, pw, err := os.Pipe()

	go func() {
		defer pr.Close()
		defer logSink.Close()

		io.Copy(logWriter, pr)
	}()

	// close the writer if we exit early due to an error
	closeWriter := true

	afterStartClosers := []io.Closer{pw}

	closeLogging := func() {
		for _, closer := range afterStartClosers {
			closer.Close()
		}
	}

	defer func() {
		if closeWriter {
			closeLogging()
		}
	}()

	if p.opts.StdinFile != "" {
		stdin, err := os.Open(p.opts.StdinFile)
		if err != nil {
			return commandWrapper{}, err
		}

		wrapper.stdin = stdin

		afterStartClosers = append(afterStartClosers, stdin)
	}

	if p.opts.StdoutFile != "" {
		stdout, err := os.OpenFile(p.opts.StdoutFile, os.O_WRONLY, 0)
		if err != nil {
			return commandWrapper{}, err
		}

		wrapper.stdout = stdout

		afterStartClosers = append(afterStartClosers, stdout)
	} else {
		wrapper.stdout = pw
	}

	if p.opts.StderrFile != "" {
		stderr, err := os.OpenFile(p.opts.StderrFile, os.O_WRONLY, 0)
		if err != nil {
			return commandWrapper{}, err
		}

		wrapper.stderr = stderr

		afterStartClosers = append(afterStartClosers, stderr)
	} else {
		wrapper.stderr = pw
	}

	closeWriter = false

	return wrapper, nil
}

// String implements [runner.Runner].
func (p *processRunner) String() string {
	return fmt.Sprintf("Process(%q)", p.args.ProcessArgs)
}
