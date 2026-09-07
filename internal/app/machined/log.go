package main

import (
	"fmt"
	"os"
	"time"
)

// logf writes a timestamped line to stdout, which under a serial-console
// boot (QEMU included) lands directly on the console. Once the kernel and
// devtmpfs are up, this should be switched to also (or instead) write to
// /dev/kmsg so logs survive without a working console and are visible via
// `dmesg`. Keeping this as its own function now means that swap is a
// one-file change later.
func logf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stdout, "[%s] machined: "+format+"\n", append([]any{ts}, args...)...)
}