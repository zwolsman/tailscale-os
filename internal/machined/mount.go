package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// mountSpec describes one filesystem we need mounted before anything
// else can reasonably run.
type mountSpec struct {
	source string
	target string
	fstype string
	flags  uintptr
	data   string
}

// essentialMounts returns the minimal set of virtual filesystems a Linux
// userspace expects to exist. Order matters: /dev must exist before
// /dev/pts, and everything should exist before we spawn any child process.
func essentialMounts() []mountSpec {
	return []mountSpec{
		{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV, ""},
		{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV, ""},
		{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID, "mode=0755"},
		{"devpts", "/dev/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC, "mode=0620,gid=5,ptmxmode=0666"},
		{"tmpfs", "/dev/shm", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=1777"},
		{"tmpfs", "/run", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV, "mode=0755"},
		{"cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV, ""},
	}
}

// mountAll creates target directories as needed and mounts each spec.
// It logs but does not fail hard on individual mount errors for
// filesystems that may already be mounted by the kernel itself
// (e.g. some kernels auto-mount devtmpfs).
func mountAll(specs []mountSpec) error {
	for _, m := range specs {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", m.target, err)
		}

		err := unix.Mount(m.source, m.target, m.fstype, m.flags, m.data)
		if err != nil {
			if err == unix.EBUSY {
				// Already mounted (common for /dev, sometimes /proc under QEMU
				// direct kernel boot). Not fatal.
				logf("mount: %s already mounted, skipping", m.target)
				continue
			}
			return fmt.Errorf("mount %s on %s (%s): %w", m.source, m.target, m.fstype, err)
		}
		logf("mount: %s -> %s (%s) ok", m.source, m.target, m.fstype)
	}
	return nil
}

// syncAndUnmountAll is best-effort filesystem sync + unmount, used during
// shutdown. Errors are logged, not returned, since we want to proceed to
// reboot/poweroff regardless.
func syncAndUnmountAll(specs []mountSpec) {
	unix.Sync()

	// Unmount in reverse order of mounting so nested mounts (e.g. /dev/pts
	// under /dev) come off first.
	for i := len(specs) - 1; i >= 0; i-- {
		target := specs[i].target
		if err := unix.Unmount(target, unix.MNT_DETACH); err != nil {
			logf("unmount: %s failed: %v", target, err)
			continue
		}
		logf("unmount: %s ok", target)
	}
}