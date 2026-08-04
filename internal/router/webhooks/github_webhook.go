package webhooks

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	
	"github.com/go-chi/chi/v5"
	"github.com/google/go-github/v89/github"
	"github.com/gummicube/gc-wizard/internal/agents"
	"github.com/gummicube/gc-wizard/internal/commands"
	"github.com/gummicube/gc-wizard/internal/remotes"
)

const wizardUsername = "imdol"

func Github() chi.Router {
	r := chi.NewRouter()
	r.Post("/github", func(w http.ResponseWriter, r *http.Request) {
		ghs := remotes.Service
		event, err := ghs.ValidateRequest(r)
		fmt.Printf("event triggered")
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		switch e := event.(type) {
		case *github.IssuesEvent:
			handleIssueAssignedEvent(r, e)
		case *github.IssueCommentEvent:
			handleIssueCommentEvent(r, e)
		}

		w.WriteHeader(http.StatusOK)
	})
	return r
}

func handleIssueAssignedEvent(r *http.Request, e *github.IssuesEvent) {
	fmt.Printf("came to the handle issued assigned event: %v\n", e)
	return
	if e.GetAction() != "assigned" || e.GetAssignee().GetLogin() != wizardUsername {
		return
	}
	issue := e.GetIssue()
	owner := e.GetRepo().GetOwner().GetLogin()
	repoName := e.GetRepo().GetName()
	log.Printf("issue #%d assigned to %s", issue.GetNumber(), wizardUsername)

	// GitHub expects a fast response to the webhook; the fix/PR flow can
	// take minutes, so it runs in the background off the request context.
	go func() {
		ctx := context.Background()
		if err := resolveIssue(ctx, owner, repoName, issue); err != nil {
			log.Printf("issue #%d: resolution failed: %v", issue.GetNumber(), err)
		}
	}()
}

func resolveIssue(ctx context.Context, owner, repoName string, issue *github.Issue) error {
	if owner == "" || repoName == "" {
		return fmt.Errorf("issue event is missing repository owner/name")
	}
	ghs := remotes.Service

	defBranch, err := ghs.GetDefaultBranch(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}

	repoPath, err := commands.Git.EnsureRepo(ctx, owner, repoName, defBranch)
	if err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}

	branch := nameBranchForIssue(issue)
	if err := commands.Git.CreateLocalBranch(ctx, repoPath, branch); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	prompt := fmt.Sprintf(
		"Resolve the following GitHub issue in this repository. Make whatever "+
			"code changes are needed; don't just describe the fix.\n\nTitle: %s\n\n%s",
		issue.GetTitle(), issue.GetBody(),
	)
	reply, err := agents.AnthropicClaude.ResolveIssue(ctx, repoPath, prompt)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	log.Printf("issue #%d: claude resolution attempt:\n%s", issue.GetNumber(), reply)

	changed, err := commands.Git.HasChanges(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("check for changes: %w", err)
	}
	if !changed {
		return fmt.Errorf("claude code made no changes")
	}

	commitMsg := fmt.Sprintf("Fix: %s\n\nResolves #%d", issue.GetTitle(), issue.GetNumber())
	if err := commands.Git.CommitAll(ctx, repoPath, commitMsg); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}
	if err := commands.Git.Push(ctx, repoPath, branch); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	prTitle := fmt.Sprintf("Fix: %s", issue.GetTitle())
	prBody := fmt.Sprintf("Resolves #%d\n\n---\n\n%s", issue.GetNumber(), reply)
	pr, err := ghs.CreatePullRequest(ctx, owner, repoName, prTitle, branch, defBranch, prBody)
	if err != nil {
		return fmt.Errorf("create pull request: %w", err)
	}

	log.Printf("issue #%d: opened pull request %s", issue.GetNumber(), pr.GetHTMLURL())
	return nil
}


func nameBranchForIssue(issue *github.Issue) string {
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

func handleIssueCommentEvent(r *http.Request, e *github.IssueCommentEvent) {
	if e.GetIssue().GetAssignee().GetLogin() != wizardUsername {
		return
	}
	log.Printf("comment on issue #%d (assigned to %s) by %s: %s",
		e.GetIssue().GetNumber(), wizardUsername, e.GetComment().GetUser().GetLogin(), e.GetComment().GetBody(),
	)
	// TODO: different behaviour for follow-up comments on issues gc-wizard owns.
}
