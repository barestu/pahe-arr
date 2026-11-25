package cmd_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCommandVersions(t *testing.T) {
	for command, prefix := range map[string]string{"volarr": "Volarr", "pahe": "pahe-arr"} {
		t.Run(command, func(t *testing.T) {
			flags := "-X pahe-arr/internal/buildinfo.Version=v1.2.3 -X pahe-arr/internal/buildinfo.Commit=abcdef"
			out, err := exec.Command("go", "run", "-ldflags", flags, "./"+command, "version").CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != prefix+" v1.2.3 (abcdef)" {
				t.Fatalf("version returned %q, error %v", out, err)
			}
		})
	}
}
