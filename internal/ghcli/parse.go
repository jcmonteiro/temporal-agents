package ghcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"temporal-agents/internal/codereview"
)

// errNoOpenPR is the sentinel returned when a branch has no open PR. It lets
// EnsureOpen distinguish the genuine "none exists yet" case (where creating one
// is correct) from other FindOpen failures such as more-than-one open PR or a
// transient/auth `gh` error (where creating one would be spurious).
var errNoOpenPR = errors.New("no open pull request found for branch")

// parseRepo extracts owner and name from `gh repo view --json owner,name`.
func parseRepo(data []byte) (owner, repo string, err error) {
	var v struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return "", "", fmt.Errorf("parse repo: %w", err)
	}
	if v.Owner.Login == "" || v.Name == "" {
		return "", "", fmt.Errorf("parse repo: missing owner or name in %q", strings.TrimSpace(string(data)))
	}
	return v.Owner.Login, v.Name, nil
}

// parsePRList decodes `gh pr list --json number,url,headRefName` into domain PRs,
// attaching the base repo owner/name (which reply/resolve/review operate on).
func parsePRList(data []byte, owner, repo string) ([]codereview.PullRequest, error) {
	var raw []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse PR list: %w", err)
	}
	prs := make([]codereview.PullRequest, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, codereview.PullRequest{
			Number:  r.Number,
			URL:     r.URL,
			Owner:   owner,
			Repo:    repo,
			HeadRef: r.HeadRefName,
			Body:    r.Body,
		})
	}
	return prs, nil
}

// selectOpenPR enforces exactly one open PR for the branch. When none exist it
// returns errNoOpenPR (wrapped) so callers can react specifically to that case.
func selectOpenPR(prs []codereview.PullRequest, branch string) (codereview.PullRequest, error) {
	switch len(prs) {
	case 0:
		return codereview.PullRequest{}, fmt.Errorf("%w %q", errNoOpenPR, branch)
	case 1:
		return prs[0], nil
	default:
		nums := make([]string, len(prs))
		for i, p := range prs {
			nums[i] = "#" + strconv.Itoa(p.Number)
		}
		return codereview.PullRequest{}, fmt.Errorf("found %d open pull requests for branch %q (%s); expected exactly one",
			len(prs), branch, strings.Join(nums, ", "))
	}
}

// parseReviewOngoing reports whether Copilot is among the PR's requested (not
// yet delivered) reviewers, given the JSON array from the REST pulls endpoint's
// requested_reviewers field. Each entry has a login ("Copilot" for the bot when
// requested); any login mentioning "copilot" counts.
func parseReviewOngoing(data []byte) (bool, error) {
	var reviewers []struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(data, &reviewers); err != nil {
		return false, fmt.Errorf("parse requested reviewers: %w", err)
	}
	for _, r := range reviewers {
		if strings.Contains(strings.ToLower(r.Login), "copilot") {
			return true, nil
		}
	}
	return false, nil
}

// parseReviewThreads decodes the reviewThreads GraphQL response, keeping only
// unresolved threads and combining each thread's comments into a single body.
func parseReviewThreads(data []byte) ([]codereview.ReviewThread, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							Path       string `json:"path"`
							Line       int    `json:"line"`
							Comments   struct {
								Nodes []struct {
									Body   string `json:"body"`
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse review threads: %w", err)
	}

	var threads []codereview.ReviewThread
	for _, n := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if n.IsResolved {
			continue
		}
		author := ""
		var bodies []string
		for _, c := range n.Comments.Nodes {
			if author == "" {
				author = c.Author.Login
			}
			if b := strings.TrimSpace(c.Body); b != "" {
				bodies = append(bodies, b)
			}
		}
		threads = append(threads, codereview.ReviewThread{
			ID:     n.ID,
			Path:   n.Path,
			Line:   n.Line,
			Author: author,
			Body:   strings.Join(bodies, "\n\n"),
		})
	}
	return threads, nil
}
