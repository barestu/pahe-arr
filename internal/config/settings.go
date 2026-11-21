// Package config holds the app settings, editable from the web UI and stored as JSON in the
// config directory. Environment variables only seed the first run.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Settings struct {
	PaheDomain      string `json:"paheDomain"`
	FlareSolverrURL string `json:"flareSolverrUrl"`
	OutputDir       string `json:"outputDir"`

	SonarrURL       string `json:"sonarrUrl"`
	SonarrAPIKey    string `json:"sonarrApiKey"`
	SonarrOutputDir string `json:"sonarrOutputDir"` // output dir as Sonarr sees it; empty = same path

	DefaultQuality    int    `json:"defaultQuality"`
	DefaultAudio      string `json:"defaultAudio"`
	DefaultAutoImport bool   `json:"defaultAutoImport"`
	Concurrency       int    `json:"concurrency"`
	DelaySeconds      int    `json:"delaySeconds"`
}

// Defaults seeds a first run from environment variables.
func Defaults() Settings {
	s := Settings{
		PaheDomain:        os.Getenv("PAHE_DOMAIN"),
		FlareSolverrURL:   os.Getenv("FLARESOLVERR_URL"),
		OutputDir:         envOr("PAHE_OUTPUT_DIR", "/DATA/.manual-import"),
		SonarrURL:         os.Getenv("SONARR_URL"),
		SonarrAPIKey:      os.Getenv("SONARR_API_KEY"),
		DefaultQuality:    1080,
		DefaultAudio:      "jpn",
		DefaultAutoImport: true,
		Concurrency:       3,
		DelaySeconds:      1,
	}
	if v, err := strconv.Atoi(os.Getenv("PAHE_DELAY_SECONDS")); err == nil {
		s.DelaySeconds = v
	}
	// Local .env files can use portable relative paths. Store an absolute path
	// so the web settings form and Sonarr receive an unambiguous location.
	if output := strings.TrimSpace(os.Getenv("PAHE_OUTPUT_DIR")); output != "" {
		if absolute, err := filepath.Abs(output); err == nil {
			s.OutputDir = absolute
		}
	}
	return s.Normalize()
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// NormalizeDomain accepts "animepahe.pw", "https://animepahe.pw/" or a full page URL.
func NormalizeDomain(d string) string {
	d = strings.TrimSpace(d)
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	return strings.ToLower(d)
}

func (s Settings) Normalize() Settings {
	s.PaheDomain = NormalizeDomain(s.PaheDomain)
	s.FlareSolverrURL = strings.TrimRight(strings.TrimSpace(s.FlareSolverrURL), "/")
	s.OutputDir = filepath.Clean(strings.TrimSpace(s.OutputDir))
	s.SonarrURL = strings.TrimRight(strings.TrimSpace(s.SonarrURL), "/")
	s.SonarrAPIKey = strings.TrimSpace(s.SonarrAPIKey)
	if d := strings.TrimSpace(s.SonarrOutputDir); d != "" {
		s.SonarrOutputDir = filepath.Clean(d)
	} else {
		s.SonarrOutputDir = ""
	}
	if s.DefaultQuality <= 0 {
		s.DefaultQuality = 1080
	}
	if s.DefaultAudio != "eng" {
		s.DefaultAudio = "jpn"
	}
	s.Concurrency = max(1, min(s.Concurrency, 4))
	s.DelaySeconds = max(0, min(s.DelaySeconds, 60))
	return s
}

// SonarrConfigured reports whether imports can be handed to Sonarr.
func (s Settings) SonarrConfigured() bool { return s.SonarrURL != "" && s.SonarrAPIKey != "" }

// SonarrPath maps a path under OutputDir to the same file as Sonarr sees it.
func (s Settings) SonarrPath(local string) string {
	if s.SonarrOutputDir == "" || s.SonarrOutputDir == s.OutputDir {
		return local
	}
	rel, err := filepath.Rel(s.OutputDir, local)
	if err != nil || strings.HasPrefix(rel, "..") {
		return local
	}
	return filepath.Join(s.SonarrOutputDir, rel)
}

// Store keeps the settings in memory and persists them to <dir>/settings.json.
type Store struct {
	path string
	mu   sync.RWMutex
	s    Settings
}

// Open loads settings.json, filling any missing fields from Defaults, and creates it if absent.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	st := &Store{path: filepath.Join(dir, "settings.json")}
	s, found, err := Load(dir)
	if err != nil {
		return nil, err
	}
	st.s = s
	if !found {
		if err := WriteJSONAtomic(st.path, s, 0o600); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// Load reads settings.json without creating it. found=false means defaults were returned.
func Load(dir string) (Settings, bool, error) {
	s := Defaults()
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, false, fmt.Errorf("settings.json: %w", err)
	}
	return s.Normalize(), true, nil
}

func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

func (st *Store) Set(s Settings) error {
	s = s.Normalize()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := WriteJSONAtomic(st.path, s, 0o600); err != nil {
		return err
	}
	st.s = s
	return nil
}

// WriteJSONAtomic writes to a temp file in the same directory and renames it into place,
// so a crash never leaves a half-written file.
func WriteJSONAtomic(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
