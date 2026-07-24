// Package ghcli is a driven adapter over the `gh` command line. It implements
// the codereview.PullRequests port.
package ghcli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"temporal-agents/internal/codereview"
)

// copilotReviewer is the handle `gh pr edit --add-reviewer` accepts for the
// Copilot code-review bot.
const copilotReviewer = "@copilot"

// GitHub runs GitHub operations via the `gh` CLI.
type GitHub struct{}

// New returns a gh CLI adapter.
func New() GitHub { return GitHub{} }

// FindOpen locates the single open PR whose head is branch, failing when there
// is no open PR or more than one.
func (h GitHub) FindOpen(ctx context.Context, dir, branch string) (codereview.PullRequest, error) {
	owner, repo, err := h.baseRepo(ctx, dir)
	if err != nil {
		return codereview.PullRequest{}, err
	}
	out, err := runDir(ctx, dir, "pr", "list",
		"--head", branch, "--state", "open",
		"--json", "number,url,headRefName,body")
	if err != nil {
		return codereview.PullRequest{}, err
	}
	prs, err := parsePRList([]byte(out), owner, repo)
	if err != nil {
		return codereview.PullRequest{}, err
	}
	return selectOpenPR(prs, branch)
}

// baseRepo returns the owner and name of the repository in dir.
func (h GitHub) baseRepo(ctx context.Context, dir string) (owner, repo string, err error) {
	out, err := runDir(ctx, dir, "repo", "view", "--json", "owner,name")
	if err != nil {
		return "", "", err
	}
	return parseRepo([]byte(out))
}

// ReviewOngoing reports whether a Copilot review is still pending on the PR,
// i.e. Copilot is a requested reviewer that has not yet delivered its review.
func (h GitHub) ReviewOngoing(ctx context.Context, pr codereview.PullRequest) (bool, error) {
	out, err := run(ctx,
		"pr", "view", strconv.Itoa(pr.Number),
		"--repo", pr.Owner+"/"+pr.Repo,
		"--json", "reviewRequests",
	)
	if err != nil {
		return false, err
	}
	return parseReviewOngoing([]byte(out))
}

// UnresolvedThreads returns the PR's unresolved review threads.
func (h GitHub) UnresolvedThreads(ctx context.Context, pr codereview.PullRequest) ([]codereview.ReviewThread, error) {
	out, err := run(ctx,
		"api", "graphql",
		"-f", "query="+unresolvedThreadsQuery,
		"-F", "owner="+pr.Owner,
		"-F", "repo="+pr.Repo,
		"-F", "number="+strconv.Itoa(pr.Number),
	)
	if err != nil {
		return nil, err
	}
	return parseReviewThreads([]byte(out))
}

// Reply posts body as a reply on the given review thread.
func (h GitHub) Reply(ctx context.Context, _ codereview.PullRequest, threadID, body string) error {
	_, err := run(ctx,
		"api", "graphql",
		"-f", "query="+replyMutation,
		"-F", "threadId="+threadID,
		"-F", "body="+body,
	)
	return err
}

// Resolve marks the given review thread as resolved.
func (h GitHub) Resolve(ctx context.Context, _ codereview.PullRequest, threadID string) error {
	_, err := run(ctx,
		"api", "graphql",
		"-f", "query="+resolveMutation,
		"-F", "threadId="+threadID,
	)
	return err
}

// RequestCopilotReview requests a fresh Copilot review on the PR.
func (h GitHub) RequestCopilotReview(ctx context.Context, pr codereview.PullRequest) error {
	_, err := run(ctx,
		"pr", "edit", strconv.Itoa(pr.Number),
		"--repo", pr.Owner+"/"+pr.Repo,
		"--add-reviewer", copilotReviewer,
	)
	return err
}

const unresolvedThreadsQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      reviewThreads(first:100){
        nodes{
          id
          isResolved
          path
          line
          comments(first:100){ nodes{ body author{ login } } }
        }
      }
    }
  }
}`

const replyMutation = `mutation($threadId:ID!,$body:String!){
  addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$threadId,body:$body}){
    comment{ id }
  }
}`

const resolveMutation = `mutation($threadId:ID!){
  resolveReviewThread(input:{threadId:$threadId}){
    thread{ isResolved }
  }
}`

// run executes `gh <args...>` and returns stdout, wrapping failures with stderr.
func run(ctx context.Context, args ...string) (string, error) {
	return runDir(ctx, "", args...)
}

// runDir executes `gh <args...>` in dir (or the current directory when empty).
func runDir(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
