package grab

import (
	"testing"

	"pahe-arr/internal/pahe"
)

func TestLabel(t *testing.T) {
	w := LabelWidth([]pahe.Episode{{Number: "1"}, {Number: "105"}})
	if got := Label("5", w); got != "005" {
		t.Errorf("got %q", got)
	}
	if got := Label("5", 2); got != "05" {
		t.Errorf("got %q", got)
	}
	if got := Label("12.5", 2); got != "12.5" {
		t.Errorf("got %q", got)
	}
}

func TestSafeName(t *testing.T) {
	if got := SafeName(`Re:Zero / "Season" 2?`); got != "Re Zero Season 2" {
		t.Errorf("got %q", got)
	}
	if got := SafeName("..."); got != "Unknown" {
		t.Errorf("got %q", got)
	}
}

func TestFileName(t *testing.T) {
	if got := FileName("Death Note", "02", 1080, "jpn"); got != "[AnimePahe] Death Note - 02 [1080p].mp4" {
		t.Errorf("got %q", got)
	}
	if got := FileName("X", "01", 720, "eng"); got != "[AnimePahe] X - 01 [720p] [Dub].mp4" {
		t.Errorf("got %q", got)
	}
}
