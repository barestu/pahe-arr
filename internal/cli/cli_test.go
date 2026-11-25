package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	for name, newline := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			const key = "PAHE_TEST_DOTENV_VALUE"
			t.Setenv(key, "restore after test")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), ".env")
			content := strings.Join([]string{"# local configuration", "", key + `="folder with spaces"`, ""}, newline)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := loadEnvFile(path); err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv(key); got != "folder with spaces" {
				t.Fatalf("loaded value = %q", got)
			}
			t.Setenv(key, "process override")
			if err := loadEnvFile(path); err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv(key); got != "process override" {
				t.Fatalf("process environment overwritten: %q", got)
			}
		})
	}
	if err := loadEnvFile(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatalf("missing .env should be optional: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PAHE_TEST_INVALID=\"unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path); err == nil {
		t.Fatal("invalid .env should report an error")
	}
}

func TestParseSelection(t *testing.T) {
	sel, err := parseSelection("1-3, 5, 12.5")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"1", "2", "3", "5", "12.5"} {
		if !sel(n) {
			t.Errorf("%s should be selected", n)
		}
	}
	for _, n := range []string{"4", "6", "12", "abc"} {
		if sel(n) {
			t.Errorf("%s should not be selected", n)
		}
	}
	if _, err := parseSelection("5-2"); err == nil {
		t.Error("reversed range should fail")
	}
	all, _ := parseSelection("all")
	if !all("999") {
		t.Error("all should select everything")
	}
}
