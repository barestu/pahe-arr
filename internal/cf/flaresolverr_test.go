package cf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" {
			t.Errorf("path %s", r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["cmd"] != "request.get" {
			t.Errorf("cmd %v", req["cmd"])
		}
		_, _ = w.Write([]byte(`{"status":"ok","message":"Challenge solved!","solution":{"url":"https://x/","status":200,"userAgent":"UA-1","cookies":[{"name":"cf_clearance","value":"secret"}]}}`))
	}))
	defer srv.Close()

	sol, err := New(srv.URL).Solve(context.Background(), "https://x/")
	if err != nil {
		t.Fatal(err)
	}
	if sol.UserAgent != "UA-1" || len(sol.Cookies) != 1 || sol.CookieNames()[0] != "cf_clearance" {
		t.Fatalf("%+v", sol)
	}
}

func TestSolveError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","message":"Challenge not detected"}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL+"/v1").Solve(context.Background(), "https://x/"); err == nil {
		t.Fatal("expected error")
	}
}
