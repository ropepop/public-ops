package acquisition

import (
	"context"
	"time"
)

type DailyPlanOptions struct {
	Now                time.Time
	Location           *time.Location
	DailyLimit         int
	DailyRegistrations int
	GroupName          string
}

type DailyPlan struct {
	Day                    string  `json:"day"`
	DailyLimit             int     `json:"dailyLimit"`
	AlreadyContactedToday  int     `json:"alreadyContactedToday"`
	RemainingFirstContacts int     `json:"remainingFirstContacts"`
	Drafts                 []Draft `json:"drafts"`
}

func BuildDailyPlan(ctx context.Context, store *Store, opts DailyPlanOptions) (DailyPlan, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	loc := opts.Location
	if loc == nil {
		loc = time.UTC
	}
	limit := opts.DailyLimit
	if limit <= 0 {
		limit = 10
	}
	day := DayKey(now, loc)
	count, err := store.DailyFirstContactCount(ctx, day)
	if err != nil {
		return DailyPlan{}, err
	}
	batch, err := store.NextDailyBatch(ctx, BatchOptions{Now: now, AlreadyContactedToday: count, DailyLimit: limit})
	if err != nil {
		return DailyPlan{}, err
	}
	drafts := make([]Draft, 0, len(batch))
	for _, candidate := range batch {
		drafts = append(drafts, DraftFirstContact(candidate, DraftOptions{
			DailyRegistrations: opts.DailyRegistrations,
			GroupName:          opts.GroupName,
		}))
	}
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	if remaining > len(drafts) {
		remaining = len(drafts)
	}
	return DailyPlan{
		Day:                    day,
		DailyLimit:             limit,
		AlreadyContactedToday:  count,
		RemainingFirstContacts: remaining,
		Drafts:                 drafts,
	}, nil
}
