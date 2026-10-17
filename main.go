package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxPages = 100

type account struct {
	Followers   int `json:"followers"`
	Following   int `json:"following"`
	PublicRepos int `json:"public_repos"`
}

type repository struct {
	Name     string `json:"name"`
	Stars    int    `json:"stargazers_count"`
	Forks    int    `json:"forks_count"`
	Archived bool   `json:"archived"`
}

type workflowRun struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type actionsResponse struct {
	Runs []workflowRun `json:"workflow_runs"`
}

type topRepository struct {
	Name  string `json:"name"`
	Stars int    `json:"stars"`
	Forks int    `json:"forks"`
}

type metrics struct {
	RepositoryCount int             `json:"repositories"`
	Stars           int             `json:"stars"`
	Forks           int             `json:"forks"`
	Archived        int             `json:"archived"`
	RecentRuns      int             `json:"recent_runs"`
	Succeeded       int             `json:"succeeded"`
	Failed          int             `json:"failed"`
	Cancelled       int             `json:"cancelled"`
	Top             []topRepository `json:"top_repositories"`
}

type snapshot struct {
	Time       time.Time `json:"time"`
	Owner      string    `json:"owner"`
	Repository string    `json:"repository"`
	Account    account   `json:"account"`
	Metrics    metrics   `json:"metrics"`
}

type apiClient struct {
	HTTP  *http.Client
	Base  string
	Token string
}

func (a apiClient) get(ctx context.Context, path string, target any) error {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Base+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "github-devops-monitor")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if a.Token != "" {
			req.Header.Set("Authorization", "Bearer "+a.Token)
		}

		res, err := a.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("GET %s: %w", path, err)
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			defer res.Body.Close()
			if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(target); err != nil {
				return fmt.Errorf("decode %s: %w", path, err)
			}
			return nil
		}
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		res.Body.Close()
		if attempt == 2 || (res.StatusCode != 429 && res.StatusCode < 500 && res.Header.Get("Retry-After") == "") {
			return fmt.Errorf("GET %s: HTTP %d: %s", path, res.StatusCode, strings.TrimSpace(string(msg)))
		}
		wait := time.Duration(1<<attempt) * time.Second
		if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
			wait = time.Duration(seconds) * time.Second
		}
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return errors.New("retries exhausted")
}

func collect(ctx context.Context, client apiClient, owner, repo string, now time.Time) (snapshot, error) {
	s := snapshot{Time: now.UTC(), Owner: owner, Repository: repo}
	if err := client.get(ctx, "/users/"+url.PathEscape(owner), &s.Account); err != nil {
		return s, err
	}

	var repos []repository
	for page := 1; page <= maxPages; page++ {
		var batch []repository
		path := fmt.Sprintf("/users/%s/repos?type=owner&per_page=100&page=%d", url.PathEscape(owner), page)
		if err := client.get(ctx, path, &batch); err != nil {
			return s, err
		}
		repos = append(repos, batch...)
		if len(batch) < 100 {
			break
		}
		if page == maxPages {
			return s, errors.New("repository pagination exceeded 10,000; refusing partial data")
		}
	}

	s.Metrics.RepositoryCount = len(repos)
	for _, r := range repos {
		s.Metrics.Stars += r.Stars
		s.Metrics.Forks += r.Forks
		if r.Archived {
			s.Metrics.Archived++
		}
		s.Metrics.Top = append(s.Metrics.Top, topRepository{r.Name, r.Stars, r.Forks})
	}
	sort.Slice(s.Metrics.Top, func(i, j int) bool {
		if s.Metrics.Top[i].Stars == s.Metrics.Top[j].Stars {
			return s.Metrics.Top[i].Name < s.Metrics.Top[j].Name
		}
		return s.Metrics.Top[i].Stars > s.Metrics.Top[j].Stars
	})
	if len(s.Metrics.Top) > 5 {
		s.Metrics.Top = s.Metrics.Top[:5]
	}

	var runs actionsResponse
	if err := client.get(ctx, "/repos/"+repo+"/actions/runs?per_page=100", &runs); err != nil {
		return s, err
	}
	for _, r := range runs.Runs {
		if r.Status != "completed" {
			continue
		}
		s.Metrics.RecentRuns++
		switch r.Conclusion {
		case "success":
			s.Metrics.Succeeded++
		case "failure", "timed_out", "action_required":
			s.Metrics.Failed++
		case "cancelled":
			s.Metrics.Cancelled++
		}
	}
	return s, nil
}

func save(out string, s snapshot, runID string) (string, error) {
	stamp := s.Time.UTC().Format("2006-01-02T15-04-05.000000000Z")
	if runID != "" {
		stamp += "-" + runID
	}
	path := filepath.Join(out, "history", s.Time.Format("2006-01-02"), stamp+".json")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("snapshot already exists: %s", path)
	}
	if err := writeJSON(path, s); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(out, "latest.json"), s); err != nil {
		return "", err
	}

	indexPath := filepath.Join(out, "index.json")
	var history []snapshot
	data, err := os.ReadFile(indexPath)
	if err == nil {
		if err := json.Unmarshal(data, &history); err != nil {
			return "", fmt.Errorf("read history index: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	history = append([]snapshot{s}, history...)
	if len(history) > 240 {
		history = history[:240]
	}
	if err := writeJSON(indexPath, history); err != nil {
		return "", err
	}
	return path, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	owner := flag.String("owner", envOr("GITHUB_REPOSITORY_OWNER", "K40N45H1Q"), "GitHub account to monitor")
	repo := flag.String("repo", envOr("GITHUB_REPOSITORY", "K40N45H1Q/github-devops-monitor"), "repository for Actions metrics")
	out := flag.String("out", "docs/data", "snapshot directory")
	flag.Parse()
	if !strings.HasPrefix(*repo, *owner+"/") {
		fmt.Fprintln(os.Stderr, "repository must belong to the monitored owner")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := apiClient{HTTP: &http.Client{Timeout: 20 * time.Second}, Base: envOr("GITHUB_API_URL", "https://api.github.com"), Token: os.Getenv("GH_TOKEN")}
	s, err := collect(ctx, client, *owner, *repo, time.Now().UTC())
	if err == nil {
		var path string
		path, err = save(*out, s, envOr("GITHUB_RUN_ID", "")+"-"+envOr("GITHUB_RUN_ATTEMPT", "1"))
		if err == nil {
			fmt.Printf("Saved %s: %d repositories, %d stars, %d completed CI runs\n", path, s.Metrics.RepositoryCount, s.Metrics.Stars, s.Metrics.RecentRuns)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
