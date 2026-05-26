package acquisition

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type TokenGenerator func() string

type CandidateCollector interface {
	CollectCandidates(ctx context.Context) ([]Candidate, error)
}

type AdminAction string

const (
	AdminApprove AdminAction = "approve"
	AdminReject  AdminAction = "reject"
)

type AdminDecision struct {
	Token  string      `json:"token"`
	Action AdminAction `json:"action"`
}

type AdminGateway interface {
	SendDraftApproval(ctx context.Context, draft ApprovalDraft) error
	SendAlert(ctx context.Context, text string) error
	PollDecisions(ctx context.Context) ([]AdminDecision, error)
}

type OutreachGateway interface {
	SenderUsername(ctx context.Context) (string, error)
	SendDirect(ctx context.Context, candidate Candidate, text string) error
}

type ContactReply struct {
	UserID    int64     `json:"userId"`
	Username  string    `json:"username,omitempty"`
	MessageID int64     `json:"messageId"`
	Text      string    `json:"text"`
	SentAt    time.Time `json:"sentAt,omitempty"`
}

type ReplySource interface {
	PollReplies(ctx context.Context, candidates []Candidate) ([]ContactReply, error)
}

type DaemonConfig struct {
	Now                func() time.Time
	Location           *time.Location
	DailyLimit         int
	DailyRegistrations int
	GroupName          string
	ExpectedSender     string
}

type CampaignDaemon struct {
	Store     *Store
	Config    DaemonConfig
	Collector CandidateCollector
	Admin     AdminGateway
	Outreach  OutreachGateway
	Replies   ReplySource
	Tokens    TokenGenerator
}

type DaemonRunResult struct {
	CandidatesCollected int `json:"candidatesCollected"`
	DraftsCreated       int `json:"draftsCreated"`
	DecisionsProcessed  int `json:"decisionsProcessed"`
	MessagesSent        int `json:"messagesSent"`
	RepliesProcessed    int `json:"repliesProcessed"`
}

func (d CampaignDaemon) RunOnce(ctx context.Context) (DaemonRunResult, error) {
	if d.Store == nil {
		return DaemonRunResult{}, fmt.Errorf("campaign store is required")
	}
	if d.Admin == nil {
		return DaemonRunResult{}, fmt.Errorf("admin gateway is required")
	}
	if d.Outreach == nil {
		return DaemonRunResult{}, fmt.Errorf("outreach gateway is required")
	}
	now := d.now()
	loc := d.location()
	day := DayKey(now, loc)
	limit := d.dailyLimit()

	if err := d.verifySender(ctx); err != nil {
		return DaemonRunResult{}, err
	}

	result := DaemonRunResult{}
	if d.Collector != nil {
		candidates, err := d.Collector.CollectCandidates(ctx)
		if err != nil {
			return DaemonRunResult{}, err
		}
		result.CandidatesCollected = len(candidates)
		if err := d.Store.UpsertCandidates(ctx, candidates, now); err != nil {
			return DaemonRunResult{}, err
		}
	}

	created, err := d.createDraftApprovals(ctx, now, day, limit)
	if err != nil {
		return DaemonRunResult{}, err
	}
	result.DraftsCreated = created

	decisions, err := d.Admin.PollDecisions(ctx)
	if err != nil {
		return DaemonRunResult{}, err
	}
	for _, decision := range decisions {
		processed, sent, err := d.processDecision(ctx, decision, now, day, limit)
		if err != nil {
			return DaemonRunResult{}, err
		}
		if processed {
			result.DecisionsProcessed++
		}
		if sent {
			result.MessagesSent++
		}
	}

	if d.Replies != nil {
		candidates, err := d.Store.ContactedCandidates(ctx)
		if err != nil {
			return DaemonRunResult{}, err
		}
		replies, err := d.Replies.PollReplies(ctx, candidates)
		if err != nil {
			return DaemonRunResult{}, err
		}
		for _, reply := range replies {
			processed, err := d.processReply(ctx, reply, now)
			if err != nil {
				return DaemonRunResult{}, err
			}
			if processed {
				result.RepliesProcessed++
			}
		}
	}

	return result, nil
}

func (d CampaignDaemon) createDraftApprovals(ctx context.Context, now time.Time, day string, limit int) (int, error) {
	count, err := d.Store.DailyFirstContactCount(ctx, day)
	if err != nil {
		return 0, err
	}
	batch, err := d.Store.NextDailyBatch(ctx, BatchOptions{Now: now, AlreadyContactedToday: count, DailyLimit: limit})
	if err != nil {
		return 0, err
	}
	created := 0
	for _, candidate := range batch {
		if _, found, err := d.Store.PendingDraftForUser(ctx, candidate.UserID); err != nil {
			return created, err
		} else if found {
			continue
		}
		draft := DraftFirstContact(candidate, DraftOptions{
			DailyRegistrations: d.dailyRegistrations(),
			GroupName:          d.Config.GroupName,
		})
		approval, err := d.Store.CreatePendingDraft(ctx, candidate, draft, d.token(), now)
		if err != nil {
			return created, err
		}
		if err := d.Admin.SendDraftApproval(ctx, approval); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func (d CampaignDaemon) processDecision(ctx context.Context, decision AdminDecision, now time.Time, day string, limit int) (bool, bool, error) {
	token := strings.TrimSpace(decision.Token)
	if token == "" {
		return false, false, nil
	}
	switch decision.Action {
	case AdminReject:
		return true, false, d.Store.MarkDraftRejected(ctx, token, now)
	case AdminApprove:
		draft, found, err := d.Store.PendingDraftByToken(ctx, token)
		if err != nil || !found {
			return found, false, err
		}
		count, err := d.Store.DailyFirstContactCount(ctx, day)
		if err != nil {
			return true, false, err
		}
		if count >= limit {
			return true, false, d.Admin.SendAlert(ctx, fmt.Sprintf("RS acquisition daily limit reached for %s; leaving draft %s pending.", day, token))
		}
		candidate, found, err := d.Store.Candidate(ctx, draft.UserID)
		if err != nil || !found {
			return found, false, err
		}
		if err := d.Outreach.SendDirect(ctx, candidate, draft.Text); err != nil {
			return true, false, err
		}
		if _, sent, err := d.Store.MarkDraftSent(ctx, token, day, now); err != nil || !sent {
			return true, false, err
		}
		return true, true, d.Admin.SendAlert(ctx, fmt.Sprintf("RS acquisition first contact sent to @%s.", candidate.Username))
	default:
		return false, false, nil
	}
}

func (d CampaignDaemon) processReply(ctx context.Context, reply ContactReply, now time.Time) (bool, error) {
	outcome, processed, err := d.Store.RecordContactReply(ctx, reply, now)
	if err != nil || !processed {
		return processed, err
	}
	switch outcome.Decision.Action {
	case ReplyConsent:
		return true, d.Admin.SendAlert(ctx, fmt.Sprintf("RS acquisition consent from %d. Grant access with: %s", reply.UserID, outcome.GrantCommand))
	case ReplyDecline:
		return true, d.Admin.SendAlert(ctx, fmt.Sprintf("RS acquisition user %d declined or opted out.", reply.UserID))
	case ReplyUnsafeStop:
		return true, d.Admin.SendAlert(ctx, fmt.Sprintf("RS acquisition stopped contact %d: %s", reply.UserID, outcome.Decision.Reason))
	default:
		return true, nil
	}
}

func (d CampaignDaemon) verifySender(ctx context.Context) error {
	expected := cleanUsername(d.Config.ExpectedSender)
	if expected == "" {
		return fmt.Errorf("expected sender username is required")
	}
	actual, err := d.Outreach.SenderUsername(ctx)
	if err != nil {
		return err
	}
	if cleanUsername(actual) != expected {
		return fmt.Errorf("sender session is @%s, want @%s", cleanUsername(actual), expected)
	}
	return nil
}

func (d CampaignDaemon) now() time.Time {
	if d.Config.Now != nil {
		return d.Config.Now().UTC()
	}
	return time.Now().UTC()
}

func (d CampaignDaemon) location() *time.Location {
	if d.Config.Location != nil {
		return d.Config.Location
	}
	return time.UTC
}

func (d CampaignDaemon) dailyLimit() int {
	if d.Config.DailyLimit > 0 {
		return d.Config.DailyLimit
	}
	return 10
}

func (d CampaignDaemon) dailyRegistrations() int {
	if d.Config.DailyRegistrations > 0 {
		return d.Config.DailyRegistrations
	}
	return 4
}

func (d CampaignDaemon) token() string {
	if d.Tokens != nil {
		return d.Tokens()
	}
	return randomToken()
}

func randomToken() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("draft-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
