package remotes

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/google/go-github/v89/github"
)

// ReposDir is the root directory (relative to the process working
// directory) under which repositories are cloned.
const ReposDir = "repos"

type githubService struct{}
type githubCommand struct{}

var Service = &githubService{}
var Command = &githubCommand{}

var githubClient = sync.OnceValue(func() *github.Client {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("github: GITHUB_TOKEN env var is required")
	}
	client, err := github.NewClient(github.WithAuthToken(token))
	if err != nil {
		log.Fatalf("github: failed to create authenticated client: %v", err)
	}
	return client
})

var webhookSecret = sync.OnceValue(func() []byte {
	return []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
})

func (s *githubService) Client() *github.Client {
	return githubClient()
}

// Token returns the GitHub token used to authenticate the client, so callers
// can authenticate other tools (e.g. `git` over HTTPS) against the same
// credentials.
func (s *githubService) Token() string {
	return os.Getenv("GITHUB_TOKEN")
}

type RepositorySummary struct {
	Name  string
	Link  string
	Owner string
}

func (s *githubService) ListRepositories(ctx context.Context) ([]RepositorySummary, error) {
	var repos []RepositorySummary

	opts := &github.RepositoryListByAuthenticatedUserOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		page, resp, err := s.Client().Repositories.ListByAuthenticatedUser(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("list repositories: %w", err)
		}
		for _, repo := range page {
			repos = append(repos, RepositorySummary{
				Name:  repo.GetName(),
				Link:  repo.GetHTMLURL(),
				Owner: repo.GetOwner().GetLogin(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return repos, nil
}

func (s *githubService) ValidateRequest(r *http.Request) (any, error) {
	payload, err := github.ValidatePayload(r, webhookSecret())

	if err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}
	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		return nil, fmt.Errorf("unrecognized event: %w", err)
	}
	return event, nil
}

// GetRepository fetches repository metadata, e.g. to resolve the default
// branch and clone URL.
func (s *githubService) GetRepository(ctx context.Context, owner, repo string) (*github.Repository, error) {
	r, _, err := s.Client().Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get repository %s/%s: %w", owner, repo, err)
	}
	return r, nil
}

// CreatePullRequest opens a pull request from head into base.
func (s *githubService) CreatePullRequest(ctx context.Context, owner, repo, title, head, base, body string) (*github.PullRequest, error) {
	pr, _, err := s.Client().PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: github.Ptr(title),
		Head:  github.Ptr(head),
		Base:  github.Ptr(base),
		Body:  github.Ptr(body),
	})
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	return pr, nil
}

func (s *githubService) CreateBranch(ctx context.Context, owner, repo, baseBranch, branch string) error {
	base, _, err := s.Client().Repositories.GetBranch(ctx, owner, repo, baseBranch, 0)
	if err != nil {
		return fmt.Errorf("get base branch %q: %w", baseBranch, err)
	}

	ref := github.CreateRef{
		Ref: "refs/heads/" + branch,
		SHA: base.GetCommit().GetSHA(),
	}
	if _, _, err := s.Client().Git.CreateRef(ctx, owner, repo, ref); err != nil {
		return fmt.Errorf("create branch %q: %w", branch, err)
	}

	return nil
}

// EnsureRepo makes sure owner/repo is cloned locally under ReposDir and
// checked out to the latest defaultBranch, returning its local path. If the
// repo was already cloned, it is fetched and reset instead of re-cloned.
func (s *githubService) EnsureRepo(ctx context.Context, owner, repo, defaultBranch, token string) (string, error) {
	root, err := filepath.Abs(ReposDir)
	if err != nil {
		return "", fmt.Errorf("resolve repos dir: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create repos dir: %w", err)
	}

	path := filepath.Join(root, repo)
	remote := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, repo)

	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		if err := run(ctx, path, "git", "remote", "set-url", "origin", remote); err != nil {
			return "", err
		}
		if err := run(ctx, path, "git", "fetch", "origin", defaultBranch); err != nil {
			return "", err
		}
		if err := run(ctx, path, "git", "checkout", defaultBranch); err != nil {
			return "", err
		}
		if err := run(ctx, path, "git", "reset", "--hard", "origin/"+defaultBranch); err != nil {
			return "", err
		}
		if err := run(ctx, path, "git", "clean", "-fd"); err != nil {
			return "", err
		}
	} else if os.IsNotExist(err) {
		if err := run(ctx, root, "git", "clone", "--branch", defaultBranch, remote, repo); err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("stat repo path: %w", err)
	}

	if err := run(ctx, path, "git", "config", "user.name", "gc-wizard"); err != nil {
		return "", err
	}
	if err := run(ctx, path, "git", "config", "user.email", "gc-wizard@users.noreply.github.com"); err != nil {
		return "", err
	}

	return path, nil
}

// LocalCreateBranch checks out a new branch from the current HEAD in the
// local checkout at path.
func (s *githubService) LocalCreateBranch(ctx context.Context, path, branch string) error {
	return run(ctx, path, "git", "checkout", "-b", branch)
}

// HasChanges reports whether the working tree has uncommitted changes
// (tracked or untracked).
func (s *githubService) HasChanges(ctx context.Context, path string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(out) > 0, nil
}

// CommitAll stages every change in the working tree and commits it.
func (s *githubService) CommitAll(ctx context.Context, path, message string) error {
	if err := run(ctx, path, "git", "add", "-A"); err != nil {
		return err
	}
	return run(ctx, path, "git", "commit", "-m", message)
}

// Push pushes branch to origin, creating it remotely and setting upstream.
func (s* githubService) Push(ctx context.Context, path, branch string) error {
	return run(ctx, path, "git", "push", "-u", "origin", branch)
}

func run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return nil
}
