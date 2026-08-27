package agents

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/tools/agenttoolset"
	"github.com/google/go-github/v89/github"
	"github.com/gummicube/gc-wizard/internal/commands"
	"github.com/gummicube/gc-wizard/internal/remotes"
)

type anthropicService struct{}

var AnthropicClaude = &anthropicService{}

const defaultAnthropicMaxTokens = 5000

var anthropicClient = sync.OnceValue(func() anthropic.Client {
	token := os.Getenv("ANTHROPIC_API_KEY")
	if token == "" {
		log.Fatal("anthropic: ANTHROPIC_API_KEY env var is required")
	}
	return anthropic.NewClient(option.WithAPIKey(token))
})

func anthropicModel() anthropic.Model {
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		log.Fatal("anthropic: ANTHROPIC_MODEL env var is required")
	}
	return anthropic.Model(model)
}

func anthropicMaxTokens() int64 {
	raw := os.Getenv("ANTHROPIC_MAX_TOKENS")
	if raw == "" {
		return defaultAnthropicMaxTokens
	}
	maxTokens, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		log.Fatalf("anthropic: invalid ANTHROPIC_MAX_TOKENS %q: %v", raw, err)
	}
	return maxTokens
}

// NOTE: PLEASE READ WHY client.Beta.Messages is being used over client.Messages
//  - Tool Runner (client.Beta.Messages.NewToolRunner) — anytime you want the SDK to run the agentic loop
//  instead of writing it manually (for loops/max iterations)
//  - The prebuilt agent toolset (what's used here) or Managed Agents (client.Beta.Agents, client.Beta.Sessions,
//  client.Beta.Environments) — hosted/managed agent primitives
//  - Context editing, compaction, MCP connector, advisor tool, memory tool, cache diagnostics — all beta
//  - Plain single-shot Messages.New calls with no beta features → use client.Messages.New, not
//  client.Beta.Messages.New

//  One thing worth flagging: this file is using ModelClaudeSonnet5 as the model (line 41) with the Tool Runner
//  — that's fine, but note git status shows internal/agents/claude_code.go was deleted and this anthropic.go
//  seems to be its replacement. Let me know if you want me to look at anything else in this file (e.g., the
//  sync.OnceValue client init, or whether error handling around runner.RunToCompletion needs adjustment).
func (s *anthropicService) resolve(ctx context.Context, repoPath, prompt string) (string, error) {
	client := anthropicClient()

	env := &agenttoolset.AgentToolContext{Workdir: repoPath}

	tools := agenttoolset.BetaAgentToolset20260401(env)
	defer agenttoolset.CloseAll(tools)
	runner := client.Beta.Messages.NewToolRunner(tools, anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: anthropic.BetaMessageNewParams{
			Model:     anthropicModel(),
			MaxTokens: anthropicMaxTokens(),
			Messages: []anthropic.BetaMessageParam{
				anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt)),
			},
		},
	})

	message, err := runner.RunToCompletion(ctx)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}

	var reply strings.Builder
	for _, block := range message.Content {
		if textBlock, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			reply.WriteString(textBlock.Text)
		}
	}
	return reply.String(), nil
}

// ResolveGithubIssue resolves a GitHub issue end-to-end: it prompts Claude to
// make the necessary code changes in the repository, then commits, pushes,
// and opens a pull request with the result.
func (s *anthropicService) ResolveGithubIssue(ctx context.Context, owner, repoName string, issue *github.Issue) error {
	if owner == "" || repoName == "" {
		return fmt.Errorf("issue event is missing repository owner/name")
	}
	ghs := remotes.GithubService
	defBranch, err := ghs.GetDefaultBranch(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}

	repoPath, err := commands.Git.EnsureRepo(ctx, owner, repoName, defBranch)
	if err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}

	prompt := fmt.Sprintf(
		"Resolve the following GitHub issue in this repository. Make whatever "+
			"code changes are needed; don't just describe the fix.\n\nTitle: %s\n\n%s",
		issue.GetTitle(), issue.GetBody(),
	)
	reply, err := s.resolve(ctx, repoPath, prompt)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	log.Printf("issue #%d: claude resolution attempt:\n%s", issue.GetNumber(), reply)

	branch := ghs.NameBranchForIssue(issue)
	if err := commands.Git.CreateLocalBranch(ctx, repoPath, branch); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

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

	summaryPrompt := "Summarize the code changes you just made to resolve this issue. " +
		"Write a concise summary (2-5 sentences) describing what changed and why, suitable for a pull request description."
	summary, err := s.resolve(ctx, repoPath, summaryPrompt)
	if err != nil {
		return fmt.Errorf("anthropic: summarize changes: %w", err)
	}
	log.Printf("issue #%d: claude change summary:\n%s", issue.GetNumber(), summary)

	prTitle := fmt.Sprintf("Fix: %s", issue.GetTitle())
	prBody := fmt.Sprintf("Resolves #%d\n\n---\n\n%s", issue.GetNumber(), summary)
	pr, err := ghs.CreatePullRequest(ctx, owner, repoName, prTitle, branch, defBranch, prBody)
	if err != nil {
		return fmt.Errorf("create pull request: %w", err)
	}

	log.Printf("issue #%d: opened pull request %s", issue.GetNumber(), pr.GetHTMLURL())
	return nil
}

// ResolveGithubIssueComment reads a comment left on a GitHub issue and posts
// Claude's reply back as a new comment on that issue.
func (s *anthropicService) ResolveGithubIssueComment(ctx context.Context, owner, repoName string, issue *github.Issue, comment *github.IssueComment) error {
	if owner == "" || repoName == "" {
		return fmt.Errorf("issue comment event is missing repository owner/name")
	}
	ghs := remotes.GithubService
	defBranch, err := ghs.GetDefaultBranch(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}

	repoPath, err := commands.Git.EnsureRepo(ctx, owner, repoName, defBranch)
	if err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}
	prompt := fmt.Sprintf(
		"A user left a comment on the following GitHub issue in this repository. "+
			"Read the comment and reply to it directly and helpfully. You may look "+
			"at the repository for context, but do not make any code changes.\n\n"+
			"Issue title: %s\n\nIssue body: %s\n\nComment from %s:\n%s",
		issue.GetTitle(), issue.GetBody(), comment.GetUser().GetLogin(), comment.GetBody(),
	)
	reply, err := s.resolve(ctx, repoPath, prompt)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	log.Printf("issue #%d: claude comment reply:\n%s", issue.GetNumber(), reply)

	if err := ghs.CreateIssueComment(ctx, owner, repoName, issue.GetNumber(), reply); err != nil {
		return fmt.Errorf("create issue comment: %w", err)
	}

	log.Printf("issue #%d: posted reply to comment from %s", issue.GetNumber(), comment.GetUser().GetLogin())
	return nil
}

// ResolveGithubPullRequestReview reads a review left on one of gc-wizard's
// own pull requests, prompts Claude to make any requested changes on the
// PR's branch, then commits and pushes the result back to that branch.
func (s *anthropicService) ResolveGithubPullRequestReview(ctx context.Context, owner, repoName string, pr *github.PullRequest, review *github.PullRequestReview) error {
	if owner == "" || repoName == "" {
		return fmt.Errorf("pull request review event is missing repository owner/name")
	}
	branch := pr.GetHead().GetRef()
	if branch == "" {
		return fmt.Errorf("pull request #%d is missing a head branch", pr.GetNumber())
	}

	ghs := remotes.GithubService
	defBranch, err := ghs.GetDefaultBranch(ctx, owner, repoName)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}

	repoPath, err := commands.Git.EnsureRepo(ctx, owner, repoName, defBranch)
	if err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}
	if err := commands.Git.CheckoutBranch(ctx, owner, repoName, repoPath, branch); err != nil {
		return fmt.Errorf("checkout pull request branch: %w", err)
	}

	prompt := fmt.Sprintf(
		"A reviewer left a review on the following GitHub pull request in this "+
			"repository. If the review requests changes, make whatever code changes "+
			"are needed to address it; don't just describe the fix. If the review "+
			"doesn't request any changes, leave the code as-is.\n\n"+
			"PR title: %s\n\nPR body: %s\n\nReview by %s (state: %s):\n%s",
		pr.GetTitle(), pr.GetBody(), review.GetUser().GetLogin(), review.GetState(), review.GetBody(),
	)
	reply, err := s.resolve(ctx, repoPath, prompt)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	log.Printf("pull request #%d: claude review resolution attempt:\n%s", pr.GetNumber(), reply)

	changed, err := commands.Git.HasChanges(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("check for changes: %w", err)
	}
	if !changed {
		log.Printf("pull request #%d: claude made no changes in response to the review", pr.GetNumber())
		return nil
	}

	commitMsg := fmt.Sprintf("Address review feedback\n\nOn #%d", pr.GetNumber())
	if err := commands.Git.CommitAll(ctx, repoPath, commitMsg); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}
	if err := commands.Git.Push(ctx, repoPath, branch); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	log.Printf("pull request #%d: pushed follow-up commit to %s", pr.GetNumber(), branch)
	return nil
}
