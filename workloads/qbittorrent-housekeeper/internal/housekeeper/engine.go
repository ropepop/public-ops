package housekeeper

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"time"

	"qbittorrenthousekeeper/internal/qbit"
	"qbittorrenthousekeeper/internal/storage"
)

const (
	DefaultAdmittedTag = "retention-admitted"
	DefaultWaitingTag  = "retention-waiting"
	DefaultRejectedTag = "retention-rejected"
	DefaultKeepTag     = "retention-keep"
)

type Policy struct {
	SoftCapBytes int64
	MinAge       time.Duration
	MinRatio     float64
	AdmittedTag  string
	WaitingTag   string
	RejectedTag  string
	KeepTag      string
	DownloadRoot string
}

func DefaultPolicy(softCapBytes int64, minAge time.Duration, minRatio float64) Policy {
	return Policy{
		SoftCapBytes: softCapBytes,
		MinAge:       minAge,
		MinRatio:     minRatio,
		AdmittedTag:  DefaultAdmittedTag,
		WaitingTag:   DefaultWaitingTag,
		RejectedTag:  DefaultRejectedTag,
		KeepTag:      DefaultKeepTag,
		DownloadRoot: "/downloads",
	}
}

func (p Policy) validate() error {
	if p.SoftCapBytes <= 0 {
		return errors.New("soft cap must be positive")
	}
	if p.MinAge < 0 || p.MinRatio < 0 || math.IsNaN(p.MinRatio) || math.IsInf(p.MinRatio, 0) {
		return errors.New("retention thresholds must not be negative")
	}
	cleanRoot := path.Clean(p.DownloadRoot)
	if !path.IsAbs(cleanRoot) || cleanRoot == "/" {
		return errors.New("download root must be an absolute dedicated directory, not the filesystem root")
	}
	tags := []string{p.AdmittedTag, p.WaitingTag, p.RejectedTag, p.KeepTag}
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" || strings.Contains(tag, ",") {
			return errors.New("policy tags must be non-empty and must not contain commas")
		}
		if _, exists := seen[tag]; exists {
			return errors.New("policy tags must be unique")
		}
		seen[tag] = struct{}{}
	}
	return nil
}

type Clock func() time.Time

type Engine struct {
	api    qbit.API
	usage  storage.Usage
	policy Policy
	now    Clock
	status *StatusStore
}

func New(api qbit.API, usage storage.Usage, policy Policy, now Clock, status *StatusStore) (*Engine, error) {
	if api == nil {
		return nil, errors.New("qBittorrent API is required")
	}
	if usage == nil {
		return nil, errors.New("storage usage source is required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	if status == nil {
		status = NewStatusStore()
	}
	return &Engine{api: api, usage: usage, policy: policy, now: now, status: status}, nil
}

func (e *Engine) Status() *StatusStore {
	return e.status
}

func (e *Engine) Reconcile(ctx context.Context) error {
	started := e.now().UTC()
	torrents, err := e.api.List(ctx)
	if err != nil {
		err = fmt.Errorf("list torrents: %w", err)
		e.status.RecordFailure(started, err)
		return err
	}
	usedBytes, err := e.usage.UsedBytes()
	if err != nil {
		err = fmt.Errorf("measure storage: %w", err)
		e.status.RecordFailure(started, err)
		return err
	}
	if usedBytes < 0 {
		err = errors.New("measure storage: negative usage")
		e.status.RecordFailure(started, err)
		return err
	}

	items := make([]*managedTorrent, 0, len(torrents))
	for i := range torrents {
		items = append(items, newManagedTorrent(torrents[i]))
	}

	eligible := e.eligibleForDeletion(items, started)
	if len(eligible) > 0 {
		for _, item := range eligible {
			if err := e.api.Delete(ctx, item.torrent.Hash, true); err != nil {
				err = fmt.Errorf("delete eligible torrent %s: %w", shortHash(item.torrent.Hash), err)
				e.status.RecordFailure(started, err)
				return err
			}
		}
		snapshot := e.snapshot(started, items, usedBytes)
		snapshot.DeletionsRequested = len(eligible)
		snapshot.Message = "eligible deletions submitted; admission deferred for a fresh storage measurement"
		e.status.RecordSuccess(snapshot)
		return nil
	}

	if err := e.normalize(ctx, items); err != nil {
		e.status.RecordFailure(started, err)
		return err
	}

	if err := e.finishInterruptedAdmissions(ctx, items, usedBytes); err != nil {
		e.status.RecordFailure(started, err)
		return err
	}

	candidates := waitingCandidates(items, e.policy)
	sort.Slice(candidates, func(i, j int) bool {
		return older(candidates[i].torrent, candidates[j].torrent)
	})

	admittedThisPoll := 0
	for _, candidate := range candidates {
		if !isStopped(candidate.torrent.State) {
			continue
		}
		if e.fits(items, candidate, usedBytes) {
			if err := e.admit(ctx, candidate); err != nil {
				e.status.RecordFailure(started, err)
				return err
			}
			admittedThisPoll++
		}
	}

	snapshot := e.snapshot(started, items, usedBytes)
	snapshot.AdmittedThisPoll = admittedThisPoll
	if snapshot.Waiting > 0 {
		snapshot.Message = "one or more torrents are waiting for capacity"
	} else {
		snapshot.Message = "policy reconciled"
	}
	e.status.RecordSuccess(snapshot)
	return nil
}

func (e *Engine) eligibleForDeletion(items []*managedTorrent, now time.Time) []*managedTorrent {
	eligible := make([]*managedTorrent, 0)
	for _, item := range items {
		t := item.torrent
		if item.has(e.policy.KeepTag) ||
			!pathsInsideDownloadRoot(t, e.policy.DownloadRoot) ||
			!currentlyComplete(t) ||
			t.CompletionOn <= 0 ||
			t.Ratio < e.policy.MinRatio {
			continue
		}
		completedAt := time.Unix(t.CompletionOn, 0)
		if completedAt.After(now) || now.Sub(completedAt) < e.policy.MinAge {
			continue
		}
		eligible = append(eligible, item)
	}
	sort.Slice(eligible, func(i, j int) bool {
		return older(eligible[i].torrent, eligible[j].torrent)
	})
	return eligible
}

func (e *Engine) normalize(ctx context.Context, items []*managedTorrent) error {
	for _, item := range items {
		t := item.torrent
		if !pathsInsideDownloadRoot(t, e.policy.DownloadRoot) {
			if !isStopped(t.State) {
				if err := e.api.Stop(ctx, t.Hash); err != nil {
					return fmt.Errorf("stop off-volume torrent %s: %w", shortHash(t.Hash), err)
				}
				item.torrent.State = "stoppedDL"
			}
			if !item.has(e.policy.RejectedTag) {
				if err := e.api.AddTags(ctx, t.Hash, e.policy.RejectedTag); err != nil {
					return fmt.Errorf("reject off-volume torrent %s: %w", shortHash(t.Hash), err)
				}
				item.add(e.policy.RejectedTag)
			}
			if err := e.removeTags(ctx, item, e.policy.WaitingTag, e.policy.AdmittedTag); err != nil {
				return err
			}
			continue
		}
		if !metadataReady(t) {
			continue
		}

		if t.Size > e.policy.SoftCapBytes {
			if !isStopped(t.State) {
				if err := e.api.Stop(ctx, t.Hash); err != nil {
					return fmt.Errorf("stop oversized torrent %s: %w", shortHash(t.Hash), err)
				}
				item.torrent.State = "stoppedDL"
			}
			if !item.has(e.policy.RejectedTag) {
				if err := e.api.AddTags(ctx, t.Hash, e.policy.RejectedTag); err != nil {
					return fmt.Errorf("tag oversized torrent %s: %w", shortHash(t.Hash), err)
				}
				item.add(e.policy.RejectedTag)
			}
			if err := e.removeTags(ctx, item, e.policy.WaitingTag, e.policy.AdmittedTag); err != nil {
				return err
			}
			continue
		}

		if item.has(e.policy.RejectedTag) {
			if !isStopped(t.State) {
				if err := e.api.Stop(ctx, t.Hash); err != nil {
					return fmt.Errorf("stop previously rejected torrent %s: %w", shortHash(t.Hash), err)
				}
				item.torrent.State = "stoppedDL"
			}
			if err := e.api.AddTags(ctx, t.Hash, e.policy.WaitingTag); err != nil {
				return fmt.Errorf("mark resized torrent waiting %s: %w", shortHash(t.Hash), err)
			}
			item.add(e.policy.WaitingTag)
			if err := e.removeTags(ctx, item, e.policy.RejectedTag); err != nil {
				return err
			}
		}

		if item.has(e.policy.AdmittedTag) {
			continue
		}

		if item.has(e.policy.WaitingTag) {
			if !isStopped(t.State) {
				if err := e.api.Stop(ctx, t.Hash); err != nil {
					return fmt.Errorf("re-stop waiting torrent %s: %w", shortHash(t.Hash), err)
				}
				item.torrent.State = "stoppedDL"
				item.blockAdmission = true
			}
			continue
		}

		if !isStopped(t.State) {
			if err := e.api.Stop(ctx, t.Hash); err != nil {
				return fmt.Errorf("stop unmanaged torrent %s: %w", shortHash(t.Hash), err)
			}
			item.torrent.State = "stoppedDL"
			item.blockAdmission = true
		}
		if err := e.api.AddTags(ctx, t.Hash, e.policy.WaitingTag); err != nil {
			return fmt.Errorf("mark torrent waiting %s: %w", shortHash(t.Hash), err)
		}
		item.add(e.policy.WaitingTag)
	}
	return nil
}

func (e *Engine) finishInterruptedAdmissions(ctx context.Context, items []*managedTorrent, usedBytes int64) error {
	for _, item := range items {
		if !item.has(e.policy.AdmittedTag) || !item.has(e.policy.WaitingTag) || item.has(e.policy.RejectedTag) {
			continue
		}
		if !e.fits(items, item, usedBytes) {
			if !isStopped(item.torrent.State) {
				if err := e.api.Stop(ctx, item.torrent.Hash); err != nil {
					return fmt.Errorf("stop over-capacity interrupted admission %s: %w", shortHash(item.torrent.Hash), err)
				}
				item.torrent.State = "stoppedDL"
			}
			continue
		}
		if isStopped(item.torrent.State) {
			if err := e.api.Start(ctx, item.torrent.Hash); err != nil {
				return fmt.Errorf("retry admission start %s: %w", shortHash(item.torrent.Hash), err)
			}
			item.torrent.State = "downloading"
		}
		if err := e.removeTags(ctx, item, e.policy.WaitingTag); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) admit(ctx context.Context, item *managedTorrent) error {
	if err := e.api.AddTags(ctx, item.torrent.Hash, e.policy.AdmittedTag); err != nil {
		return fmt.Errorf("reserve torrent %s: %w", shortHash(item.torrent.Hash), err)
	}
	item.add(e.policy.AdmittedTag)
	if err := e.api.Start(ctx, item.torrent.Hash); err != nil {
		return fmt.Errorf("start admitted torrent %s: %w", shortHash(item.torrent.Hash), err)
	}
	item.torrent.State = "downloading"
	if err := e.removeTags(ctx, item, e.policy.WaitingTag, e.policy.RejectedTag); err != nil {
		return err
	}
	return nil
}

func (e *Engine) removeTags(ctx context.Context, item *managedTorrent, tags ...string) error {
	present := make([]string, 0, len(tags))
	for _, tag := range tags {
		if item.has(tag) {
			present = append(present, tag)
		}
	}
	if len(present) == 0 {
		return nil
	}
	if err := e.api.RemoveTags(ctx, item.torrent.Hash, present...); err != nil {
		return fmt.Errorf("remove policy tags from torrent %s: %w", shortHash(item.torrent.Hash), err)
	}
	for _, tag := range present {
		item.remove(tag)
	}
	return nil
}

func (e *Engine) fits(items []*managedTorrent, candidate *managedTorrent, usedBytes int64) bool {
	reserved, materialized := int64(0), int64(0)
	for _, item := range items {
		if item == candidate || !item.has(e.policy.AdmittedTag) || item.has(e.policy.RejectedTag) {
			continue
		}
		reserved = saturatingAdd(reserved, item.torrent.Size)
		materialized = saturatingAdd(materialized, materializedBytes(item.torrent))
	}
	reserved = saturatingAdd(reserved, candidate.torrent.Size)
	materialized = saturatingAdd(materialized, materializedBytes(candidate.torrent))
	if reserved > e.policy.SoftCapBytes || usedBytes > e.policy.SoftCapBytes {
		return false
	}
	untracked := usedBytes - materialized
	if untracked < 0 {
		untracked = 0
	}
	committed := saturatingAdd(untracked, reserved)
	return committed <= e.policy.SoftCapBytes
}

func (e *Engine) snapshot(now time.Time, items []*managedTorrent, usedBytes int64) Snapshot {
	snapshot := Snapshot{
		LastAttempt:  now,
		LastSuccess:  now,
		Healthy:      true,
		TorrentCount: len(items),
		UsedBytes:    usedBytes,
		SoftCapBytes: e.policy.SoftCapBytes,
	}
	materialized := int64(0)
	for _, item := range items {
		switch {
		case item.has(e.policy.RejectedTag):
			snapshot.Rejected++
		case item.has(e.policy.AdmittedTag):
			snapshot.Admitted++
			snapshot.ReservedBytes = saturatingAdd(snapshot.ReservedBytes, item.torrent.Size)
			materialized = saturatingAdd(materialized, materializedBytes(item.torrent))
		case item.has(e.policy.WaitingTag):
			snapshot.Waiting++
		default:
			snapshot.Unmanaged++
		}
		if item.has(e.policy.KeepTag) {
			snapshot.Kept++
		}
	}
	untracked := usedBytes - materialized
	if untracked < 0 {
		untracked = 0
	}
	snapshot.CommittedBytes = saturatingAdd(untracked, snapshot.ReservedBytes)
	return snapshot
}

type managedTorrent struct {
	torrent        qbit.Torrent
	tags           map[string]struct{}
	blockAdmission bool
}

func newManagedTorrent(torrent qbit.Torrent) *managedTorrent {
	tags := make(map[string]struct{})
	for _, tag := range strings.Split(torrent.Tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags[tag] = struct{}{}
		}
	}
	return &managedTorrent{torrent: torrent, tags: tags}
}

func (m *managedTorrent) has(tag string) bool {
	_, exists := m.tags[tag]
	return exists
}

func (m *managedTorrent) add(tag string) {
	m.tags[tag] = struct{}{}
}

func (m *managedTorrent) remove(tag string) {
	delete(m.tags, tag)
}

func waitingCandidates(items []*managedTorrent, policy Policy) []*managedTorrent {
	candidates := make([]*managedTorrent, 0)
	for _, item := range items {
		if item.blockAdmission || !metadataReady(item.torrent) || item.torrent.Size > policy.SoftCapBytes {
			continue
		}
		if item.has(policy.WaitingTag) && !item.has(policy.AdmittedTag) && !item.has(policy.RejectedTag) {
			candidates = append(candidates, item)
		}
	}
	return candidates
}

func metadataReady(t qbit.Torrent) bool {
	return t.Size > 0 && t.State != "metaDL" && t.State != "checkingResumeData"
}

func currentlyComplete(t qbit.Torrent) bool {
	if t.AmountLeft != 0 || t.Progress < 1 {
		return false
	}
	state := strings.ToLower(t.State)
	return state != "" &&
		!strings.Contains(state, "check") &&
		!strings.Contains(state, "error") &&
		!strings.Contains(state, "missing") &&
		!strings.Contains(state, "moving") &&
		state != "metadl"
}

func pathsInsideDownloadRoot(t qbit.Torrent, root string) bool {
	if !pathInsideRoot(t.SavePath, root) {
		return false
	}
	return t.DownloadPath == "" || pathInsideRoot(t.DownloadPath, root)
}

func pathInsideRoot(candidate, root string) bool {
	cleanCandidate := path.Clean(candidate)
	cleanRoot := path.Clean(root)
	if candidate == "" || !path.IsAbs(cleanCandidate) {
		return false
	}
	return cleanCandidate == cleanRoot || strings.HasPrefix(cleanCandidate, cleanRoot+"/")
}

func isStopped(state string) bool {
	return strings.HasPrefix(state, "stopped") || strings.HasPrefix(state, "paused")
}

func older(a, b qbit.Torrent) bool {
	aTime := sortTime(a)
	bTime := sortTime(b)
	if aTime != bTime {
		return aTime < bTime
	}
	return a.Hash < b.Hash
}

func sortTime(t qbit.Torrent) int64 {
	if t.CompletionOn > 0 {
		return t.CompletionOn
	}
	if t.AddedOn > 0 {
		return t.AddedOn
	}
	return int64(^uint64(0) >> 1)
}

func materializedBytes(t qbit.Torrent) int64 {
	if t.Completed <= 0 || t.Size <= 0 {
		return 0
	}
	if t.Completed > t.Size {
		return t.Size
	}
	return t.Completed
}

func saturatingAdd(a, b int64) int64 {
	if b <= 0 {
		return a
	}
	max := int64(^uint64(0) >> 1)
	if a > max-b {
		return max
	}
	return a + b
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
