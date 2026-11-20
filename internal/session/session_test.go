package session

import "testing"

func TestIsChallenge(t *testing.T) {
	if !IsChallenge(403, []byte("<title>Just a moment...</title>")) {
		t.Error("403 interstitial should be a challenge")
	}
	if IsChallenge(200, []byte("Just a moment")) {
		t.Error("200 is never a challenge")
	}
	if IsChallenge(403, []byte(`{"error":"forbidden"}`)) {
		t.Error("plain 403 without markers is not a challenge")
	}
}
