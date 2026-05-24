package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	JobWaiting   = "waiting"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCanceled  = "canceled"
)

const rigasSatiksmeGeneratedScreenshotCropPercent = 5

const (
	defaultTicketGrace        = 10 * time.Second
	defaultMaxTicketQRBlock   = 15 * time.Second
	defaultRunnerInterval     = 500 * time.Millisecond
	defaultPhoneSendTimeout   = 2 * time.Second
	defaultJobTimeout         = 75 * time.Second
	defaultImageTTL           = 2 * time.Minute
	defaultTicketLeaseTTL     = 45 * time.Second
	minTicketLeaseTTL         = 3 * time.Second
	maxTicketLeaseTTL         = 3 * time.Minute
	maxRecoverableJobAttempts = 3

	controlCodeHealthPollInterval   = 250 * time.Millisecond
	controlCodeDisconnectResultWait = 12 * time.Second
	healthUpstreamProbeTimeout      = 250 * time.Millisecond
	rigasSatiksmeBatchBurstWindow   = 250 * time.Millisecond
	rigasSatiksmeBatchMaxJobs       = 3

	websocketProxyReadLimitBytes = 8 << 20

	expectedRigasSatiksmeSourceApp  = "com.flutter.rspassenger"
	expectedRigasSatiksmeTicketFlow = "rigas_satiksme_android_monthly_ticket_control"

	rsQRSlowSuccessThresholdSeconds = 15
)

var qrCodePattern = regexp.MustCompile(`^[0-9]{5}$`)

type Config struct {
	BindAddr         string
	Port             int
	UpstreamBaseURL  string
	StatePath        string
	TicketGrace      time.Duration
	MaxTicketQRBlock time.Duration
	RunnerInterval   time.Duration
	PhoneSendTimeout time.Duration
	JobTimeout       time.Duration
	ImageTTL         time.Duration
}

type Broker struct {
	cfg Config

	mu                    sync.Mutex
	jobs                  map[string]*QRJob
	order                 []string
	images                map[string]QRImage
	ticketViewers         int
	ticketSockets         int
	ticketGraceUntil      time.Time
	ticketQRBlockUntil    time.Time
	ticketLeases          map[string]ticketLease
	lastPreemptionReason  string
	lastPreemptionAt      time.Time
	runningJobID          string
	runningJobIDs         []string
	runningBatchID        string
	runningCancel         context.CancelFunc
	runningControl        *websocket.Conn
	runningControlWriteMu sync.Mutex
	wake                  chan struct{}
}

type QRJobInput struct {
	ChatID string
	UserID string
	Code   string
	Now    time.Time
}

type TicketPresenceInput struct {
	Viewers int
	Now     time.Time
}

type TicketLeaseInput struct {
	LeaseID   string
	RequestID string
	Reason    string
	TTL       time.Duration
	Now       time.Time
}

type ticketLease struct {
	ID         string
	RequestID  string
	Reason     string
	AcquiredAt time.Time
	UpdatedAt  time.Time
	ExpiresAt  time.Time
}

type TicketLeaseSnapshot struct {
	ID                   string `json:"id"`
	Owner                string `json:"owner"`
	RequestID            string `json:"requestId,omitempty"`
	Reason               string `json:"reason,omitempty"`
	AcquiredAt           string `json:"acquiredAt,omitempty"`
	UpdatedAt            string `json:"updatedAt,omitempty"`
	ExpiresAt            string `json:"expiresAt,omitempty"`
	RemainingMillis      int64  `json:"remainingMillis,omitempty"`
	BlockedJobs          int    `json:"blockedJobs,omitempty"`
	LastPreemptionReason string `json:"lastPreemptionReason,omitempty"`
	LastPreemptionAt     string `json:"lastPreemptionAt,omitempty"`
}

type QRJob struct {
	ID          string           `json:"id"`
	ChatID      string           `json:"chatId"`
	UserID      string           `json:"userId"`
	Code        string           `json:"code,omitempty"`
	Status      string           `json:"status"`
	Reason      string           `json:"reason,omitempty"`
	Attempts    int              `json:"attempts"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
	StartedAt   string           `json:"startedAt,omitempty"`
	CompletedAt string           `json:"completedAt,omitempty"`
	Phone       RSQRPhoneSummary `json:"phone,omitempty"`
}

type publicQRJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	Attempts    int    `json:"attempts"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

type QRImage struct {
	MIME      string
	Bytes     []byte
	ExpiresAt time.Time
}

type upstreamControlCodeRequest struct {
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Value     string `json:"value"`
}

type upstreamRigasSatiksmeBatch struct {
	BatchID             string           `json:"batchId"`
	Status              string           `json:"status"`
	ActiveRequestID     string           `json:"activeRequestId"`
	LastResultRequestID string           `json:"lastResultRequestId"`
	LastResultStatus    string           `json:"lastResultStatus"`
	LastResultReason    string           `json:"lastResultReason"`
	Phases              map[string]int64 `json:"phases"`
}

type upstreamHealth struct {
	ControlCodeRequest upstreamControlCodeRequest `json:"controlCodeRequest"`
	RigasSatiksmeBatch upstreamRigasSatiksmeBatch `json:"rigasSatiksmeBatch"`
}

type StateSnapshot struct {
	CurrentOwner         string               `json:"currentOwner"`
	DesiredOwner         string               `json:"desiredOwner"`
	DesiredPriority      []string             `json:"desiredPriority,omitempty"`
	ActiveLease          *TicketLeaseSnapshot `json:"activeLease,omitempty"`
	LeaseReason          string               `json:"leaseReason,omitempty"`
	LeaseRequestID       string               `json:"leaseRequestId,omitempty"`
	BlockedJobs          int                  `json:"blockedJobs,omitempty"`
	LastPreemptionReason string               `json:"lastPreemptionReason,omitempty"`
	LastPreemptionAt     string               `json:"lastPreemptionAt,omitempty"`
	TicketViewers        int                  `json:"ticketViewers"`
	TicketSockets        int                  `json:"ticketSockets"`
	TicketActive         bool                 `json:"ticketActive"`
	QueueDepth           int                  `json:"queueDepth"`
	RunningQRJob         bool                 `json:"runningQRJob,omitempty"`
	RunningJobID         string               `json:"-"`
	Jobs                 []StateJob           `json:"jobs,omitempty"`
}

type StateJob struct {
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

type AnalyticsSnapshot struct {
	Schema      string        `json:"schema"`
	GeneratedAt string        `json:"generatedAt"`
	RSQR        RSQRAnalytics `json:"rsQr"`
}

type RSQRAnalytics struct {
	Totals            RSQRTotals       `json:"totals"`
	ByReason          map[string]int   `json:"byReason,omitempty"`
	SuccessLatencySec LatencyStats     `json:"successLatencySec,omitempty"`
	UserImpact        []RSQRUserImpact `json:"userImpact,omitempty"`
	RecentIncidents   []RSQRIncident   `json:"recentIncidents,omitempty"`
}

type RSQRTotals struct {
	Jobs        int `json:"jobs"`
	Waiting     int `json:"waiting,omitempty"`
	Running     int `json:"running,omitempty"`
	Succeeded   int `json:"succeeded,omitempty"`
	Failed      int `json:"failed,omitempty"`
	Canceled    int `json:"canceled,omitempty"`
	Retried     int `json:"retried,omitempty"`
	SlowSuccess int `json:"slowSuccess,omitempty"`
}

type LatencyStats struct {
	Count int     `json:"count"`
	Min   float64 `json:"min,omitempty"`
	P50   float64 `json:"p50,omitempty"`
	P90   float64 `json:"p90,omitempty"`
	Max   float64 `json:"max,omitempty"`
}

type RSQRUserImpact struct {
	ActorHash   string `json:"actorHash"`
	Jobs        int    `json:"jobs"`
	Waiting     int    `json:"waiting,omitempty"`
	Running     int    `json:"running,omitempty"`
	Succeeded   int    `json:"succeeded,omitempty"`
	Failed      int    `json:"failed,omitempty"`
	Canceled    int    `json:"canceled,omitempty"`
	Retried     int    `json:"retried,omitempty"`
	SlowSuccess int    `json:"slowSuccess,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastReason  string `json:"lastReason,omitempty"`
	LastAt      string `json:"lastAt,omitempty"`
}

type RSQRIncident struct {
	TraceID         string           `json:"traceId"`
	Seq             int              `json:"seq"`
	ActorHash       string           `json:"actorHash,omitempty"`
	Status          string           `json:"status"`
	Reason          string           `json:"reason,omitempty"`
	Attempts        int              `json:"attempts,omitempty"`
	CreatedAt       string           `json:"createdAt,omitempty"`
	StartedAt       string           `json:"startedAt,omitempty"`
	CompletedAt     string           `json:"completedAt,omitempty"`
	TotalSec        float64          `json:"totalSec,omitempty"`
	QueueSec        float64          `json:"queueSec,omitempty"`
	FinalAttemptSec float64          `json:"finalAttemptSec,omitempty"`
	Retried         bool             `json:"retried,omitempty"`
	SlowSuccess     bool             `json:"slowSuccess,omitempty"`
	Phone           RSQRPhoneSummary `json:"phone,omitempty"`
}

type RSQRPhoneSummary struct {
	SourceApp           string           `json:"sourceApp,omitempty"`
	TicketFlow          string           `json:"ticketFlow,omitempty"`
	TotalDurationMillis int64            `json:"totalDurationMillis,omitempty"`
	Phases              map[string]int64 `json:"phases,omitempty"`
}

type persistedState struct {
	Jobs []QRJob `json:"jobs"`
}

func New(cfg Config) (*Broker, error) {
	cfg.UpstreamBaseURL = strings.TrimRight(strings.TrimSpace(cfg.UpstreamBaseURL), "/")
	if cfg.UpstreamBaseURL == "" {
		return nil, fmt.Errorf("upstream base URL is required")
	}
	if _, err := url.Parse(cfg.UpstreamBaseURL); err != nil {
		return nil, fmt.Errorf("parse upstream base URL: %w", err)
	}
	if cfg.TicketGrace <= 0 {
		cfg.TicketGrace = defaultTicketGrace
	}
	if cfg.MaxTicketQRBlock <= 0 {
		cfg.MaxTicketQRBlock = defaultMaxTicketQRBlock
	}
	if cfg.RunnerInterval <= 0 {
		cfg.RunnerInterval = defaultRunnerInterval
	}
	if cfg.PhoneSendTimeout <= 0 {
		cfg.PhoneSendTimeout = defaultPhoneSendTimeout
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = defaultJobTimeout
	}
	if cfg.ImageTTL <= 0 {
		cfg.ImageTTL = defaultImageTTL
	}
	b := &Broker{
		cfg:          cfg,
		jobs:         map[string]*QRJob{},
		images:       map[string]QRImage{},
		ticketLeases: map[string]ticketLease{},
		wake:         make(chan struct{}, 1),
	}
	if err := b.load(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Broker) Run(ctx context.Context) {
	ticker := time.NewTicker(b.cfg.RunnerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
			b.tick(ctx)
		case <-ticker.C:
			b.tick(ctx)
		}
	}
}

func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", b.handleHealth)
	mux.HandleFunc("/api/v1/state", b.handleState)
	mux.HandleFunc("/api/v1/analytics", b.handleAnalytics)
	mux.HandleFunc("/api/v1/ticket/presence", b.handleTicketPresence)
	mux.HandleFunc("/api/v1/phone/leases/ticket", b.handleTicketLease)
	mux.HandleFunc("/api/v1/phone/leases/ticket/release", b.handleTicketLeaseRelease)
	mux.HandleFunc("/api/v1/qr/jobs", b.handleQRJobs)
	mux.HandleFunc("/api/v1/qr/jobs/", b.handleQRJob)
	mux.HandleFunc("/api/v1/session/start", b.proxyHTTP)
	mux.HandleFunc("/api/v1/session/stop", b.proxyHTTP)
	mux.HandleFunc("/api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		b.proxyWebsocket(w, r, "/api/v1/session")
	})
	mux.HandleFunc("/api/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		b.proxyWebsocket(w, r, "/api/v1/stream")
	})
	return mux
}

func (b *Broker) EnqueueQRJob(ctx context.Context, input QRJobInput) (QRJob, error) {
	code := strings.TrimSpace(input.Code)
	if !qrCodePattern.MatchString(code) {
		return QRJob{}, fmt.Errorf("code must contain exactly 5 digits")
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	job := &QRJob{
		ID:        randomID(),
		ChatID:    strings.TrimSpace(input.ChatID),
		UserID:    strings.TrimSpace(input.UserID),
		Code:      code,
		Status:    JobWaiting,
		CreatedAt: nowText,
		UpdatedAt: nowText,
	}
	b.mu.Lock()
	b.jobs[job.ID] = job
	b.order = append(b.order, job.ID)
	err := b.saveLocked()
	out := cloneJob(*job)
	b.mu.Unlock()
	b.signalRunner()
	return out, err
}

func (b *Broker) UpdateTicketPresence(ctx context.Context, input TicketPresenceInput) error {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	b.mu.Lock()
	if input.Viewers < 0 {
		input.Viewers = 0
	}
	previousViewers := b.ticketViewers
	b.ticketViewers = input.Viewers
	if input.Viewers > 0 {
		b.ticketGraceUntil = time.Time{}
		if previousViewers == 0 || input.Viewers > previousViewers || b.ticketQRBlockUntil.IsZero() {
			b.ticketQRBlockUntil = now.Add(b.cfg.MaxTicketQRBlock)
		}
		if b.ticketBlocksQRLocked(now) {
			b.preemptRunningLocked(now, "ticket_active")
		}
	} else if b.ticketSockets == 0 {
		b.ticketQRBlockUntil = time.Time{}
		b.ticketGraceUntil = now.Add(b.cfg.TicketGrace)
	}
	err := b.saveLocked()
	b.mu.Unlock()
	b.signalRunner()
	return err
}

func (b *Broker) AcquireTicketLease(ctx context.Context, input TicketLeaseInput) (TicketLeaseSnapshot, error) {
	_ = ctx
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	leaseID := strings.TrimSpace(input.LeaseID)
	if leaseID == "" {
		leaseID = "ticket:" + randomID()
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "ticket_active"
	}
	ttl := normalizeTicketLeaseTTL(input.TTL)
	lease := ticketLease{
		ID:         leaseID,
		RequestID:  strings.TrimSpace(input.RequestID),
		Reason:     reason,
		AcquiredAt: now.UTC(),
		UpdatedAt:  now.UTC(),
		ExpiresAt:  now.Add(ttl).UTC(),
	}
	b.mu.Lock()
	if b.ticketLeases == nil {
		b.ticketLeases = map[string]ticketLease{}
	}
	b.pruneExpiredTicketLeasesLocked(now)
	if previous, ok := b.ticketLeases[leaseID]; ok && !previous.AcquiredAt.IsZero() {
		lease.AcquiredAt = previous.AcquiredAt
	}
	b.ticketLeases[leaseID] = lease
	if b.runningJobID != "" {
		b.preemptRunningLocked(now, "ticket_lease_active")
	}
	blockedJobs := b.queueDepthLocked()
	snapshot := ticketLeaseSnapshot(lease, now, blockedJobs, b.lastPreemptionReason, b.lastPreemptionAt)
	b.mu.Unlock()
	b.signalRunner()
	return snapshot, nil
}

func (b *Broker) ReleaseTicketLease(ctx context.Context, input TicketLeaseInput) error {
	_ = ctx
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	leaseID := strings.TrimSpace(input.LeaseID)
	requestID := strings.TrimSpace(input.RequestID)
	b.mu.Lock()
	b.pruneExpiredTicketLeasesLocked(now)
	if leaseID != "" {
		delete(b.ticketLeases, leaseID)
	} else if requestID != "" {
		for id, lease := range b.ticketLeases {
			if strings.TrimSpace(lease.RequestID) == requestID {
				delete(b.ticketLeases, id)
			}
		}
	}
	b.mu.Unlock()
	b.signalRunner()
	return nil
}

func (b *Broker) Job(id string) (QRJob, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	job := b.jobs[strings.TrimSpace(id)]
	if job == nil {
		return QRJob{}, false
	}
	return cloneJob(*job), true
}

func (b *Broker) LatestJob(userID string) (QRJob, bool) {
	userID = strings.TrimSpace(userID)
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.order) - 1; i >= 0; i-- {
		job := b.jobs[b.order[i]]
		if job != nil && job.UserID == userID {
			return cloneJob(*job), true
		}
	}
	return QRJob{}, false
}

func (b *Broker) CancelLatestJob(ctx context.Context, userID string, now time.Time) (QRJob, bool, error) {
	if now.IsZero() {
		now = time.Now()
	}
	userID = strings.TrimSpace(userID)
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.order) - 1; i >= 0; i-- {
		job := b.jobs[b.order[i]]
		if job == nil || job.UserID != userID {
			continue
		}
		if job.Status == JobSucceeded || job.Status == JobFailed || job.Status == JobCanceled {
			return cloneJob(*job), true, nil
		}
		job.Status = JobCanceled
		job.Reason = "user_canceled"
		job.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		job.CompletedAt = job.UpdatedAt
		if b.isRunningJobLocked(job.ID) {
			b.sendCancelToRunningLocked("user_canceled")
			if b.runningCancel != nil {
				b.runningCancel()
			}
			b.runningJobID = ""
			b.runningJobIDs = nil
			b.runningBatchID = ""
			b.runningCancel = nil
			b.runningControl = nil
		}
		err := b.saveLocked()
		return cloneJob(*job), true, err
	}
	return QRJob{}, false, nil
}

func (b *Broker) JobImage(id string) (QRImage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	image, ok := b.images[strings.TrimSpace(id)]
	if !ok {
		return QRImage{}, false
	}
	if !image.ExpiresAt.IsZero() && time.Now().After(image.ExpiresAt) {
		delete(b.images, id)
		return QRImage{}, false
	}
	image.Bytes = append([]byte(nil), image.Bytes...)
	return image, true
}

func (b *Broker) Snapshot(now time.Time) StateSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneExpiredTicketLeasesLocked(now)
	jobs := make([]StateJob, 0, len(b.order))
	for _, id := range b.order {
		if job := b.jobs[id]; job != nil {
			jobs = append(jobs, stateJobFromQRJob(*job))
		}
	}
	queueDepth := b.queueDepthLocked()
	desiredOwner, desiredPriority := b.desiredPriorityLocked(now, queueDepth)
	blockedJobs := 0
	if b.ticketBlocksQRLocked(now) {
		blockedJobs = queueDepth
	}
	activeLease := b.activeTicketLeaseSnapshotLocked(now, blockedJobs)
	leaseReason := ""
	leaseRequestID := ""
	if activeLease != nil {
		leaseReason = activeLease.Reason
		leaseRequestID = activeLease.RequestID
	}
	lastPreemptionAt := ""
	if !b.lastPreemptionAt.IsZero() {
		lastPreemptionAt = b.lastPreemptionAt.UTC().Format(time.RFC3339Nano)
	}
	return StateSnapshot{
		CurrentOwner:         b.currentOwnerLocked(now),
		DesiredOwner:         desiredOwner,
		DesiredPriority:      desiredPriority,
		ActiveLease:          activeLease,
		LeaseReason:          leaseReason,
		LeaseRequestID:       leaseRequestID,
		BlockedJobs:          blockedJobs,
		LastPreemptionReason: b.lastPreemptionReason,
		LastPreemptionAt:     lastPreemptionAt,
		TicketViewers:        b.ticketViewers,
		TicketSockets:        b.ticketSockets,
		TicketActive:         b.ticketActiveLocked(now),
		QueueDepth:           queueDepth,
		RunningQRJob:         b.runningJobID != "" || len(b.runningJobIDs) > 0,
		RunningJobID:         b.runningJobID,
		Jobs:                 jobs,
	}
}

func (b *Broker) Analytics(now time.Time) AnalyticsSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	type orderedJob struct {
		Seq int
		Job QRJob
	}
	b.mu.Lock()
	jobs := make([]orderedJob, 0, len(b.order))
	for index, id := range b.order {
		if job := b.jobs[id]; job != nil {
			jobs = append(jobs, orderedJob{Seq: index + 1, Job: cloneJob(*job)})
		}
	}
	b.mu.Unlock()

	analytics := AnalyticsSnapshot{
		Schema:      "rs-vivi-qr-analytics.v1",
		GeneratedAt: now.UTC().Format(time.RFC3339Nano),
		RSQR: RSQRAnalytics{
			ByReason: map[string]int{},
		},
	}
	latencies := make([]float64, 0, len(jobs))
	impactByActor := map[string]*RSQRUserImpact{}
	for _, item := range jobs {
		job := item.Job
		actor := actorHash(job.UserID, job.ChatID)
		analytics.RSQR.Totals.Jobs++
		switch job.Status {
		case JobWaiting:
			analytics.RSQR.Totals.Waiting++
		case JobRunning:
			analytics.RSQR.Totals.Running++
		case JobSucceeded:
			analytics.RSQR.Totals.Succeeded++
		case JobFailed:
			analytics.RSQR.Totals.Failed++
		case JobCanceled:
			analytics.RSQR.Totals.Canceled++
		}
		if reason := strings.TrimSpace(job.Reason); reason != "" {
			analytics.RSQR.ByReason[reason]++
		}
		if job.Attempts > 1 {
			analytics.RSQR.Totals.Retried++
		}
		totalSec, queueSec, finalAttemptSec := qrJobTimingSeconds(job, now)
		if job.Status == JobSucceeded && totalSec > 0 {
			latencies = append(latencies, totalSec)
		}
		slowSuccess := job.Status == JobSucceeded && totalSec >= rsQRSlowSuccessThresholdSeconds
		if slowSuccess {
			analytics.RSQR.Totals.SlowSuccess++
		}
		if actor != "" {
			impact := impactByActor[actor]
			if impact == nil {
				impact = &RSQRUserImpact{ActorHash: actor}
				impactByActor[actor] = impact
			}
			impact.Jobs++
			switch job.Status {
			case JobWaiting:
				impact.Waiting++
			case JobRunning:
				impact.Running++
			case JobSucceeded:
				impact.Succeeded++
			case JobFailed:
				impact.Failed++
			case JobCanceled:
				impact.Canceled++
			}
			if job.Attempts > 1 {
				impact.Retried++
			}
			if slowSuccess {
				impact.SlowSuccess++
			}
			if lastAt := qrJobLastAt(job); lastAt != "" && (impact.LastAt == "" || lastAt > impact.LastAt) {
				impact.LastAt = lastAt
				impact.LastStatus = job.Status
				impact.LastReason = strings.TrimSpace(job.Reason)
			}
		}
		if job.Status == JobFailed || job.Status == JobCanceled || job.Status == JobRunning || job.Attempts > 1 || slowSuccess {
			analytics.RSQR.RecentIncidents = append(analytics.RSQR.RecentIncidents, RSQRIncident{
				TraceID:         fmt.Sprintf("rsqr:%06d", item.Seq),
				Seq:             item.Seq,
				ActorHash:       actor,
				Status:          job.Status,
				Reason:          strings.TrimSpace(job.Reason),
				Attempts:        job.Attempts,
				CreatedAt:       job.CreatedAt,
				StartedAt:       job.StartedAt,
				CompletedAt:     job.CompletedAt,
				TotalSec:        totalSec,
				QueueSec:        queueSec,
				FinalAttemptSec: finalAttemptSec,
				Retried:         job.Attempts > 1,
				SlowSuccess:     slowSuccess,
				Phone:           cloneRSQRPhoneSummary(job.Phone),
			})
		}
	}
	if len(analytics.RSQR.RecentIncidents) > 20 {
		analytics.RSQR.RecentIncidents = analytics.RSQR.RecentIncidents[len(analytics.RSQR.RecentIncidents)-20:]
	}
	analytics.RSQR.UserImpact = userImpactList(impactByActor)
	analytics.RSQR.SuccessLatencySec = latencyStats(latencies)
	return analytics
}

func (b *Broker) tick(parent context.Context) {
	now := time.Now()
	var jobs []QRJob
	var cancel context.CancelFunc
	var runCtx context.Context
	b.mu.Lock()
	if b.runningJobID != "" || len(b.runningJobIDs) > 0 || b.ticketBlocksQRLocked(now) {
		b.mu.Unlock()
		return
	}
	jobs = b.selectRigasSatiksmeBatchLocked(now)
	if len(jobs) > 0 {
		startedAt := now.UTC().Format(time.RFC3339Nano)
		ids := make([]string, 0, len(jobs))
		for _, selected := range jobs {
			candidate := b.jobs[selected.ID]
			if candidate == nil || candidate.Status != JobWaiting {
				continue
			}
			candidate.Status = JobRunning
			previousReason := strings.TrimSpace(candidate.Reason)
			// Ticket-priority preemption pauses the RS job; it should not consume the
			// retry budget reserved for real phone/app result failures.
			if !isTicketPriorityPreemptionReason(previousReason) || candidate.Attempts == 0 {
				candidate.Attempts++
			}
			candidate.Reason = ""
			candidate.StartedAt = startedAt
			candidate.UpdatedAt = startedAt
			ids = append(ids, candidate.ID)
		}
		var runCancel context.CancelFunc
		runCtx, runCancel = context.WithCancel(parent)
		cancel = runCancel
		b.runningJobIDs = ids
		if len(ids) > 0 {
			b.runningJobID = ids[0]
			b.runningBatchID = "rsbatch-" + randomID()
		}
		b.runningCancel = runCancel
		_ = b.saveLocked()
	}
	b.mu.Unlock()
	if len(jobs) == 0 {
		return
	}
	go b.runQRBatch(runCtx, jobs, cancel)
}

func (b *Broker) selectRigasSatiksmeBatchLocked(now time.Time) []QRJob {
	var selected []QRJob
	var firstWaitingCreated time.Time
	for _, id := range b.order {
		job := b.jobs[id]
		if job == nil || job.Status != JobWaiting {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339Nano, job.CreatedAt)
		if err != nil {
			createdAt = now
		}
		if len(selected) == 0 {
			firstWaitingCreated = createdAt
		}
		selected = append(selected, cloneJob(*job))
		if len(selected) >= rigasSatiksmeBatchMaxJobs {
			break
		}
	}
	if len(selected) == 0 {
		return nil
	}
	if len(selected) < rigasSatiksmeBatchMaxJobs && now.Sub(firstWaitingCreated) < rigasSatiksmeBatchBurstWindow {
		return nil
	}
	return selected
}

func (b *Broker) runQRBatch(runCtx context.Context, jobs []QRJob, cancel context.CancelFunc) {
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(runCtx, b.cfg.JobTimeout)
	defer timeoutCancel()

	conn, err := b.openQRPhoneBatchControl(ctx, jobs)
	if err != nil {
		for _, job := range jobs {
			b.finishRunningJob(job.ID, false, "phone_unavailable", "", nil)
		}
		return
	}
	defer func() {
		if conn != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "batch finished")
		}
	}()

	type phoneReadResult struct {
		msgType websocket.MessageType
		data    []byte
		err     error
	}
	startPhoneRead := func(readConn *websocket.Conn) <-chan phoneReadResult {
		readDone := make(chan phoneReadResult, 1)
		go func() {
			msgType, data, err := readConn.Read(context.Background())
			readDone <- phoneReadResult{msgType: msgType, data: data, err: err}
		}()
		return readDone
	}

	pending := make(map[string]QRJob, len(jobs))
	jobOrder := make([]string, 0, len(jobs))
	for _, job := range jobs {
		pending[job.ID] = job
		jobOrder = append(jobOrder, job.ID)
	}
	pendingJobs := func() []QRJob {
		out := make([]QRJob, 0, len(pending))
		for _, id := range jobOrder {
			if job, ok := pending[id]; ok {
				out = append(out, job)
			}
		}
		return out
	}
	readConn := conn
	readDone := startPhoneRead(readConn)
	for len(pending) > 0 {
		var result phoneReadResult
		select {
		case <-ctx.Done():
			stillRunning := make([]string, 0, len(pending))
			b.mu.Lock()
			for id := range pending {
				current := b.jobs[id]
				status := ""
				if current != nil {
					status = current.Status
				}
				if status != JobWaiting && status != JobCanceled {
					stillRunning = append(stillRunning, id)
				}
			}
			b.mu.Unlock()
			if len(stillRunning) == 0 {
				return
			}
			b.sendCancelBatchToConn(readConn, b.currentRunningBatchID(), stillRunning, "job_timeout")
			for _, id := range stillRunning {
				b.finishRunningJob(id, false, "phone_timeout", "", nil)
			}
			_ = readConn.Close(websocket.StatusNormalClosure, "batch timeout")
			return
		case result = <-readDone:
		}

		msgType, data, err := result.msgType, result.data, result.err
		if err != nil {
			select {
			case <-runCtx.Done():
				return
			default:
			}
			for id := range pending {
				if b.finishFromUpstreamRigasSatiksmeBatchHealthOnce(ctx, id) {
					delete(pending, id)
				}
			}
			if len(pending) == 0 {
				return
			}
			select {
			case <-ctx.Done():
				continue
			case <-time.After(500 * time.Millisecond):
			}
			_ = readConn.Close(websocket.StatusInternalError, "qr batch control reconnect")
			nextConn, reconnectErr := b.openQRPhoneBatchControl(ctx, pendingJobs())
			if reconnectErr != nil {
				readConn = conn
				readDone = startPhoneRead(readConn)
				continue
			}
			conn = nextConn
			readConn = nextConn
			readDone = startPhoneRead(readConn)
			continue
		}
		if msgType != websocket.MessageText {
			readDone = startPhoneRead(readConn)
			continue
		}
		var payload rigasSatiksmeQRPhoneMessage
		if err := json.Unmarshal(data, &payload); err != nil {
			readDone = startPhoneRead(readConn)
			continue
		}
		requestID := strings.TrimSpace(payload.RequestID)
		if _, ok := pending[requestID]; !ok {
			readDone = startPhoneRead(readConn)
			continue
		}
		decision := evaluateRigasSatiksmeQRPhoneMessage(payload)
		if !decision.Final {
			readDone = startPhoneRead(readConn)
			continue
		}
		if decision.OK {
			b.finishRunningJobWithPhone(requestID, true, decision.Reason, decision.MIME, decision.Image, decision.Phone)
		} else {
			b.finishRunningJobWithPhone(requestID, false, decision.Reason, "", nil, decision.Phone)
		}
		delete(pending, requestID)
		if len(pending) > 0 {
			readDone = startPhoneRead(readConn)
		}
	}
}

func (b *Broker) currentRunningBatchID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runningBatchID
}

func (b *Broker) finishFromUpstreamRigasSatiksmeBatchHealthOnce(ctx context.Context, jobID string) bool {
	health, err := b.fetchUpstreamHealth(ctx)
	if err != nil {
		return false
	}
	result := health.RigasSatiksmeBatch
	if strings.TrimSpace(result.LastResultRequestID) != jobID {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(result.LastResultStatus))
	switch status {
	case "failed", "failure", "error", "canceled", "cancelled":
		reason := normalizeRigasSatiksmeQRFailureReason(result.LastResultReason)
		b.finishRunningJobWithPhone(jobID, false, reason, "", nil, RSQRPhoneSummary{
			Phases: sanitizeRSQRPhonePhases(result.Phases),
		})
		return true
	default:
		return false
	}
}

func cropRigasSatiksmeGeneratedScreenshotArtifact(input []byte, mime string) ([]byte, string) {
	if len(input) == 0 {
		return input, mime
	}
	cleanMIME := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	if cleanMIME != "" && cleanMIME != "image/png" {
		return input, mime
	}
	source, err := png.Decode(bytes.NewReader(input))
	if err != nil {
		return input, mime
	}
	bounds := source.Bounds()
	topCrop := rigasSatiksmeGeneratedScreenshotCropPixels(bounds.Dy())
	bottomCrop := topCrop
	if topCrop <= 0 || bottomCrop <= 0 {
		return input, mime
	}
	if bounds.Dx() <= 0 || bounds.Dy() <= topCrop+bottomCrop {
		return input, mime
	}
	croppedBounds := image.Rect(0, 0, bounds.Dx(), bounds.Dy()-topCrop-bottomCrop)
	cropped := image.NewRGBA(croppedBounds)
	draw.Draw(cropped, croppedBounds, source, image.Point{X: bounds.Min.X, Y: bounds.Min.Y + topCrop}, draw.Src)
	var output bytes.Buffer
	if err := png.Encode(&output, cropped); err != nil {
		return input, mime
	}
	return output.Bytes(), "image/png"
}

func rigasSatiksmeGeneratedScreenshotCropPixels(height int) int {
	if height <= 0 {
		return 0
	}
	return height * rigasSatiksmeGeneratedScreenshotCropPercent / 100
}

func normalizeRigasSatiksmeQRFailureReason(reason string) string {
	clean := strings.TrimSpace(reason)
	if clean == "" || clean == "generated" {
		return "qr_image_missing"
	}
	return clean
}

func (b *Broker) fetchUpstreamHealth(ctx context.Context) (upstreamHealth, error) {
	var health upstreamHealth
	healthCtx, cancel := context.WithTimeout(ctx, b.cfg.PhoneSendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, b.cfg.UpstreamBaseURL+"/api/v1/health", nil)
	if err != nil {
		return health, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return health, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return health, fmt.Errorf("upstream health status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return health, err
	}
	return health, nil
}

func (b *Broker) openQRPhoneBatchControl(ctx context.Context, jobs []QRJob) (*websocket.Conn, error) {
	conn, err := b.openPhoneControl(ctx, false)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	batchID := ""
	b.mu.Lock()
	if len(jobs) > 0 && b.runningJobID == jobs[0].ID {
		b.runningControl = conn
		batchID = b.runningBatchID
	}
	ticketPriorityActive := b.ticketBlocksQRLocked(now)
	b.mu.Unlock()
	if batchID == "" {
		batchID = "rsbatch-" + randomID()
	}
	jobPayloads := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		jobPayloads = append(jobPayloads, map[string]any{
			"requestId": job.ID,
			"digits":    job.Code,
			"createdAt": job.CreatedAt,
		})
	}
	if err := b.writePhoneJSON(ctx, conn, map[string]any{
		"type":                 "generate_rigassatiksme_qr_batch",
		"batchId":              batchID,
		"owner":                "rigassatiksme",
		"app":                  "rigas_satiksme",
		"flow":                 "monthly_ticket",
		"jobs":                 jobPayloads,
		"ticketPriorityActive": ticketPriorityActive,
		"serverSentAt":         time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "send qr batch command failed")
		return nil, err
	}
	return conn, nil
}

func (b *Broker) openPhoneControl(ctx context.Context, startSession bool) (*websocket.Conn, error) {
	if startSession {
		if err := b.postUpstream(ctx, "/api/v1/session/start"); err != nil {
			return nil, err
		}
	}
	target, err := b.websocketURL("/api/v1/session")
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.Dial(ctx, target, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(websocketProxyReadLimitBytes)
	return conn, nil
}

func (b *Broker) finishRunningJob(id string, ok bool, reason string, mime string, image []byte) {
	b.finishRunningJobWithPhone(id, ok, reason, mime, image, RSQRPhoneSummary{})
}

func (b *Broker) finishRunningJobWithPhone(id string, ok bool, reason string, mime string, image []byte, phone RSQRPhoneSummary) {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	job := b.jobs[id]
	if job == nil || job.Status != JobRunning {
		return
	}
	cleanReason := strings.TrimSpace(reason)
	job.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if !isZeroRSQRPhoneSummary(phone) {
		job.Phone = cloneRSQRPhoneSummary(phone)
	}
	if ok {
		job.Status = JobSucceeded
		job.Reason = cleanReason
		job.CompletedAt = job.UpdatedAt
		b.images[id] = QRImage{
			MIME:      mime,
			Bytes:     append([]byte(nil), image...),
			ExpiresAt: now.Add(b.cfg.ImageTTL),
		}
	} else if shouldRetryQRJob(cleanReason, job.Attempts) {
		job.Status = JobWaiting
		job.Reason = cleanReason
		job.CompletedAt = ""
	} else {
		job.Status = JobFailed
		job.Reason = cleanReason
		job.CompletedAt = job.UpdatedAt
	}
	if b.isRunningJobLocked(id) {
		b.removeRunningJobLocked(id)
	}
	_ = b.saveLocked()
	b.signalRunnerLocked()
}

func (b *Broker) removeRunningJobLocked(id string) {
	if len(b.runningJobIDs) > 0 {
		filtered := b.runningJobIDs[:0]
		for _, runningID := range b.runningJobIDs {
			if runningID != id {
				filtered = append(filtered, runningID)
			}
		}
		b.runningJobIDs = filtered
		if len(b.runningJobIDs) > 0 {
			b.runningJobID = b.runningJobIDs[0]
			return
		}
	}
	b.runningJobID = ""
	b.runningJobIDs = nil
	b.runningBatchID = ""
	b.runningCancel = nil
	b.runningControl = nil
}

func (b *Broker) isRunningJobLocked(id string) bool {
	if b.runningJobID == id {
		return true
	}
	for _, runningID := range b.runningJobIDs {
		if runningID == id {
			return true
		}
	}
	return false
}

func isZeroRSQRPhoneSummary(summary RSQRPhoneSummary) bool {
	return strings.TrimSpace(summary.SourceApp) == "" &&
		strings.TrimSpace(summary.TicketFlow) == "" &&
		summary.TotalDurationMillis == 0 &&
		len(summary.Phases) == 0
}

func cloneRSQRPhoneSummary(summary RSQRPhoneSummary) RSQRPhoneSummary {
	out := RSQRPhoneSummary{
		SourceApp:           strings.TrimSpace(summary.SourceApp),
		TicketFlow:          strings.TrimSpace(summary.TicketFlow),
		TotalDurationMillis: summary.TotalDurationMillis,
	}
	if len(summary.Phases) > 0 {
		out.Phases = make(map[string]int64, len(summary.Phases))
		for key, value := range summary.Phases {
			cleanKey := strings.TrimSpace(key)
			if cleanKey == "" {
				continue
			}
			if value < 0 {
				value = 0
			}
			out.Phases[cleanKey] = value
		}
		if len(out.Phases) == 0 {
			out.Phases = nil
		}
	}
	return out
}

func (b *Broker) preemptRunningLocked(now time.Time, reason string) {
	if b.runningJobID == "" && len(b.runningJobIDs) == 0 {
		return
	}
	b.lastPreemptionReason = strings.TrimSpace(reason)
	b.lastPreemptionAt = now.UTC()
	runningIDs := append([]string(nil), b.runningJobIDs...)
	if len(runningIDs) == 0 && b.runningJobID != "" {
		runningIDs = []string{b.runningJobID}
	}
	for _, id := range runningIDs {
		job := b.jobs[id]
		if job != nil && job.Status == JobRunning {
			job.Status = JobWaiting
			job.Reason = reason
			job.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		}
	}
	b.sendCancelToRunningLocked(reason)
	if b.runningCancel != nil {
		b.runningCancel()
	}
	b.runningJobID = ""
	b.runningJobIDs = nil
	b.runningBatchID = ""
	b.runningCancel = nil
	b.runningControl = nil
}

func (b *Broker) sendCancelToRunningLocked(reason string) {
	conn := b.runningControl
	if conn == nil {
		return
	}
	runningIDs := append([]string(nil), b.runningJobIDs...)
	if b.runningBatchID != "" {
		b.sendCancelBatchToConn(conn, b.runningBatchID, runningIDs, reason)
		return
	}
	requestID := b.runningJobID
	if len(runningIDs) == 1 {
		requestID = runningIDs[0]
	}
	b.sendCancelToConn(conn, requestID, reason)
}

func (b *Broker) sendCancelToConn(conn *websocket.Conn, requestID string, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = b.writePhoneJSON(ctx, conn, map[string]any{
		"type":      "cancel_rigassatiksme_qr",
		"requestId": requestID,
		"reason":    reason,
		"owner":     "rigassatiksme",
		"app":       "rigas_satiksme",
		"flow":      "monthly_ticket",
	})
}

func (b *Broker) sendCancelBatchToConn(conn *websocket.Conn, batchID string, requestIDs []string, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = b.writePhoneJSON(ctx, conn, map[string]any{
		"type":       "cancel_rigassatiksme_qr_batch",
		"batchId":    batchID,
		"requestIds": requestIDs,
		"reason":     reason,
		"owner":      "rigassatiksme",
		"app":        "rigas_satiksme",
		"flow":       "monthly_ticket",
	})
}

func (b *Broker) writePhoneJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.runningControlWriteMu.Lock()
	defer b.runningControlWriteMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, body)
}

func (b *Broker) proxyHTTP(w http.ResponseWriter, r *http.Request) {
	target := b.cfg.UpstreamBaseURL + r.URL.Path
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (b *Broker) proxyWebsocket(w http.ResponseWriter, r *http.Request, targetPath string) {
	b.beginTicketSocket()
	defer b.endTicketSocket()

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	clientConn.SetReadLimit(websocketProxyReadLimitBytes)
	defer clientConn.Close(websocket.StatusNormalClosure, "broker proxy closed")

	target, err := b.websocketURL(targetPath)
	if err != nil {
		_ = clientConn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	upstreamConn, _, err := websocket.Dial(r.Context(), target, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		_ = clientConn.Close(websocket.StatusInternalError, "upstream unavailable")
		return
	}
	upstreamConn.SetReadLimit(websocketProxyReadLimitBytes)
	defer upstreamConn.Close(websocket.StatusNormalClosure, "broker proxy closed")

	errCh := make(chan error, 2)
	go func() { errCh <- proxyMessages(r.Context(), upstreamConn, clientConn) }()
	go func() { errCh <- proxyMessages(r.Context(), clientConn, upstreamConn) }()
	select {
	case <-r.Context().Done():
	case <-errCh:
	}
}

func proxyMessages(ctx context.Context, dst *websocket.Conn, src *websocket.Conn) error {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

func (b *Broker) beginTicketSocket() {
	now := time.Now()
	b.mu.Lock()
	b.ticketSockets++
	b.ticketGraceUntil = time.Time{}
	if b.ticketBlocksQRLocked(now) {
		b.preemptRunningLocked(now, "ticket_active")
	}
	_ = b.saveLocked()
	b.mu.Unlock()
}

func (b *Broker) endTicketSocket() {
	now := time.Now()
	b.mu.Lock()
	if b.ticketSockets > 0 {
		b.ticketSockets--
	}
	if b.ticketSockets == 0 && b.ticketViewers == 0 {
		b.ticketGraceUntil = now.Add(b.cfg.TicketGrace)
	}
	b.mu.Unlock()
	b.signalRunner()
}

func (b *Broker) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	response := map[string]any{
		"ok":    true,
		"state": b.Snapshot(time.Now()),
	}
	if health, ok := b.upstreamHealthSnapshot(r.Context()); ok {
		if strings.TrimSpace(health.ControlCodeRequest.RequestID) != "" {
			response["controlCodeRequest"] = health.ControlCodeRequest
		}
		if strings.TrimSpace(health.RigasSatiksmeBatch.BatchID) != "" {
			response["rigasSatiksmeBatch"] = health.RigasSatiksmeBatch
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (b *Broker) upstreamHealthSnapshot(ctx context.Context) (upstreamHealth, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, healthUpstreamProbeTimeout)
	defer cancel()
	health, err := b.fetchUpstreamHealth(probeCtx)
	return health, err == nil
}

func (b *Broker) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": b.Snapshot(time.Now())})
}

func (b *Broker) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "analytics": b.Analytics(time.Now())})
}

func (b *Broker) handleTicketPresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Viewers int `json:"viewers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	if err := b.UpdateTicketPresence(r.Context(), TicketPresenceInput{Viewers: req.Viewers, Now: time.Now()}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": b.Snapshot(time.Now())})
}

func (b *Broker) handleTicketLease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		LeaseID   string `json:"leaseId"`
		RequestID string `json:"requestId"`
		Reason    string `json:"reason"`
		TTLMillis int64  `json:"ttlMillis"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	lease, err := b.AcquireTicketLease(r.Context(), TicketLeaseInput{
		LeaseID:   req.LeaseID,
		RequestID: req.RequestID,
		Reason:    req.Reason,
		TTL:       time.Duration(req.TTLMillis) * time.Millisecond,
		Now:       time.Now(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"lease": lease,
		"state": b.Snapshot(time.Now()),
	})
}

func (b *Broker) handleTicketLeaseRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		LeaseID   string `json:"leaseId"`
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	if err := b.ReleaseTicketLease(r.Context(), TicketLeaseInput{
		LeaseID:   req.LeaseID,
		RequestID: req.RequestID,
		Now:       time.Now(),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": b.Snapshot(time.Now())})
}

func (b *Broker) handleQRJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
		UserID string `json:"userId"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	job, err := b.EnqueueQRJob(r.Context(), QRJobInput{ChatID: req.ChatID, UserID: req.UserID, Code: req.Code, Now: time.Now()})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": publicJobFromQRJob(job)})
}

func (b *Broker) handleQRJob(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(r.URL.Path, "/api/v1/qr/jobs/")
	if clean == "latest" {
		b.handleLatestQRJob(w, r)
		return
	}
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "image" {
		b.handleQRJobImage(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		b.handleQRJobCancel(w, r, id)
		return
	}
	if len(parts) != 1 || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	job, ok := b.Job(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": publicJobFromQRJob(job)})
}

func (b *Broker) handleLatestQRJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	job, ok := b.LatestJob(r.URL.Query().Get("userId"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": publicJobFromQRJob(job)})
}

func (b *Broker) handleQRJobCancel(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b.mu.Lock()
	job := b.jobs[id]
	if job == nil {
		b.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	if job.Status == JobWaiting || job.Status == JobRunning {
		job.Status = JobCanceled
		job.Reason = "user_canceled"
		job.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		job.CompletedAt = job.UpdatedAt
		if b.isRunningJobLocked(job.ID) {
			b.sendCancelToRunningLocked("user_canceled")
			if b.runningCancel != nil {
				b.runningCancel()
			}
			b.runningJobID = ""
			b.runningJobIDs = nil
			b.runningBatchID = ""
			b.runningCancel = nil
			b.runningControl = nil
		}
	}
	out := cloneJob(*job)
	err := b.saveLocked()
	b.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": publicJobFromQRJob(out)})
}

func (b *Broker) handleQRJobImage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	image, ok := b.JobImage(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", image.MIME)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(image.Bytes)
}

func (b *Broker) postUpstream(ctx context.Context, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.cfg.UpstreamBaseURL+targetPath, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("upstream %s status %d", targetPath, resp.StatusCode)
}

func (b *Broker) websocketURL(targetPath string) (string, error) {
	parsed, err := url.Parse(b.cfg.UpstreamBaseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported upstream scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + targetPath
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func normalizeTicketLeaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTicketLeaseTTL
	}
	if ttl < minTicketLeaseTTL {
		return minTicketLeaseTTL
	}
	if ttl > maxTicketLeaseTTL {
		return maxTicketLeaseTTL
	}
	return ttl
}

func (b *Broker) pruneExpiredTicketLeasesLocked(now time.Time) {
	if b.ticketLeases == nil {
		return
	}
	for id, lease := range b.ticketLeases {
		if !lease.ExpiresAt.IsZero() && !now.Before(lease.ExpiresAt) {
			delete(b.ticketLeases, id)
		}
	}
}

func ticketLeaseSnapshot(lease ticketLease, now time.Time, blockedJobs int, preemptionReason string, preemptionAt time.Time) TicketLeaseSnapshot {
	remainingMillis := int64(lease.ExpiresAt.Sub(now) / time.Millisecond)
	if remainingMillis < 0 {
		remainingMillis = 0
	}
	snapshot := TicketLeaseSnapshot{
		ID:                   lease.ID,
		Owner:                "ticket",
		RequestID:            lease.RequestID,
		Reason:               lease.Reason,
		AcquiredAt:           lease.AcquiredAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:            lease.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:            lease.ExpiresAt.UTC().Format(time.RFC3339Nano),
		RemainingMillis:      remainingMillis,
		BlockedJobs:          blockedJobs,
		LastPreemptionReason: strings.TrimSpace(preemptionReason),
	}
	if !preemptionAt.IsZero() {
		snapshot.LastPreemptionAt = preemptionAt.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

func (b *Broker) activeTicketLeaseLocked(now time.Time) (ticketLease, bool) {
	b.pruneExpiredTicketLeasesLocked(now)
	var selected ticketLease
	found := false
	for _, lease := range b.ticketLeases {
		if lease.ID == "" {
			continue
		}
		if !lease.ExpiresAt.IsZero() && !now.Before(lease.ExpiresAt) {
			continue
		}
		if !found || lease.ExpiresAt.After(selected.ExpiresAt) || (lease.ExpiresAt.Equal(selected.ExpiresAt) && lease.UpdatedAt.After(selected.UpdatedAt)) {
			selected = lease
			found = true
		}
	}
	return selected, found
}

func (b *Broker) activeTicketLeaseSnapshotLocked(now time.Time, blockedJobs int) *TicketLeaseSnapshot {
	lease, ok := b.activeTicketLeaseLocked(now)
	if !ok {
		return nil
	}
	snapshot := ticketLeaseSnapshot(lease, now, blockedJobs, b.lastPreemptionReason, b.lastPreemptionAt)
	return &snapshot
}

func (b *Broker) ticketLeaseActiveLocked(now time.Time) bool {
	_, ok := b.activeTicketLeaseLocked(now)
	return ok
}

func (b *Broker) ticketActiveLocked(now time.Time) bool {
	return b.ticketLeaseActiveLocked(now) || b.ticketViewers > 0 || b.ticketSockets > 0 || (!b.ticketGraceUntil.IsZero() && now.Before(b.ticketGraceUntil))
}

func (b *Broker) ticketBlocksQRLocked(now time.Time) bool {
	if b.ticketLeaseActiveLocked(now) {
		return true
	}
	if b.ticketViewers > 0 {
		return true
	}
	return !b.ticketGraceUntil.IsZero() && now.Before(b.ticketGraceUntil)
}

func isTicketPriorityPreemptionReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "ticket_active", "ticket_lease_active":
		return true
	default:
		return false
	}
}

func (b *Broker) currentOwnerLocked(now time.Time) string {
	if b.ticketBlocksQRLocked(now) {
		return "ticket"
	}
	if b.runningJobID != "" || len(b.runningJobIDs) > 0 {
		return "rigassatiksme"
	}
	if b.ticketActiveLocked(now) {
		return "ticket"
	}
	return "none"
}

func (b *Broker) desiredPriorityLocked(now time.Time, queueDepth int) (string, []string) {
	qrDesired := queueDepth > 0 || b.runningJobID != "" || len(b.runningJobIDs) > 0
	if qrDesired {
		if b.ticketBlocksQRLocked(now) {
			return "ticket", []string{"ticket", "rigassatiksme"}
		}
		if b.ticketActiveLocked(now) {
			return "rigassatiksme", []string{"rigassatiksme", "ticket"}
		}
		return "rigassatiksme", []string{"rigassatiksme"}
	}
	if b.ticketActiveLocked(now) {
		return "ticket", []string{"ticket"}
	}
	return "none", nil
}

func (b *Broker) queueDepthLocked() int {
	depth := 0
	for _, job := range b.jobs {
		if job != nil && job.Status == JobWaiting {
			depth++
		}
	}
	return depth
}

func (b *Broker) signalRunner() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *Broker) signalRunnerLocked() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func shouldRetryQRJob(reason string, attempts int) bool {
	if attempts >= maxRecoverableJobAttempts {
		return false
	}
	switch strings.TrimSpace(reason) {
	case "phone_timeout",
		"qr_image_missing":
		return true
	default:
		return false
	}
}

func (b *Broker) load() error {
	if strings.TrimSpace(b.cfg.StatePath) == "" {
		return nil
	}
	body, err := os.ReadFile(b.cfg.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state persistedState
	if err := json.Unmarshal(body, &state); err != nil {
		return err
	}
	sort.SliceStable(state.Jobs, func(i, j int) bool {
		return state.Jobs[i].CreatedAt < state.Jobs[j].CreatedAt
	})
	for _, item := range state.Jobs {
		item := item
		if item.ID == "" {
			continue
		}
		if item.Status == JobRunning {
			item.Status = JobWaiting
			item.Reason = "broker_restarted"
		}
		b.jobs[item.ID] = &item
		b.order = append(b.order, item.ID)
	}
	return nil
}

func (b *Broker) saveLocked() error {
	if strings.TrimSpace(b.cfg.StatePath) == "" {
		return nil
	}
	state := persistedState{Jobs: make([]QRJob, 0, len(b.order))}
	for _, id := range b.order {
		if job := b.jobs[id]; job != nil {
			state.Jobs = append(state.Jobs, cloneJob(*job))
		}
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path.Dir(b.cfg.StatePath), 0o755); err != nil {
		return err
	}
	tmp := b.cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.cfg.StatePath)
}

func actorHash(userID, chatID string) string {
	source := strings.TrimSpace(userID)
	if source == "" {
		source = strings.TrimSpace(chatID)
	}
	if source == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(source))
	return "user:" + hex.EncodeToString(sum[:])[:12]
}

func qrJobTimingSeconds(job QRJob, now time.Time) (totalSec float64, queueSec float64, finalAttemptSec float64) {
	created, createdOK := parseJobTime(job.CreatedAt)
	started, startedOK := parseJobTime(job.StartedAt)
	completed, completedOK := parseJobTime(job.CompletedAt)
	end := completed
	if !completedOK {
		end = now
	}
	if createdOK {
		totalSec = roundedSeconds(end.Sub(created))
	}
	if createdOK && startedOK {
		queueSec = roundedSeconds(started.Sub(created))
	}
	if startedOK {
		finalAttemptSec = roundedSeconds(end.Sub(started))
	}
	return totalSec, queueSec, finalAttemptSec
}

func qrJobLastAt(job QRJob) string {
	for _, value := range []string{job.CompletedAt, job.UpdatedAt, job.StartedAt, job.CreatedAt} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func userImpactList(impactByActor map[string]*RSQRUserImpact) []RSQRUserImpact {
	if len(impactByActor) == 0 {
		return nil
	}
	items := make([]RSQRUserImpact, 0, len(impactByActor))
	for _, impact := range impactByActor {
		items = append(items, *impact)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LastAt != items[j].LastAt {
			return items[i].LastAt > items[j].LastAt
		}
		if items[i].Failed != items[j].Failed {
			return items[i].Failed > items[j].Failed
		}
		if items[i].Retried != items[j].Retried {
			return items[i].Retried > items[j].Retried
		}
		return items[i].ActorHash < items[j].ActorHash
	})
	if len(items) > 50 {
		items = items[:50]
	}
	return items
}

func parseJobTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func roundedSeconds(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return math.Round(duration.Seconds()*10) / 10
}

func latencyStats(values []float64) LatencyStats {
	if len(values) == 0 {
		return LatencyStats{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return LatencyStats{
		Count: len(sorted),
		Min:   sorted[0],
		P50:   percentile(sorted, 0.50),
		P90:   percentile(sorted, 0.90),
		Max:   sorted[len(sorted)-1],
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func cloneJob(job QRJob) QRJob {
	if job.Status == "" {
		job.Status = JobWaiting
	}
	job.Phone = cloneRSQRPhoneSummary(job.Phone)
	return job
}

func stateJobFromQRJob(job QRJob) StateJob {
	job = cloneJob(job)
	return StateJob{
		Status:      job.Status,
		Reason:      job.Reason,
		Attempts:    job.Attempts,
		CreatedAt:   job.CreatedAt,
		UpdatedAt:   job.UpdatedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
	}
}

func publicJobFromQRJob(job QRJob) publicQRJob {
	job = cloneJob(job)
	return publicQRJob{
		ID:          job.ID,
		Status:      job.Status,
		Reason:      job.Reason,
		Attempts:    job.Attempts,
		CreatedAt:   job.CreatedAt,
		UpdatedAt:   job.UpdatedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("phone broker write json failed: %v", err)
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func randomID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
