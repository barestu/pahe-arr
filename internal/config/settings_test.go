package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDomain(t *testing.T) {
	for in, want := range map[string]string{
		"animepahe.pw":                         "animepahe.pw",
		" https://AnimePahe.pw/ ":              "animepahe.pw",
		"https://animepahe.pw/anime/abc?x=1#y": "animepahe.pw",
		"http://localhost:8443/":               "localhost:8443",
	} {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q want %q", in, got, want)
		}
	}
}

func TestRelativeOutputDefault(t *testing.T) {
	t.Setenv("PAHE_OUTPUT_DIR", "./tmp/downloads")
	want, err := filepath.Abs(filepath.Join("tmp", "downloads"))
	if err != nil {
		t.Fatal(err)
	}
	if got := Defaults().OutputDir; got != want {
		t.Fatalf("output directory = %q, want %q", got, want)
	}
}

func TestStoreRoundTripAndDefaults(t *testing.T) {
	t.Setenv("PAHE_DOMAIN", "https://animepahe.pw/")
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Get()
	if s.PaheDomain != "animepahe.pw" || s.OutputDir != "/DATA/.manual-import" || s.Concurrency != 3 || !s.DefaultAutoImport {
		t.Fatalf("defaults: %+v", s)
	}
	s.SonarrURL, s.SonarrAPIKey, s.Concurrency, s.DefaultAudio = "http://x:8989/", "k", 9, "fr"
	if err := st.Set(s); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(dir, "settings.json"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("settings.json should be private, got %v", info.Mode().Perm())
	}
	st2, _ := Open(dir)
	got := st2.Get()
	if got.SonarrURL != "http://x:8989" || got.Concurrency != 4 || got.DefaultAudio != "jpn" || !got.SonarrConfigured() {
		t.Fatalf("reloaded: %+v", got)
	}
}

func TestSonarrPath(t *testing.T) {
	s := Settings{OutputDir: "/DATA/.manual-import"}
	if got := s.SonarrPath("/DATA/.manual-import/X/a.mp4"); got != "/DATA/.manual-import/X/a.mp4" {
		t.Errorf("same mount: %s", got)
	}
	s.SonarrOutputDir = "/media/import"
	if got := s.SonarrPath("/DATA/.manual-import/X/a.mp4"); got != "/media/import/X/a.mp4" {
		t.Errorf("mapped: %s", got)
	}
}
