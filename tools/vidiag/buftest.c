/* Decide how musl buffers stdout on this device when stdout is a file.
 *
 * libkvm reports its init failures with printf, and the server sends stdout to
 * /dev/null. Before redirecting stdout to a file that a collector can read, we
 * must know whether a printf reaches that file immediately or waits in a
 * buffer. A fault that prints 65 bytes and then continues to run would sit in
 * a 1024-byte buffer for hours, and the redirection would look correct while
 * it captured nothing.
 *
 *   buftest        - default buffering, as libkvm has it now
 *   buftest line   - after setvbuf(stdout, NULL, _IOLBF, 0)
 *
 * Print one short line per second for 12 seconds. Measure the file while the
 * program still runs. If the file stays empty until exit, stdout is fully
 * buffered and a redirection alone does not work.
 */
#include <stdio.h>
#include <string.h>
#include <unistd.h>

int main(int argc, char **argv)
{
	int i;

	if (argc > 1 && strcmp(argv[1], "line") == 0)
		setvbuf(stdout, NULL, _IOLBF, 0);

	for (i = 0; i < 12; i++) {
		printf("_mmf_vpss_init_new failed. s32Ret: 0x%x !\n", 0xc0078003);
		sleep(1);
	}
	return 0;
}
