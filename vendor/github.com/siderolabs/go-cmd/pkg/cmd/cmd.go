// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package cmd same as exec module but with reaper.
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/armon/circbuf"

	"github.com/siderolabs/go-cmd/pkg/cmd/proc/reaper"
)

type stdinCtxKey string

// ExitError wraps any exit error (reaper or exec).
type ExitError struct {
	Output   []byte
	ExitCode int
}

// Error implements error interface.
func (exitError *ExitError) Error() string {
	return fmt.Sprintf("exit status %d: %s", exitError.ExitCode, exitError.Output)
}

// MaxStderrLen is maximum length of stderr output captured for error message.
const (
	MaxStderrLen = 4096

	stdin stdinCtxKey = "stdin"
)

// WithStdin creates a new context from the existing context
// and sets stdin value.
func WithStdin(ctx context.Context, stdinData io.Reader) context.Context {
	return context.WithValue(ctx, stdin, stdinData)
}

// Run executes a command.
//
// Deprecated: Use RunContext instead, which allows passing a context.
func Run(name string, args ...string) (string, error) {
	return RunContext(context.Background(), name, args...)
}

// RunContext executes a command with context.
//
// Deprecated: use RunWithOptions instead, which allows passing options properly.
func RunContext(ctx context.Context, name string, args ...string) (string, error) {
	var opts []Option

	if stdin := ctx.Value(stdin); stdin != nil {
		stdinReader, ok := stdin.(io.Reader)
		if !ok {
			return "", fmt.Errorf("failed to read stdin object from the context")
		}

		opts = append(opts, WithStandardInput(stdinReader))
	}

	return RunWithOptions(ctx, name, args, opts...)
}

// Options are used to configure the command execution.
type Options struct {
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
	CaptureFullStdout bool
}

// Option is a function that applies a configuration to the Options struct.
type Option func(*Options)

// WithStandardInput returns an Option that sets the stdin for the command.
func WithStandardInput(stdin io.Reader) Option {
	return func(opts *Options) {
		opts.Stdin = stdin
	}
}

// WithFullStdoutCapture returns an Option that enables capturing the full stdout output.
func WithFullStdoutCapture() Option {
	return func(opts *Options) {
		opts.CaptureFullStdout = true
	}
}

// WithStdout returns an Option that streams the command's stdout to w.
//
// Only used by StartWithOptions; ignored by RunWithOptions.
func WithStdout(w io.Writer) Option {
	return func(opts *Options) {
		opts.Stdout = w
	}
}

// WithStderr returns an Option that streams the command's stderr to w.
//
// Only used by StartWithOptions; ignored by RunWithOptions.
func WithStderr(w io.Writer) Option {
	return func(opts *Options) {
		opts.Stderr = w
	}
}

// RunWithOptions executes a command with context and options.
func RunWithOptions(ctx context.Context, name string, args []string, options ...Option) (string, error) {
	var opts Options

	for _, option := range options {
		option(&opts)
	}

	cmd := exec.CommandContext(ctx, name, args...)

	var stdout interface {
		io.Writer
		String() string
	}

	if opts.CaptureFullStdout {
		stdout = new(bytes.Buffer)
	} else {
		var err error

		stdout, err = circbuf.NewBuffer(MaxStderrLen)
		if err != nil {
			return stdout.String(), err
		}
	}

	stderr, err := circbuf.NewBuffer(MaxStderrLen)
	if err != nil {
		return stdout.String(), err
	}

	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = opts.Stdin

	notifyCh := make(chan reaper.ProcessInfo, 8)
	usingReaper := reaper.Notify(notifyCh)

	if usingReaper {
		defer reaper.Stop(notifyCh)
	}

	if err = cmd.Start(); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, stderr.String())
	}

	if err = reaper.WaitWrapper(usingReaper, notifyCh, cmd); err != nil {
		var (
			reaperErr *reaper.ExitError
			execErr   *exec.ExitError
		)

		switch {
		case errors.As(err, &reaperErr):
			return stdout.String(), &ExitError{
				ExitCode: reaperErr.ExitCode,
				Output:   stderr.Bytes(),
			}
		case errors.As(err, &execErr) && execErr.ExitCode() != -1:
			return stdout.String(), &ExitError{
				ExitCode: execErr.ExitCode(),
				Output:   stderr.Bytes(),
			}
		}

		return stdout.String(), fmt.Errorf("%w: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// Process is a handle to a command started with StartWithOptions.
type Process struct {
	// Stdout is a pipe from the command's stdout, set only when WithStdout was
	// not passed. The caller must read it (until EOF) to avoid blocking the
	// process, and finish reading before calling Wait.
	Stdout io.ReadCloser
	// Stderr is a pipe from the command's stderr, set only when WithStderr was
	// not passed. Same read-before-Wait contract as Stdout.
	Stderr io.ReadCloser

	cmd         *exec.Cmd
	notifyCh    chan reaper.ProcessInfo
	usingReaper bool
}

// StartWithOptions starts a (potentially long-running) command and returns
// without waiting for it to finish.
//
// Unless WithStdout/WithStderr are passed, Process.Stdout/Process.Stderr expose
// pipes to stream the command's output. The caller must consume them and then
// call Wait to release resources.
func StartWithOptions(ctx context.Context, name string, args []string, options ...Option) (*Process, error) {
	var opts Options

	for _, option := range options {
		option(&opts)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = opts.Stdin

	p := &Process{cmd: cmd}

	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		var err error

		if p.Stdout, err = cmd.StdoutPipe(); err != nil {
			return nil, err
		}
	}

	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		var err error

		if p.Stderr, err = cmd.StderrPipe(); err != nil {
			return nil, err
		}
	}

	p.notifyCh = make(chan reaper.ProcessInfo, 8)
	p.usingReaper = reaper.Notify(p.notifyCh)

	if err := cmd.Start(); err != nil {
		if p.usingReaper {
			reaper.Stop(p.notifyCh)
		}

		return nil, err
	}

	return p, nil
}

// Wait waits for the command to exit, returning an *ExitError on a non-zero
// exit code. It must be called exactly once, after any Stdout/Stderr pipes have
// been fully read.
func (p *Process) Wait() error {
	if p.usingReaper {
		defer reaper.Stop(p.notifyCh)
	}

	err := reaper.WaitWrapper(p.usingReaper, p.notifyCh, p.cmd)
	if err == nil {
		return nil
	}

	var (
		reaperErr *reaper.ExitError
		execErr   *exec.ExitError
	)

	switch {
	case errors.As(err, &reaperErr):
		return &ExitError{ExitCode: reaperErr.ExitCode}
	case errors.As(err, &execErr) && execErr.ExitCode() != -1:
		return &ExitError{ExitCode: execErr.ExitCode()}
	}

	return err
}
