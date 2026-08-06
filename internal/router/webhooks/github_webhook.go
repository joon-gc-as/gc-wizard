package webhooks

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/go-github/v89/github"
	"github.com/gummicube/gc-wizard/internal/agents"
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
		case *github.PullRequestReviewEvent:
			handlePullRequestReviewEvent(r, e)
		case *github.PullRequestReviewCommentEvent:
			handlePullRequestReviewCommentEvent(r, e)
		case *github.CommitCommentEvent:
			handleCommitCommentEvent(r, e)
		}

		w.WriteHeader(http.StatusOK)
	})
	return r
}

///////////////////////////////////////////////////////////////////////////////
//                         WHEN AN ISSUE IS ASSIGNED                         //
///////////////////////////////////////////////////////////////////////////////
func handleIssueAssignedEvent(r *http.Request, e *github.IssuesEvent) {
	assignee := e.GetIssue().GetAssignee().GetLogin()
	if e.GetAction() != "assigned" || assignee != wizardUsername {
		return
	}
	issue := e.GetIssue()
	owner := e.GetRepo().GetOwner().GetLogin()
	repoName := e.GetRepo().GetName()
	log.Printf("issue #%d assigned to %s", issue.GetNumber(), wizardUsername)

	// GitHub expects a fast response to the webhook; the fix/PR flow can
	// take minutes, so it runs in the background off the request context
	go func() {
		ctx := context.Background()
		if err := agents.AnthropicClaude.ResolveGithubIssue(ctx, owner, repoName, issue); err != nil {
			log.Printf("issue #%d: resolution failed: %v", issue.GetNumber(), err)
		}
	}()
}

///////////////////////////////////////////////////////////////////////////////
//                        WHEN AN ISSUE HAS A COMMENT                        //
///////////////////////////////////////////////////////////////////////////////
func handleIssueCommentEvent(_ *http.Request, e *github.IssueCommentEvent) {
	assignee := e.GetIssue().GetAssignee().GetLogin()
	if assignee != wizardUsername {
		return
	}
	issue := e.GetIssue()
	owner := e.GetRepo().GetOwner().GetLogin()
	repoName := e.GetRepo().GetName()
	comment := e.GetComment()
	log.Printf("comment on issue #%d (assigned to %s) by %s: %s",
		issue.GetNumber(), wizardUsername, comment.GetUser().GetLogin(), comment.GetBody(),
	)
	go func() {
		ctx := context.Background()
		if err := agents.AnthropicClaude.ResolveGithubIssueComment(ctx, owner, repoName, issue, comment); err != nil {
			log.Printf("issue #%d: resolution failed: %v", issue.GetNumber(), err)
		}
	}()
}

///////////////////////////////////////////////////////////////////////////////
//                                  WHEN PR REVIEW                           //
///////////////////////////////////////////////////////////////////////////////
func handlePullRequestReviewEvent(_ *http.Request, e *github.PullRequestReviewEvent) {
	if e.GetPullRequest().GetAssignee().GetLogin() != wizardUsername {
		return
	}
	pr := e.GetPullRequest()
	review := e.GetReview()
	owner := e.GetRepo().GetOwner().GetLogin()
	repoName := e.GetRepo().GetName()
	log.Printf("review on pull request #%d (assigned to %s) by %s: %s (%s)",
		pr.GetNumber(), wizardUsername, review.GetUser().GetLogin(), review.GetState(), review.GetBody(),
	)
	go func() {
		ctx := context.Background()
		if err := agents.AnthropicClaude.ResolveGithubPullRequestReview(ctx, owner, repoName, pr, review); err != nil {
			log.Printf("pull request #%d: resolution failed: %v", pr.GetNumber(), err)
		}
	}()
}

// TODO: IBID...
///////////////////////////////////////////////////////////////////////////////
//                         WHEN COMMIT HAS A COMMENT                         //
///////////////////////////////////////////////////////////////////////////////
func handleCommitCommentEvent(_ *http.Request, e *github.CommitCommentEvent) {
	log.Printf("comment on commit %s by %s: %s",
		e.GetComment().GetCommitID(), e.GetComment().GetUser().GetLogin(), e.GetComment().GetBody(),
	)
	// TODO: commit comments have no assignee to gate on; figure out how gc-wizard should react.
}

///////////////////////////////////////////////////////////////////////////////
//                         WHEN PR HAS REVIEW COMMENT                        //
///////////////////////////////////////////////////////////////////////////////
func handlePullRequestReviewCommentEvent(_ *http.Request, e *github.PullRequestReviewCommentEvent) {
	if e.GetPullRequest().GetAssignee().GetLogin() != wizardUsername {
		return
	}
	log.Printf("review comment on pull request #%d (assigned to %s) by %s: %s",
		e.GetPullRequest().GetNumber(), wizardUsername, e.GetComment().GetUser().GetLogin(), e.GetComment().GetBody(),
	)
	// TODO: feed review comments on gc-wizard's own pull requests back to claude.
}
