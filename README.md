# GitHub DevOps Monitor

A self-hosted-in-GitHub monitoring pipeline written in Go. It collects public account and repository metrics, samples CI results, stores timestamped JSON snapshots, and optionally publishes a static dashboard. No server, database, personal access token or external Go modules required.

## Quick start

1. Open **Actions → Monitor → Run workflow** on the default `main` branch.
2. The workflow runs formatting, `go vet`, tests, a build and the monitor, then commits the new snapshot to `docs/data/`.
3. Look for the new `data: record GitHub metrics (...) [automated]` commit and inspect `docs/data/latest.json`.

Runs are also scheduled at **01:17, 07:17, 13:17 and 19:17 UTC**, subject to GitHub Actions scheduling delays. Every run is a new measurement, even when metric values remain unchanged. All changes are recorded transparently as automated telemetry.

If commit pushes fail, check **Settings → Actions → General → Workflow permissions** and branch protection. The workflow requests `contents: write` and `actions: read` from the built-in `GITHUB_TOKEN`. No custom secret is needed.

## Metrics

- Followers, following and public repository count.
- Total public repository stars, forks, archived repository count and top five starred repositories.
- Successes, failures and cancellations among the most recent **100 workflow runs** in this repository, excluding in-progress runs.
- Latest snapshot, individual timestamped snapshots and a 240-observation rolling dashboard index.

Only public data and workflow metadata for this repository are collected. The public-repository list is paginated, and API errors stop the operation instead of silently writing incomplete results.

## Dashboard

To enable public deployment:

1. In **Settings → Pages**, choose **GitHub Actions** as the source.
2. Run **Actions → Publish dashboard → Run workflow** once. Every subsequent successful Monitor workflow automatically deploys updated metrics.

The site is served from `docs/` and uses only HTML, CSS and JavaScript. It reads the generated JSON without a build step.

## CI and container release

- **CI** (`ci.yml`): checks formatting, `go vet`, race-enabled tests and builds on code changes, pull requests or manual runs.
- **Monitor** (`monitor.yml`): runs four times daily or manually, checks the code, collects metrics and commits data.
- **Publish dashboard** (`pages.yml`): optional GitHub Pages deployment, enabled with a repository variable.
- **Release container** (`release.yml`): publishes a minimal Go container to GHCR when you push a version tag such as `v0.1.0`, or when manually dispatched.

For local use:

```bash
GH_TOKEN=... go run . -owner K40N45H1Q -repo K40N45H1Q/github-devops-monitor
```

Public GitHub API endpoints can also be read without a token, subject to tighter rate limits. The Docker image accepts `-out /data` with a writable volume.

## Contributions

The Monitor workflow configures Git author `K40N45H1Q` and the account's expected GitHub `noreply` address, so GitHub may attribute eligible automated snapshot commits to the account. Verify the exact address under [GitHub email settings](https://github.com/settings/emails). Only commits recognized as yours on the default branch can count toward your personal Contributions graph. GitHub can take up to 24 hours to display qualifying commits. **Neither a particular shade of green nor uninterrupted daily runs can be guaranteed.** GitHub Actions can delay or skip scheduled runs, and GitHub policies still apply.

Automated commits made with `GITHUB_TOKEN` do **not** trigger new `push` workflow runs. Monitor performs its own validation before committing, and dashboard publication runs through `workflow_run`.
