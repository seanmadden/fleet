package gitlab

import "testing"

func TestHostMatches(t *testing.T) {
	cases := map[string]bool{
		"gitlab.com":         true,
		"GitLab.com":         true,
		"gitlab.example.com": true,
		"git.gitlab.acme.io": true,
		"github.com":         false,
		"bitbucket.org":      false,
		"git.corp.com":       false,
		"":                   false,
	}
	for host, want := range cases {
		if got := HostMatches(host); got != want {
			t.Errorf("HostMatches(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestNormalizeState(t *testing.T) {
	cases := map[string]string{
		"opened": "OPEN",
		"merged": "MERGED",
		"closed": "CLOSED",
		"locked": "CLOSED",
		"OPENED": "OPEN",
		"":       "CLOSED",
	}
	for in, want := range cases {
		if got := normalizeState(in); got != want {
			t.Errorf("normalizeState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPipelineStatus(t *testing.T) {
	tests := []struct {
		name           string
		head, fallback *glabPipeline
		want           string
	}{
		{"nil both", nil, nil, ""},
		{"head success", &glabPipeline{Status: "success"}, nil, "SUCCESS"},
		{"head failed", &glabPipeline{Status: "failed"}, nil, "FAILURE"},
		{"head running", &glabPipeline{Status: "running"}, nil, "PENDING"},
		{"head created", &glabPipeline{Status: "created"}, nil, "PENDING"},
		{"head canceled -> neutral", &glabPipeline{Status: "canceled"}, nil, ""},
		{"head manual -> neutral", &glabPipeline{Status: "manual"}, nil, ""},
		{"head preferred over fallback", &glabPipeline{Status: "running"}, &glabPipeline{Status: "success"}, "PENDING"},
		{"fallback used when no head", nil, &glabPipeline{Status: "success"}, "SUCCESS"},
		{"case insensitive", &glabPipeline{Status: "Success"}, nil, "SUCCESS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelineStatus(tt.head, tt.fallback); got != tt.want {
				t.Errorf("pipelineStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApprovalDecision(t *testing.T) {
	approvedBy := func(n int) glabApprovals {
		a := glabApprovals{}
		a.ApprovedBy = make([]struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
		}, n)
		return a
	}
	tests := []struct {
		name string
		ap   glabApprovals
		want string
	}{
		{"explicit approved flag", glabApprovals{Approved: true}, "APPROVED"},
		{"rule satisfied", glabApprovals{ApprovalsRequired: 2, ApprovalsLeft: 0}, "APPROVED"},
		{"rule not satisfied", glabApprovals{ApprovalsRequired: 2, ApprovalsLeft: 1}, ""},
		{"no rule, no approvals", glabApprovals{}, ""},
		{"no rule, one approval", approvedBy(1), "APPROVED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := approvalDecision(tt.ap); got != tt.want {
				t.Errorf("approvalDecision() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMR(t *testing.T) {
	t.Run("open MR with passing pipeline and conflicts", func(t *testing.T) {
		data := []byte(`{
			"iid": 42,
			"title": "Add widget",
			"web_url": "https://gitlab.com/group/sub/proj/-/merge_requests/42",
			"state": "opened",
			"project_id": 7,
			"has_conflicts": true,
			"detailed_merge_status": "conflict",
			"blocking_discussions_resolved": false,
			"head_pipeline": {"id": 99, "status": "running"},
			"pipeline": {"id": 50, "status": "success"}
		}`)
		pr, mr, err := parseMR(data)
		if err != nil {
			t.Fatalf("parseMR: %v", err)
		}
		if pr == nil {
			t.Fatal("parseMR returned nil PR")
		}
		if pr.Number != 42 || pr.Title != "Add widget" {
			t.Errorf("number/title = %d/%q", pr.Number, pr.Title)
		}
		if pr.State != "OPEN" {
			t.Errorf("state = %q, want OPEN", pr.State)
		}
		if pr.CIStatus != "PENDING" { // head pipeline wins
			t.Errorf("CIStatus = %q, want PENDING", pr.CIStatus)
		}
		if !pr.HasConflicts {
			t.Error("HasConflicts = false, want true")
		}
		if pr.UnresolvedThreads != 1 {
			t.Errorf("UnresolvedThreads = %d, want 1", pr.UnresolvedThreads)
		}
		if pr.Forge != "gitlab" {
			t.Errorf("Forge = %q, want gitlab", pr.Forge)
		}
		if pr.ReviewDecision != "" {
			t.Errorf("ReviewDecision = %q, want empty (set separately)", pr.ReviewDecision)
		}
		if mr.ProjectID != 7 {
			t.Errorf("ProjectID = %d, want 7", mr.ProjectID)
		}
	})

	t.Run("merged MR, resolved discussions, no conflicts", func(t *testing.T) {
		data := []byte(`{
			"iid": 3,
			"title": "Fix typo",
			"web_url": "https://gitlab.com/x/y/-/merge_requests/3",
			"state": "merged",
			"project_id": 1,
			"blocking_discussions_resolved": true,
			"pipeline": {"id": 1, "status": "success"}
		}`)
		pr, _, err := parseMR(data)
		if err != nil {
			t.Fatalf("parseMR: %v", err)
		}
		if pr.State != "MERGED" || pr.CIStatus != "SUCCESS" || pr.HasConflicts || pr.UnresolvedThreads != 0 {
			t.Errorf("unexpected pr: %+v", pr)
		}
	})

	t.Run("empty payload -> nil", func(t *testing.T) {
		pr, _, err := parseMR([]byte(`{}`))
		if err != nil {
			t.Fatalf("parseMR: %v", err)
		}
		if pr != nil {
			t.Errorf("expected nil PR, got %+v", pr)
		}
	})

	t.Run("garbage -> error", func(t *testing.T) {
		if _, _, err := parseMR([]byte("not json")); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}
