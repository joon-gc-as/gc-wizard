package remotes

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/google/go-github/v89/github"
)

type githubService struct{}

var Service = &githubService{}

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

func (s *githubService) GetDefaultBranch(ctx context.Context, owner, repo string) (string, error) {
	r, _, err := s.Client().Repositories.Get(ctx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("get repository %s/%s: %w", owner, repo, err)
	}
	br := r.GetDefaultBranch()
	return br, nil
}

// CreatePullRequest opens a pull request from head into base.
func (s *githubService) CreatePullRequest(ctx context.Context, owner, repo, title, head, base, body string) (*github.PullRequest, error) {
	pr, _, err := s.Client().PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: new(title),
		Head:  new(head),
		Base:  new(base),
		Body:  new(body),
	})
	if err != nil {
		return nil, fmt.Errorf("create pull request: %w", err)
	}
	return pr, nil
}

// CreateIssueComment posts a comment on the given issue (or pull request,
// since GitHub models PR conversations as issues under the hood).
func (s *githubService) CreateIssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	if _, _, err := s.Client().Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{
		Body: new(body),
	}); err != nil {
		return fmt.Errorf("create issue comment: %w", err)
	}
	return nil
}

func (s *githubService) NameBranchForIssue(issue *github.Issue) string {
	branchSlugPattern := regexp.MustCompile(`[^a-z0-9]+`)
	slug := strings.Trim(branchSlugPattern.ReplaceAllString(strings.ToLower(issue.GetTitle()), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "issue"
	}
	return fmt.Sprintf("gc-wizard/issue-%d-%s", issue.GetNumber(), slug)
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

