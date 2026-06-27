package acquisition

import (
	"context"
	"testing"
	"time"
)

func TestBuildDailyPlanUsesStoredCountAndDraftsMessages(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	store := openTestStore(t)
	if err := store.UpsertCandidates(ctx, []Candidate{
		{UserID: 1, Username: "one", Source: SourceRecentActive, LastActiveAt: now},
		{UserID: 2, Username: "two", Source: SourceRecentActive, LastActiveAt: now.Add(-time.Minute)},
	}, now); err != nil {
		t.Fatalf("UpsertCandidates: %v", err)
	}
	if err := store.RecordFirstContact(ctx, 1, "already sent", now); err != nil {
		t.Fatalf("RecordFirstContact: %v", err)
	}

	plan, err := BuildDailyPlan(ctx, store, DailyPlanOptions{
		Now:                now,
		Location:           time.UTC,
		DailyLimit:         2,
		DailyRegistrations: 4,
		GroupName:          "Rīgas Zaķi",
	})
	if err != nil {
		t.Fatalf("BuildDailyPlan: %v", err)
	}

	if plan.Day != "2026-05-26" || plan.RemainingFirstContacts != 1 {
		t.Fatalf("plan day/count = %s/%d, want 2026-05-26/1", plan.Day, plan.RemainingFirstContacts)
	}
	if len(plan.Drafts) != 1 || plan.Drafts[0].Username != "two" {
		t.Fatalf("drafts = %+v, want only user two", plan.Drafts)
	}
}
