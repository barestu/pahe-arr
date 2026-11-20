package jobs

import (
	"testing"

	"pahe-arr/internal/config"
)

func newManager(t *testing.T, dir string) *Manager {
	t.Helper()
	st, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir, st, NewClients())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestQueueLifecycle(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, dir)
	base := Job{AnimeSession: "a", AnimeTitle: "Death Note", Quality: 1080, Audio: "jpn"}
	j1, j2 := base, base
	j1.EpisodeSession, j1.EpisodeNumber = "e1", "1"
	j2.EpisodeSession, j2.EpisodeNumber = "e2", "2"
	if n := m.Add(j1, j2); n != 2 {
		t.Fatalf("added %d", n)
	}
	if n := m.Add(j1); n != 0 {
		t.Fatal("duplicate pending job should be skipped")
	}
	list := m.List()
	if len(list) != 2 || list[0].EpisodeNumber != "1" || list[0].Status != Queued {
		t.Fatalf("list %+v", list)
	}

	// Simulate a crash mid-download: the claimed job goes back to queued on restart.
	claimed, _ := m.next()
	if claimed == nil || claimed.EpisodeNumber != "1" || claimed.Status != Resolving {
		t.Fatalf("claimed %+v", claimed)
	}
	m2 := newManager(t, dir)
	for _, j := range m2.List() {
		if j.Status != Queued {
			t.Fatalf("after restart %s is %s", j.EpisodeNumber, j.Status)
		}
	}

	id := m2.List()[1].ID
	m2.Cancel(id)
	if got := find(m2.List(), id); got.Status != Canceled {
		t.Fatalf("cancel queued: %s", got.Status)
	}
	m2.Retry(id)
	if got := find(m2.List(), id); got.Status != Queued {
		t.Fatalf("retry: %s", got.Status)
	}
	m2.Cancel(id)
	if n := m2.ClearFinished(); n != 1 {
		t.Fatalf("cleared %d", n)
	}
	m2.Remove(m2.List()[0].ID)
	if len(m2.List()) != 0 {
		t.Fatalf("remove: %+v", m2.List())
	}
}

func find(js []Job, id string) Job {
	for _, j := range js {
		if j.ID == id {
			return j
		}
	}
	return Job{}
}

func TestPercent(t *testing.T) {
	if (Job{Status: Downloading, Done: 30, Total: 120}).Percent() != 25 {
		t.Error("25%")
	}
	if (Job{Status: Done}).Percent() != 100 {
		t.Error("done = 100%")
	}
}
