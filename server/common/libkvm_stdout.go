//go:build !novision

package common

/*
#include <stdio.h>

static void kvm_line_buffer_stdout(void) {
	setvbuf(stdout, NULL, _IOLBF, 0);
}
*/
import "C"

// init asks libc to write stdout at the end of every line.
//
// libkvm reports a capture pipeline that does not start with printf, and those
// messages are the only record of the failure. musl decides how to buffer
// stdout at the first write. If stdout is not a terminal, musl releases that
// first line and then buffers in full. Every message after it waits for the
// 1024-byte buffer to fill, or for the process to exit. The server does not
// exit, so a failure that prints 65 bytes stays in the buffer while the board
// runs, and a log file collects one line and then nothing.
//
// tools/vidiag/buftest.c measures this on the device. It prints 12 messages,
// one each second, to a file. The default releases 1 message in the first 5
// seconds and the other 11 at exit. After this call it releases 5.
//
// The Go runtime writes with write(2) and does not use C stdio, so this call
// changes the output of the C libraries only. libkvm and the Go code share one
// libc, and therefore one stdout, so one call covers both.
func init() {
	C.kvm_line_buffer_stdout()
}
