// Package session makes plain HTTP requests to animepahe using the cookie and user agent
// FlareSolverr obtained, and re-solves once when Cloudflare challenges again.
package session

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"pahe-arr/internal/cf"
)

// Session holds the Cloudflare clearance for one animepahe host.
type Session struct {
	Host   string
	solver *cf.Client
	client *http.Client

	mu      sync.Mutex
	ua      string
	cookies []*http.Cookie
}

func New(host string, solver *cf.Client) *Session {
	return &Session{Host: host, solver: solver, client: &http.Client{Timeout: 60 * time.Second}}
}

// UserAgent is the user agent FlareSolverr used. The clearance cookie only works with it.
func (s *Session) UserAgent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ua
}

// Solve gets a fresh clearance from FlareSolverr.
func (s *Session) Solve(ctx context.Context) (*cf.Solution, error) {
	sol, err := s.solver.Solve(ctx, "https://"+s.Host+"/")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.ua, s.cookies = sol.UserAgent, sol.Cookies
	s.mu.Unlock()
	return sol, nil
}

// ChallengeError means Cloudflare still blocks the request after a fresh clearance.
type ChallengeError struct {
	URL    string
	Status int
}

func (e *ChallengeError) Error() string {
	return fmt.Sprintf("Cloudflare still blocks %s (HTTP %d) with a fresh cookie", e.URL, e.Status)
}

// Get fetches an animepahe URL. On a Cloudflare challenge it re-solves once and retries.
func (s *Session) Get(ctx context.Context, rawURL string) ([]byte, error) {
	if s.UserAgent() == "" {
		if _, err := s.Solve(ctx); err != nil {
			return nil, err
		}
	}
	body, status, err := s.do(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if IsChallenge(status, body) {
		if _, err := s.Solve(ctx); err != nil {
			return nil, err
		}
		if body, status, err = s.do(ctx, rawURL); err != nil {
			return nil, err
		}
		if IsChallenge(status, body) {
			return nil, &ChallengeError{URL: rawURL, Status: status}
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", rawURL, status)
	}
	return body, nil
}

func (s *Session) do(ctx context.Context, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	ua, cookies := s.ua, s.cookies
	s.mu.Unlock()

	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://"+s.Host+"/")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	res, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	return body, res.StatusCode, err
}

var challengeRe = regexp.MustCompile(`(?i)cf-browser-verification|Just a moment|cf_chl_opt|Attention Required`)

// IsChallenge reports whether a response is a Cloudflare interstitial rather than real content.
func IsChallenge(status int, body []byte) bool {
	if status != http.StatusForbidden && status != http.StatusServiceUnavailable {
		return false
	}
	return challengeRe.Match(body[:min(len(body), 64<<10)])
}
