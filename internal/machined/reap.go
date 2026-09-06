package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// describeWaitStatus renders a unix.WaitStatus in a human-readable form.
// unix.WaitStatus (unlike syscall.WaitStatus on some platforms) has no
// String() method, so we format the cases we actually care about.
func describeWaitStatus(ws unix.WaitStatus) string {
	switch {
	case ws.Exited():
		return fmt.Sprintf("exit code %d", ws.ExitStatus())
	case ws.Signaled():
		return fmt.Sprintf("killed by signal %v", ws.Signal())
	default:
		return fmt.Sprintf("status 0x%x", uint32(ws))
	}
}

// reapChildren waits for any exited/signalled child process and reaps it.
// As PID 1, every orphaned process in the system gets reparented to us, so
// if we never wait() on them they pile up as zombies forever. This should
// be called whenever we receive SIGCHLD, and once more in a drain loop
// since multiple children can exit before we get scheduled again.
//
// It returns the pid and exit status of the reaped supervised child, if
// that child was among those reaped, so the caller can react (e.g. restart
// tailscaled). For any other pid (typically re-parented orphans), the
// result is discarded here - we just reap them so they don't leak.
func reapChildren(supervisedPID int) (exited bool, status unix.WaitStatus) {
	for {
		var ws unix.WaitStatus
		pid, err := unix.Wait4(-1, &ws, unix.WNOHANG, nil)
		if err != nil || pid <= 0 {
			// ECHILD means no children exist at all; pid == 0 means none
			// are ready to be reaped right now. Either way, we're done
			// draining for this call.
			return exited, status
		}

		logf("reap: pid %d exited (%s)", pid, describeWaitStatus(ws))

		if pid == supervisedPID {
			exited = true
			status = ws
		}
		// Loop again - there may be more than one exited child queued.
	}
}