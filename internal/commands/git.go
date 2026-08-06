package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/object"
	transporthttp "github.com/go-git/go-git/v6/plumbing/transport/http"

	"github.com/gummicube/gc-wizard/internal/remotes"
)

// ReposDir is the root directory (relative to the process working
// directory) under which repositories are cloned.
const ReposDir = "repos"

var commitAuthor = &object.Signature{
	Name:  "gc-wizard",
	Email: "gc-wizard@users.noreply.github.com",
}

type gitCommand struct{}

var Git = &gitCommand{}

// authOption builds the transport auth used to talk to GitHub over HTTPS,
// from the same token remotes.Service authenticates its API client with.
func authOption() (client.Option, error) {
	token := remotes.Service.Token()
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN is not configured")
	}
	return client.WithHTTPAuth(&transporthttp.BasicAuth{
		Username: "x-access-token",
		Password: token,
	}), nil
}

// EnsureRepo makes sure owner/repo is cloned locally under ReposDir and
// checked out to the latest defaultBranch, returning its local path. If the
// repo was already cloned, it is fetched and reset instead of re-cloned.
func (c *gitCommand) EnsureRepo(ctx context.Context, owner, repo, defaultBranch string) (string, error) {
	auth, err := authOption()
	if err != nil {
		return "", err
	}

	root, err := filepath.Abs(ReposDir)
	if err != nil {
		return "", fmt.Errorf("resolve repos dir: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create repos dir: %w", err)
	}

	path := filepath.Join(root, repo)
	remote := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	branchRef := plumbing.NewBranchReferenceName(defaultBranch)

	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		r, err := git.PlainOpen(path)
		if err != nil {
			return "", fmt.Errorf("open repo: %w", err)
		}

		refSpec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", defaultBranch, defaultBranch))
		err = r.FetchContext(ctx, &git.FetchOptions{
			RemoteURL:     remote,
			RefSpecs:      []config.RefSpec{refSpec},
			ClientOptions: []client.Option{auth},
			Force:         true,
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return "", fmt.Errorf("fetch: %w", err)
		}

		wt, err := r.Worktree()
		if err != nil {
			return "", fmt.Errorf("worktree: %w", err)
		}
		if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
			return "", fmt.Errorf("checkout: %w", err)
		}

		remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", defaultBranch), true)
		if err != nil {
			return "", fmt.Errorf("resolve remote branch: %w", err)
		}
		if err := wt.Reset(&git.ResetOptions{Commit: remoteRef.Hash(), Mode: git.HardReset}); err != nil {
			return "", fmt.Errorf("reset: %w", err)
		}
		if err := wt.Clean(&git.CleanOptions{Dir: true}); err != nil {
			return "", fmt.Errorf("clean: %w", err)
		}
	} else if os.IsNotExist(err) {
		_, err := git.PlainCloneContext(ctx, path, &git.CloneOptions{
			URL:           remote,
			ReferenceName: branchRef,
			SingleBranch:  true,
			ClientOptions: []client.Option{auth},
		})
		if err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
	} else {
		return "", fmt.Errorf("stat repo path: %w", err)
	}

	return path, nil
}

// CreateLocalBranch checks out a new branch from the current HEAD in the
// local checkout at path.
func (c *gitCommand) CreateLocalBranch(ctx context.Context, path, branch string) error {
	r, err := git.PlainOpen(path)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
		Create: true,
	}); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}
	return nil
}

// CheckoutBranch fetches branch from origin and checks it out in the local
// checkout at path, resetting the working tree to match the remote exactly.
// Unlike CreateLocalBranch, branch is expected to already exist on origin
// (e.g. an open pull request's head branch).
func (c *gitCommand) CheckoutBranch(ctx context.Context, owner, repo, path, branch string) error {
	auth, err := authOption()
	if err != nil {
		return err
	}
	r, err := git.PlainOpen(path)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	remote := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	refSpec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch))
	if err := r.FetchContext(ctx, &git.FetchOptions{
		RemoteURL:     remote,
		RefSpecs:      []config.RefSpec{refSpec},
		ClientOptions: []client.Option{auth},
		Force:         true,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("fetch: %w", err)
	}

	remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return fmt.Errorf("resolve remote branch: %w", err)
	}

	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(branch)
	// Create the local branch tracking the fetched commit the first time we
	// see it; on later runs it already exists, so just switch to it.
	create := true
	if _, err := r.Reference(branchRef, true); err == nil {
		create = false
	}
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Hash: remoteRef.Hash(), Create: create, Force: true}); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}
	if err := wt.Reset(&git.ResetOptions{Commit: remoteRef.Hash(), Mode: git.HardReset}); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	if err := wt.Clean(&git.CleanOptions{Dir: true}); err != nil {
		return fmt.Errorf("clean: %w", err)
	}
	return nil
}

// HasChanges reports whether the working tree has uncommitted changes
// (tracked or untracked).
func (c *gitCommand) HasChanges(ctx context.Context, path string) (bool, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return false, fmt.Errorf("open repo: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return false, fmt.Errorf("worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("status: %w", err)
	}
	return !status.IsClean(), nil
}

// CommitAll stages every change in the working tree and commits it.
func (c *gitCommand) CommitAll(ctx context.Context, path, message string) error {
	r, err := git.PlainOpen(path)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("add: %w", err)
	}

	author := *commitAuthor
	author.When = time.Now()
	if _, err := wt.Commit(message, &git.CommitOptions{Author: &author}); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Push pushes branch to origin, creating it remotely and setting upstream.
func (c *gitCommand) Push(ctx context.Context, path, branch string) error {
	auth, err := authOption()
	if err != nil {
		return err
	}
	r, err := git.PlainOpen(path)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	refSpec := config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch))
	if err := r.PushContext(ctx, &git.PushOptions{
		RemoteName:    "origin",
		RefSpecs:      []config.RefSpec{refSpec},
		ClientOptions: []client.Option{auth},
	}); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	return nil
}
