package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Error("missing Accept header")
		}
		switch {
		case r.URL.Path == "/users/testuser":
			_, _ = w.Write([]byte(`{"followers":14,"following":12,"public_repos":2}`))
		case r.URL.Path == "/users/testuser/repos":
			_, _ = w.Write([]byte(`[{"name":"repo-a","stargazers_count":5,"forks_count":2},{"name":"repo-b","stargazers_count":9,"forks_count":1,"archived":true}]`))
		case r.URL.Path == "/repos/testuser/monitor/actions/runs":
			_, _ = w.Write([]byte(`{"workflow_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"failure"},{"status":"in_progress","conclusion":null}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	s, err := collect(context.Background(), apiClient{HTTP: server.Client(), Base: server.URL}, "testuser", "testuser/monitor", now)
	if err != nil {
		t.Fatal(err)
	}
	if s.Account.Followers != 14 || s.Metrics.RepositoryCount != 2 || s.Metrics.Stars != 14 || s.Metrics.Archived != 1 {
		t.Fatalf("incorrect totals: %+v", s)
	}
	if s.Metrics.RecentRuns != 2 || s.Metrics.Succeeded != 1 || s.Metrics.Failed != 1 {
		t.Fatalf("incorrect workflow counts: %+v", s.Metrics)
	}
	if s.Metrics.Top[0].Name != "repo-b" {
		t.Fatalf("wrong ordering: %+v", s.Metrics.Top)
	}
}

func TestAPIFailureStopsCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"token permission denied"}`))
	}))
	defer server.Close()
	_, err := collect(context.Background(), apiClient{HTTP: server.Client(), Base: server.URL}, "testuser", "testuser/monitor", time.Now())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected HTTP 403, got %v", err)
	}
}

func TestSaveKeepsHistory(t *testing.T) {
	out := t.TempDir()
	s := snapshot{Time: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), Owner: "testuser"}
	first, err := save(out, s, "100-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(first); err != nil {
		t.Fatal(err)
	}
	s.Time = s.Time.Add(time.Hour)
	if _, err = save(out, s, "101-1"); err != nil {
		t.Fatal(err)
	}
	var history []snapshot
	data, err := os.ReadFile(filepath.Join(out, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || !history[0].Time.Equal(s.Time) {
		t.Fatalf("incorrect history: %+v", history)
	}
	if _, err = save(out, s, "101-1"); err == nil {
		t.Fatal("duplicate snapshot should fail")
	}
}
