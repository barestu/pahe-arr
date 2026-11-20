// Package cf asks FlareSolverr to pass a Cloudflare challenge and returns the
// resulting cookies and user agent, so plain HTTP requests can reuse them.
package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Solution is what FlareSolverr hands back after solving a challenge.
type Solution struct {
	URL       string
	Status    int
	UserAgent string
	Cookies   []*http.Cookie
}

// CookieNames lists cookie names only. Values are session credentials and must not be logged.
func (s *Solution) CookieNames() []string {
	names := make([]string, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		names = append(names, c.Name)
	}
	return names
}

// Client talks to the FlareSolverr v1 API.
type Client struct {
	endpoint   string
	httpClient *http.Client
	maxTimeout time.Duration
}

// New accepts either http://host:8191 or http://host:8191/v1.
func New(endpoint string) *Client {
	e := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if !strings.HasSuffix(e, "/v1") {
		e += "/v1"
	}
	return &Client{
		endpoint:   e,
		httpClient: &http.Client{Timeout: 90 * time.Second},
		maxTimeout: 60 * time.Second,
	}
}

type request struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type response struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL       string `json:"url"`
		Status    int    `json:"status"`
		UserAgent string `json:"userAgent"`
		Cookies   []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	} `json:"solution"`
}

// Solve opens target in FlareSolverr's browser and returns the cookies and user agent it ended up with.
func (c *Client) Solve(ctx context.Context, target string) (*Solution, error) {
	body, err := json.Marshal(request{Cmd: "request.get", URL: target, MaxTimeout: int(c.maxTimeout / time.Millisecond)})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FlareSolverr unreachable at %s: %w", c.endpoint, err)
	}
	defer res.Body.Close()

	var r response
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("FlareSolverr returned an unreadable response (HTTP %d): %w", res.StatusCode, err)
	}
	if r.Status != "ok" {
		return nil, fmt.Errorf("FlareSolverr could not solve %s: %s", target, r.Message)
	}
	if r.Solution.UserAgent == "" {
		return nil, errors.New("FlareSolverr returned no user agent")
	}

	s := &Solution{URL: r.Solution.URL, Status: r.Solution.Status, UserAgent: r.Solution.UserAgent}
	for _, ck := range r.Solution.Cookies {
		s.Cookies = append(s.Cookies, &http.Cookie{Name: ck.Name, Value: ck.Value, Path: "/"})
	}
	return s, nil
}
