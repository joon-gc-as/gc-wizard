# Gummicube Wizard

![Wizard](static/wizard.webp)

It's bot that resolves issues autonomously. Assign it to a
GitHub issue and it uses Claude (via the Anthropic API KEY) to write the
fix, commit, push a branch, and open a pull request — no human in the loop
until review time.

## How it works

1. GitHub sends a webhook to `/webhooks/github` when an issue is assigned,
   commented on, or a pull request it owns is reviewed.
2. `internal/router/webhooks` validates the webhook signature and routes the
   event to a handler based on its type.
3. `internal/agents` builds a prompt from the issue/comment/review and hands
   it to Claude through the Anthropic SDK's tool runner, which can read and
   edit files in a local clone of the target repository.
4. `internal/commands/git` clones/updates the repo, creates a branch, and
   commits and pushes Claude's changes using `go-git`.
5. `internal/remotes/github` talks to the GitHub API to open the pull
   request (or post a reply comment, for issue comments that don't need code
   changes).

Supported events:

| Event | Behavior |
|---|---|
| Issue assigned to the bot | Resolve the issue, push a branch, open a PR |
| Comment on an issue assigned to the bot | Reply to the comment (read-only, no code changes) |
| Review on one of the bot's pull requests | Address the review feedback and push a follow-up commit |
| Comment on a pull request review | Not yet implemented (logged only) |
| Commit comment | Not yet implemented (logged only) |

## Project layout

```
main.go                              entrypoint: loads .env, starts the HTTP server
internal/
  server/                            builds the http.Server
  router/                            chi router, mounts routes and webhooks
    routes/                          authenticated API routes (e.g. GET /github/repos)
    webhooks/                        GitHub webhook handler and event dispatch
  agents/                            Claude/Anthropic integration that resolves issues
  remotes/                           GitHub API client (go-github)
  commands/                          git operations against local repo clones (go-git)
  proxy/                             forwards a smee.io tunnel to the local webhook endpoint (dev only)
repos/                                local clones the wizard works in at runtime (gitignored)
```

## Requirements

- Go (see `go.mod` for the toolchain version)
- A GitHub account/App or personal access token the bot can act as
- An Anthropic API key

## Configuration

Configuration is loaded from a `.env` file in the project root (see
`.gitignore` — `.env` is not committed). Copy the variables below into your
own `.env`:

| Variable | Description |
|---|---|
| `APP_ENV` | Set to `PRODUCTION` to disable the local smee.io proxy |
| `APP_PORT` | Port the HTTP server listens on |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `ANTHROPIC_MODEL` | Claude model to use |
| `ANTHROPIC_MAX_TOKENS` | Max tokens per Claude response (optional, defaults to 5000) |
| `GITHUB_TOKEN` | GitHub token used for API calls and git push/pull over HTTPS |
| `GITHUB_USERNAME` | GitHub username the bot acts as |
| `GITHUB_WEBHOOK_SECRET` | Secret used to validate incoming GitHub webhook signatures |
| `PROXY_URL` | smee.io channel URL, for forwarding webhooks to a local dev server |
| `ENABLED_REPOS` | Repositories the bot is allowed to operate on |
| `OPENAI_API_KEY` | Reserved for future OpenAI/Codex support |

## Running locally

```sh
make run     # go run main.go
make build   # builds the `wizard` binary
make test    # go test ./...
make clean   # removes the built binary
```

For local development, set `PROXY_URL` to a [smee.io](https://smee.io)
channel and point your GitHub webhook at that channel; `internal/proxy`
forwards deliveries to `http://localhost:$APP_PORT/webhooks/github` so you
don't need a public endpoint.

## API

- `GET /health` — health check
- `GET /github/repos` — list repositories accessible to the authenticated GitHub token
- `POST /webhooks/github` — GitHub webhook receiver
