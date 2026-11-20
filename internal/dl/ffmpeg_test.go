package dl

import (
	"strings"
	"testing"
)

func TestRemuxArgs(t *testing.T) {
	joined := strings.Join(RemuxArgs("/tmp/in.ts", "/out/ep.mp4"), " ")
	for _, want := range []string{"-i /tmp/in.ts", "-c copy", "-f mp4 /out/ep.mp4.part"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "http") {
		t.Error("remux must not touch the network")
	}
}
