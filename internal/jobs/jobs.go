// Package jobs is the download queue: jobs persist to <config>/jobs.json, one runs at a time,
// failures retry twice with backoff, and finished downloads are handed to Sonarr when asked.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"pahe-arr/internal/cf"
	"pahe-arr/internal/config"
	"pahe-arr/internal/grab"
	"pahe-arr/internal/pahe"
	"pahe-arr/internal/session"
	"pahe-arr/internal/sonarr"
)

type Status string

const (
	Queued      Status = "queued"
	Resolving   Status = "resolving"
	Downloading Status = "downloading"
	Remuxing    Status = "remuxing"
	Importing   Status = "importing"
	Done        Status = "done"
	Failed      Status = "failed"
	Canceled    Status = "canceled"
)

// MaxAutoRetries is how many times a failed download is retried before giving up.
const MaxAutoRetries = 2

func (s Status) Active() bool {
	return s == Resolving || s == Downloading || s == Remuxing || s == Importing
}

func (s Status) Finished() bool { return s == Done || s == Failed || s == Canceled }

type Job struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	AnimeSession   string `json:"animeSession"`
	AnimeTitle     string `json:"animeTitle"`
	EpisodeSession string `json:"episodeSession"`
	EpisodeNumber  string `json:"episodeNumber"`
	EpisodeLabel   string `json:"episodeLabel"`

	Quality int    `json:"quality"`
	Audio   string `json:"audio"`

	ReleaseTitle      string `json:"releaseTitle,omitempty"` // Present on Sonarr-managed downloads.
	Import            bool   `json:"import"`
	SonarrSeriesID    int    `json:"sonarrSeriesId,omitempty"`
	SonarrSeriesTitle string `json:"sonarrSeriesTitle,omitempty"`
	SonarrSeason      int    `json:"sonarrSeason,omitempty"` // 0 = absolute numbering
	SonarrOffset      int    `json:"sonarrOffset,omitempty"`

	Status    Status    `json:"status"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	Attempts  int       `json:"attempts"`
	NotBefore time.Time `json:"notBefore,omitempty"`
	Error     string    `json:"error,omitempty"`
	Warning   string    `json:"warning,omitempty"`
	Output    string    `json:"output,omitempty"`
	Via       string    `json:"via,omitempty"`
	GotQual   int       `json:"gotQuality,omitempty"`
}

// Percent is download progress for the UI.
func (j Job) Percent() int {
	switch {
	case j.Status == Done:
		return 100
	case j.Total > 0:
		return j.Done * 100 / j.Total
	}
	return 0
}

// Clients caches one animepahe client (and its Cloudflare clearance) per domain + solver.
type Clients struct {
	mu sync.Mutex
	m  map[string]*pahe.Client
}

func NewClients() *Clients { return &Clients{m: map[string]*pahe.Client{}} }

func (c *Clients) For(s config.Settings) *pahe.Client {
	key := s.PaheDomain + "|" + s.FlareSolverrURL
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.m[key]; ok {
		delay := time.Duration(s.DelaySeconds) * time.Second
		if cl.Delay != delay {
			cl = &pahe.Client{S: cl.S, Delay: delay}
			c.m[key] = cl
		}
		return cl
	}
	cl := &pahe.Client{
		S:     session.New(s.PaheDomain, cf.New(s.FlareSolverrURL)),
		Delay: time.Duration(s.DelaySeconds) * time.Second,
	}
	c.m[key] = cl
	return cl
}

// Reset drops cached clients, e.g. after the domain or FlareSolverr URL changes.
func (c *Clients) Reset() {
	c.mu.Lock()
	c.m = map[string]*pahe.Client{}
	c.mu.Unlock()
}

type Manager struct {
	path     string
	settings *config.Store
	clients  *Clients

	mu      sync.Mutex
	jobs    []*Job
	cancels map[string]context.CancelFunc
	wake    chan struct{}

	// now and retryDelay are replaceable in tests.
	now        func() time.Time
	retryDelay func(attempt int) time.Duration
}

func NewManager(dir string, settings *config.Store, clients *Clients) (*Manager, error) {
	m := &Manager{
		path:       filepath.Join(dir, "jobs.json"),
		settings:   settings,
		clients:    clients,
		cancels:    map[string]context.CancelFunc{},
		wake:       make(chan struct{}, 1),
		now:        time.Now,
		retryDelay: func(a int) time.Duration { return time.Duration(30*a) * time.Second },
	}
	data, err := os.ReadFile(m.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &m.jobs); err != nil {
			return nil, fmt.Errorf("jobs.json: %w", err)
		}
	}
	// Anything interrupted by a restart goes back into the queue.
	for _, j := range m.jobs {
		if j.Status.Active() {
			j.Status, j.Done, j.Total = Queued, 0, 0
		}
	}
	return m, m.saveLocked()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) saveLocked() error {
	return config.WriteJSONAtomic(m.path, m.jobs, 0o644)
}

func (m *Manager) save() {
	if err := m.saveLocked(); err != nil {
		log.Printf("saving jobs: %v", err)
	}
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Add queues jobs and returns how many were added (duplicates of pending jobs are skipped).
func (m *Manager) Add(js ...Job) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	added := 0
	for _, j := range js {
		if m.pendingDuplicateLocked(j) {
			continue
		}
		j.ID = newID()
		j.CreatedAt, j.UpdatedAt = m.now(), m.now()
		j.Status = Queued
		jj := j
		m.jobs = append(m.jobs, &jj)
		added++
	}
	m.save()
	m.signal()
	return added
}

// AddRelease returns the persisted download ID Sonarr tracks through queue and history.
func (m *Manager) AddRelease(j Job) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.jobs {
		if existing.ReleaseTitle == j.ReleaseTitle && existing.EpisodeSession == j.EpisodeSession && !existing.Status.Finished() {
			return existing.ID, nil
		}
	}
	j.ID = newID()
	j.CreatedAt = m.now()
	j.UpdatedAt = j.CreatedAt
	j.Status = Queued
	m.jobs = append(m.jobs, &j)
	if err := m.saveLocked(); err != nil {
		m.jobs = m.jobs[:len(m.jobs)-1]
		return "", err
	}
	m.signal()
	return j.ID, nil
}

func (m *Manager) pendingDuplicateLocked(j Job) bool {
	for _, e := range m.jobs {
		if !e.Status.Finished() && e.EpisodeSession == j.EpisodeSession && e.Audio == j.Audio && e.Quality == j.Quality {
			return true
		}
	}
	return false
}

// List returns copies: running and queued first (queue order), then finished, newest first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	rank := func(j Job) int {
		switch {
		case j.Status.Active():
			return 0
		case j.Status == Queued:
			return 1
		}
		return 2
	}
	sort.SliceStable(out, func(a, b int) bool {
		ra, rb := rank(out[a]), rank(out[b])
		if ra != rb {
			return ra < rb
		}
		if ra == 2 {
			return out[a].UpdatedAt.After(out[b].UpdatedAt)
		}
		return out[a].CreatedAt.Before(out[b].CreatedAt)
	})
	return out
}

func (m *Manager) find(id string) *Job {
	for _, j := range m.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.find(id)
	if j == nil || j.Status.Finished() {
		return
	}
	if cancel, ok := m.cancels[id]; ok {
		cancel() // the worker marks it canceled when the download stops
		return
	}
	j.Status, j.UpdatedAt = Canceled, m.now()
	m.save()
}

func (m *Manager) Retry(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.find(id)
	if j == nil || !(j.Status == Failed || j.Status == Canceled) {
		return
	}
	j.Status, j.Error, j.Warning = Queued, "", ""
	j.Done, j.Total, j.Attempts, j.NotBefore = 0, 0, 0, time.Time{}
	j.UpdatedAt = m.now()
	m.save()
	m.signal()
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, j := range m.jobs {
		if j.ID == id && !j.Status.Active() {
			m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
			m.save()
			return
		}
	}
}

// RemoveRelease cancels and forgets only jobs owned by the Sonarr download client.
func (m *Manager) RemoveRelease(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, j := range m.jobs {
		if j.ID != id || j.ReleaseTitle == "" {
			continue
		}
		if cancel := m.cancels[id]; cancel != nil {
			cancel()
		}
		m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
		m.save()
		return
	}
}

// ClearFinished removes completed and canceled jobs (failed ones stay until removed or retried).
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.jobs[:0]
	n := 0
	for _, j := range m.jobs {
		if j.ReleaseTitle == "" && (j.Status == Done || j.Status == Canceled) {
			n++
			continue
		}
		kept = append(kept, j)
	}
	m.jobs = kept
	m.save()
	return n
}

// Run processes the queue until ctx is canceled.
func (m *Manager) Run(ctx context.Context) {
	for {
		j, wait := m.next()
		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
			case <-time.After(wait):
			}
			continue
		}
		m.process(ctx, j)
		if ctx.Err() != nil {
			return
		}
		if d := time.Duration(m.settings.Get().DelaySeconds) * time.Second; d > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		}
	}
}

// next claims the first runnable queued job, or says how long until one becomes runnable.
func (m *Manager) next() (*Job, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wait := time.Minute
	now := m.now()
	for _, j := range m.jobs {
		if j.Status != Queued {
			continue
		}
		if j.NotBefore.After(now) {
			wait = min(wait, j.NotBefore.Sub(now))
			continue
		}
		j.Status, j.UpdatedAt = Resolving, now
		m.save()
		return j, 0
	}
	return nil, wait
}

func (m *Manager) update(j *Job, f func(*Job)) {
	m.mu.Lock()
	f(j)
	j.UpdatedAt = m.now()
	m.mu.Unlock()
}

func (m *Manager) process(parent context.Context, j *Job) {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	if m.find(j.ID) != j || j.Status == Canceled {
		m.mu.Unlock()
		cancel()
		return
	}
	m.cancels[j.ID] = cancel
	snap := *j
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.cancels, j.ID)
		m.save()
		m.mu.Unlock()
	}()

	s := m.settings.Get()
	client := m.clients.For(s)
	ep := pahe.Episode{AnimeSession: snap.AnimeSession, Session: snap.EpisodeSession, Number: snap.EpisodeNumber}
	opts := grab.Options{
		OutputDir:   s.OutputDir,
		Title:       snap.AnimeTitle,
		Label:       snap.EpisodeLabel,
		Quality:     snap.Quality,
		Audio:       snap.Audio,
		Concurrency: s.Concurrency,
		Exact:       snap.ReleaseTitle != "",
	}
	res, err := grab.Episode(ctx, client, ep, opts, func(stage grab.Stage, done, total int) {
		m.update(j, func(j *Job) {
			switch stage {
			case grab.Resolving:
				j.Status = Resolving
			case grab.Downloading:
				j.Status, j.Done, j.Total = Downloading, done, total
			case grab.Remuxing:
				j.Status = Remuxing
			}
		})
	})
	if err != nil {
		m.fail(ctx, parent, j, err, true)
		return
	}
	m.update(j, func(j *Job) {
		j.Output, j.Via, j.GotQual = res.Path, res.Via, res.Quality
		if res.Skipped {
			j.Warning = "file already existed, download skipped"
		}
	})

	if snap.Import && snap.SonarrSeriesID > 0 {
		m.update(j, func(j *Job) { j.Status = Importing })
		if err := m.importToSonarr(ctx, s, snap, res.Path); err != nil {
			// Downloaded fine; only the hand-off failed. No auto retry: the file stays in staging.
			m.fail(ctx, parent, j, fmt.Errorf("downloaded, but Sonarr import failed: %w", err), false)
			return
		}
	}
	m.update(j, func(j *Job) { j.Status, j.Error = Done, "" })
}

func (m *Manager) fail(ctx, parent context.Context, j *Job, err error, retryable bool) {
	m.update(j, func(j *Job) {
		switch {
		case parent.Err() != nil: // shutting down: pick it up again on next start
			j.Status = Queued
		case ctx.Err() != nil: // canceled by the user
			j.Status, j.Error = Canceled, ""
		case retryable && j.Attempts < MaxAutoRetries:
			j.Error = err.Error()
			j.Attempts++
			j.Status = Queued
			j.NotBefore = m.now().Add(m.retryDelay(j.Attempts))
		default:
			j.Error = err.Error()
			j.Status = Failed
		}
	})
}

func (m *Manager) importToSonarr(ctx context.Context, s config.Settings, j Job, local string) error {
	if !s.SonarrConfigured() {
		return errors.New("Sonarr is not configured in Settings")
	}
	cl := sonarr.New(s.SonarrURL, s.SonarrAPIKey)
	eps, err := cl.Episodes(ctx, j.SonarrSeriesID)
	if err != nil {
		return err
	}
	target, err := sonarr.FindEpisode(eps, j.EpisodeNumber, sonarr.Mapping{Season: j.SonarrSeason, Offset: j.SonarrOffset})
	if err != nil {
		return err
	}
	ierr := cl.ImportFile(ctx, s.SonarrPath(local), j.SonarrSeriesID, target.ID)
	var rej *sonarr.RejectedError
	if ierr != nil && !errors.As(ierr, &rej) {
		return ierr
	}
	if _, err := os.Stat(local); err == nil {
		if rej != nil {
			return rej
		}
		return errors.New("Sonarr finished but left the file in place; check Sonarr → System → Logs")
	}
	if rej != nil {
		m.mu.Lock()
		if live := m.find(j.ID); live != nil {
			live.Warning = "imported despite: " + rej.Error()
		}
		m.mu.Unlock()
	}
	return nil
}
