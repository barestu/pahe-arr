// Package dl turns the downloaded MPEG-TS file into an .mp4 with ffmpeg. No network access:
// ffmpeg only reads the local file and copies the streams (no re-encode).
package dl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// RemuxArgs builds the ffmpeg command line. Exposed for tests.
func RemuxArgs(in, out string) []string {
	return []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", in,
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4", out + ".part",
	}
}

// Remux writes <out>.part and renames it on success, so importers never see half a file.
// ffmpeg's error output is included in the returned error.
func Remux(ctx context.Context, in, out string, stderr io.Writer) error {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", RemuxArgs(in, out)...)
	if stderr != nil {
		cmd.Stderr = io.MultiWriter(stderr, &buf)
	} else {
		cmd.Stderr = &buf
	}
	if err := cmd.Run(); err != nil {
		_ = os.Remove(out + ".part")
		msg := strings.TrimSpace(buf.String())
		if len(msg) > 400 {
			msg = "…" + msg[len(msg)-400:]
		}
		if msg != "" {
			return fmt.Errorf("ffmpeg remux: %w: %s", err, msg)
		}
		return fmt.Errorf("ffmpeg remux: %w", err)
	}
	return os.Rename(out+".part", out)
}
