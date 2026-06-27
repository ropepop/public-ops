package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/acquisition"
)

func TestRetryFailedDryRunListsOnlyRetryableDuePeerFloodDrafts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	statePath := t.TempDir() + "/campaign.db"
	store, err := acquisition.OpenStore(statePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	if err := store.UpsertCandidates(ctx, []acquisition.Candidate{
		{UserID: 42, Username: "flooded", Source: acquisition.SourceRecentActive},
		{UserID: 43, Username: "invalid", Source: acquisition.SourceRecentActive},
	}, now); err != nil {
		t.Fatalf("UpsertCandidates: %v", err)
	}
	for _, item := range []struct {
		userID int64
		token  string
		reason string
	}{
		{42, "tok-flood", "PEER_FLOOD target=@flooded"},
		{43, "tok-invalid", "PEER_ID_INVALID target=@invalid"},
	} {
		if _, err := store.CreatePendingDraft(ctx, acquisition.Candidate{UserID: item.userID}, acquisition.Draft{UserID: item.userID, Text: "hello"}, item.token, now); err != nil {
			t.Fatalf("CreatePendingDraft %s: %v", item.token, err)
		}
		if _, _, err := store.MarkDraftOutreachFailed(ctx, item.token, item.reason, now); err != nil {
			t.Fatalf("MarkDraftOutreachFailed %s: %v", item.token, err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = run(ctx, []string{
		"retry-failed",
		"--state", statePath,
		"--all-due",
		"--now", now.Add(13 * time.Hour).Format(time.RFC3339),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run retry-failed dry-run: %v stderr=%s", err, stderr.String())
	}

	var response struct {
		DryRun bool `json:"dryRun"`
		Drafts []struct {
			Token       string `json:"token"`
			FailureKind string `json:"failureKind"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("json output: %v output=%s", err, stdout.String())
	}
	if !response.DryRun {
		t.Fatalf("dryRun=%v, want true", response.DryRun)
	}
	if len(response.Drafts) != 1 || response.Drafts[0].Token != "tok-flood" || response.Drafts[0].FailureKind != acquisition.FailureKindPeerFlood {
		t.Fatalf("response=%+v, want only peer flood retry candidate", response)
	}
	if strings.Contains(stdout.String(), "tok-invalid") {
		t.Fatalf("output=%s, want invalid peer excluded", stdout.String())
	}
}

func TestRetryFailedLoadsStateFromEnvFileBeforeFlagsAreBound(t *testing.T) {
	ctx := context.Background()
	t.Setenv("RS_ACQUISITION_STATE_PATH", "")
	now := time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "campaign.db")
	store, err := acquisition.OpenStore(statePath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	if err := store.UpsertCandidates(ctx, []acquisition.Candidate{{UserID: 42, Username: "flooded", Source: acquisition.SourceRecentActive}}, now); err != nil {
		t.Fatalf("UpsertCandidates: %v", err)
	}
	if _, err := store.CreatePendingDraft(ctx, acquisition.Candidate{UserID: 42}, acquisition.Draft{UserID: 42, Text: "hello"}, "tok-flood", now); err != nil {
		t.Fatalf("CreatePendingDraft: %v", err)
	}
	if _, _, err := store.MarkDraftOutreachFailed(ctx, "tok-flood", "PEER_FLOOD target=@flooded", now); err != nil {
		t.Fatalf("MarkDraftOutreachFailed: %v", err)
	}
	envPath := filepath.Join(tempDir, "campaign.env")
	if err := os.WriteFile(envPath, []byte("RS_ACQUISITION_STATE_PATH="+statePath+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile env: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = run(ctx, []string{
		"retry-failed",
		"--env-file", envPath,
		"--all-due",
		"--now", now.Add(13 * time.Hour).Format(time.RFC3339),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run retry-failed dry-run: %v stderr=%s", err, stderr.String())
	}

	var response struct {
		State  string `json:"state"`
		Drafts []struct {
			Token string `json:"token"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("json output: %v output=%s", err, stdout.String())
	}
	if response.State != statePath {
		t.Fatalf("state=%q, want env-file state path %q", response.State, statePath)
	}
	if len(response.Drafts) != 1 || response.Drafts[0].Token != "tok-flood" {
		t.Fatalf("response=%+v, want env-file backed failed draft", response)
	}
}
