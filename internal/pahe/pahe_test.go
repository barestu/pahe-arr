package pahe

import "testing"

const playHTML = `<div id="resolutionMenu">
<button class="dropdown-item" data-src="https://kwik.si/e/AAA" data-fansub="SubsPlease" data-resolution="360" data-audio="jpn">SubsPlease · 360p</button>
<button class="dropdown-item" data-src="https://kwik.si/e/BBB" data-fansub="SubsPlease" data-resolution="720" data-audio="jpn">SubsPlease · 720p</button>
<button class="dropdown-item" data-src="https://kwik.cx/e/CCC" data-fansub="SubsPlease" data-resolution="1080p" data-audio="jpn">SubsPlease · 1080p</button>
<button class="dropdown-item" data-src="https://kwik.cx/e/DDD" data-fansub="Dub" data-resolution="720" data-audio="eng">Dub · 720p</button>
<button class="other" data-src="https://example.com/x" data-resolution="720">not kwik</button>
</div>`

func TestParsePlaySources(t *testing.T) {
	s := ParsePlaySources(playHTML)
	if len(s) != 4 {
		t.Fatalf("got %d sources: %+v", len(s), s)
	}
	if s[2].Quality != 1080 || s[2].KwikURL != "https://kwik.cx/e/CCC" {
		t.Fatalf("1080p source wrong: %+v", s[2])
	}
	if s[3].Audio != "eng" {
		t.Fatalf("dub not detected: %+v", s[3])
	}
}

func TestPickSource(t *testing.T) {
	s := ParsePlaySources(playHTML)
	if got, _ := PickSource(s, 1080, "jpn"); got.KwikURL != "https://kwik.cx/e/CCC" {
		t.Errorf("exact pick: %+v", got)
	}
	if got, _ := PickSource(s, 480, "jpn"); got.Quality != 360 {
		t.Errorf("nearest pick for 480 should be 360 (first of equal distance): %+v", got)
	}
	if got, _ := PickSource(s, 1080, "eng"); got.KwikURL != "https://kwik.cx/e/DDD" {
		t.Errorf("dub pick: %+v", got)
	}
	if _, ok := PickSource(nil, 720, "jpn"); ok {
		t.Error("empty list should return ok=false")
	}
}

func TestCleanTitle(t *testing.T) {
	cases := map[string]string{
		"Death Note anime info":           "Death Note",
		"Death Note Ep. 1-37 [Completed]": "Death Note",
		"One Piece [Airing]":              "One Piece",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q want %q", in, got, want)
		}
	}
}
