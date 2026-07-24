package ghcli

import "testing"

func TestParseRepo(t *testing.T) {
	owner, repo, err := parseRepo([]byte(`{"owner":{"login":"acme"},"name":"widgets"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme" || repo != "widgets" {
		t.Fatalf("got %q/%q, want acme/widgets", owner, repo)
	}
}

func TestParseRepo_Missing(t *testing.T) {
	if _, _, err := parseRepo([]byte(`{"owner":{"login":""},"name":""}`)); err == nil {
		t.Fatal("expected error for missing owner/name")
	}
}

func TestSelectOpenPR(t *testing.T) {
	one, err := parsePRList([]byte(`[{"number":7,"url":"u","headRefName":"feat"}]`), "acme", "widgets")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	t.Run("exactly one", func(t *testing.T) {
		pr, err := selectOpenPR(one, "feat")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pr.Number != 7 || pr.Owner != "acme" || pr.Repo != "widgets" || pr.HeadRef != "feat" {
			t.Fatalf("unexpected PR: %+v", pr)
		}
	})

	t.Run("none is an error", func(t *testing.T) {
		if _, err := selectOpenPR(nil, "feat"); err == nil {
			t.Fatal("expected error for no open PR")
		}
	})

	t.Run("multiple is an error", func(t *testing.T) {
		many, _ := parsePRList([]byte(`[{"number":7},{"number":8}]`), "acme", "widgets")
		if _, err := selectOpenPR(many, "feat"); err == nil {
			t.Fatal("expected error for multiple open PRs")
		}
	})
}

func TestParseReviewThreads_KeepsUnresolvedAndCombinesBodies(t *testing.T) {
	data := []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
		{"id":"t1","isResolved":false,"path":"a.go","line":10,"comments":{"nodes":[
			{"body":"first","author":{"login":"octocat"}},
			{"body":"second","author":{"login":"other"}}
		]}},
		{"id":"t2","isResolved":true,"path":"b.go","line":5,"comments":{"nodes":[
			{"body":"already done","author":{"login":"octocat"}}
		]}}
	]}}}}}`)

	threads, err := parseReviewThreads(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("expected 1 unresolved thread, got %d", len(threads))
	}
	th := threads[0]
	if th.ID != "t1" {
		t.Fatalf("wrong thread id: %q", th.ID)
	}
	if th.Author != "octocat" {
		t.Fatalf("author should be the first commenter, got %q", th.Author)
	}
	if th.Body != "first\n\nsecond" {
		t.Fatalf("bodies should be combined, got %q", th.Body)
	}
	if th.Path != "a.go" || th.Line != 10 {
		t.Fatalf("location not parsed: %+v", th)
	}
}

func TestParseReviewOngoing(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"copilot bot pending", `{"reviewRequests":[{"login":"copilot-pull-request-reviewer"}]}`, true},
		{"copilot by display name", `{"reviewRequests":[{"name":"Copilot"}]}`, true},
		{"only a human reviewer", `{"reviewRequests":[{"login":"octocat"}]}`, false},
		{"no requests", `{"reviewRequests":[]}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseReviewOngoing([]byte(tc.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseReviewOngoing(%s) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
