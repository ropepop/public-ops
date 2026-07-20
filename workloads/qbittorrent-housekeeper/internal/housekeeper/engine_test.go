package housekeeper

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"qbittorrenthousekeeper/internal/qbit"
)

var fixedNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func TestRetentionBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		completionOn int64
		ratio        float64
		wantDelete   bool
	}{
		{name: "23 hours 59 minutes is retained", completionOn: fixedNow.Add(-23*time.Hour - 59*time.Minute).Unix(), ratio: 2},
		{name: "24 hours below ratio is retained", completionOn: fixedNow.Add(-24 * time.Hour).Unix(), ratio: 0.999},
		{name: "exactly 24 hours and ratio one is deleted", completionOn: fixedNow.Add(-24 * time.Hour).Unix(), ratio: 1, wantDelete: true},
		{name: "missing completion timestamp fails closed", completionOn: 0, ratio: 9},
		{name: "future completion timestamp fails closed", completionOn: fixedNow.Add(time.Minute).Unix(), ratio: 9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := newFakeAPI(qbit.Torrent{
				Hash:         "completed-hash",
				Size:         40,
				Completed:    40,
				Progress:     1,
				State:        "uploading",
				Tags:         DefaultAdmittedTag,
				CompletionOn: test.completionOn,
				Ratio:        test.ratio,
			})
			engine := newTestEngine(t, api, &fakeUsage{used: 40}, 100)
			if err := engine.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			gotDelete := api.count("delete") > 0
			if gotDelete != test.wantDelete {
				t.Fatalf("delete = %v, want %v; calls: %#v", gotDelete, test.wantDelete, api.calls)
			}
			if gotDelete && !api.calls[0].deleteFiles {
				t.Fatal("eligible deletion did not request payload deletion")
			}
		})
	}
}

func TestEligibleDeletionOldestFirstAndAsyncSpaceRecheck(t *testing.T) {
	oldest := qbit.Torrent{
		Hash: "oldest", Size: 40, Completed: 40, Progress: 1, State: "uploading", Tags: DefaultAdmittedTag,
		AddedOn: fixedNow.Add(-72 * time.Hour).Unix(), CompletionOn: fixedNow.Add(-48 * time.Hour).Unix(), Ratio: 1.5,
	}
	newer := qbit.Torrent{
		Hash: "newer", Size: 20, Completed: 20, Progress: 1, State: "uploading", Tags: DefaultAdmittedTag,
		AddedOn: fixedNow.Add(-60 * time.Hour).Unix(), CompletionOn: fixedNow.Add(-30 * time.Hour).Unix(), Ratio: 2,
	}
	waiting := qbit.Torrent{
		Hash: "waiting", Size: 30, State: "stoppedDL", Tags: DefaultWaitingTag,
		AddedOn: fixedNow.Add(-time.Hour).Unix(),
	}
	api := newFakeAPI(newer, waiting, oldest)
	usage := &fakeUsage{used: 90}
	engine := newTestEngine(t, api, usage, 100)

	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	deleteHashes := api.hashesFor("delete")
	if !slices.Equal(deleteHashes, []string{"oldest", "newer"}) {
		t.Fatalf("delete order = %v, want [oldest newer]", deleteHashes)
	}
	if api.count("start") != 0 {
		t.Fatal("admission occurred in the same poll as an asynchronous deletion")
	}

	api.clearCalls()
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if api.count("start") != 0 {
		t.Fatal("torrent admitted before filesystem usage showed reclaimed space")
	}
	if !api.hasTag("waiting", DefaultWaitingTag) {
		t.Fatal("capacity-blocked torrent lost its waiting tag")
	}

	usage.used = 10
	api.clearCalls()
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("third Reconcile() error = %v", err)
	}
	if !slices.Equal(api.hashesFor("start"), []string{"waiting"}) {
		t.Fatalf("start calls = %v, want [waiting]", api.hashesFor("start"))
	}
	if !api.hasTag("waiting", DefaultAdmittedTag) || api.hasTag("waiting", DefaultWaitingTag) {
		t.Fatalf("admitted torrent tags = %q", api.torrent("waiting").Tags)
	}
}

func TestOversizedTorrentIsRejectedAndRestopped(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{Hash: "huge", Size: 101, State: "downloading", AddedOn: fixedNow.Unix()})
	engine := newTestEngine(t, api, &fakeUsage{}, 100)

	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !slices.Equal(api.hashesFor("stop"), []string{"huge"}) {
		t.Fatalf("stop calls = %v", api.hashesFor("stop"))
	}
	if !api.hasTag("huge", DefaultRejectedTag) || api.count("start") != 0 {
		t.Fatalf("oversized torrent was not left rejected: %#v", api.torrent("huge"))
	}

	api.torrentPtr("huge").State = "downloading"
	api.clearCalls()
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if !slices.Equal(api.hashesFor("stop"), []string{"huge"}) || api.count("start") != 0 {
		t.Fatalf("rejected torrent was not re-stopped: %#v", api.calls)
	}
}

func TestAdmissionUsesFullReservationsAndOldestWaitingFirst(t *testing.T) {
	api := newFakeAPI(
		qbit.Torrent{Hash: "active", Size: 60, Completed: 5, State: "downloading", Tags: DefaultAdmittedTag},
		qbit.Torrent{Hash: "newer", Size: 20, State: "stoppedDL", Tags: DefaultWaitingTag, AddedOn: 20},
		qbit.Torrent{Hash: "older", Size: 30, State: "stoppedDL", Tags: DefaultWaitingTag, AddedOn: 10},
	)
	engine := newTestEngine(t, api, &fakeUsage{used: 5}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !slices.Equal(api.hashesFor("start"), []string{"older"}) {
		t.Fatalf("start calls = %v, want only oldest waiting torrent", api.hashesFor("start"))
	}
	if !api.hasTag("newer", DefaultWaitingTag) {
		t.Fatal("newer torrent should remain waiting because full reservations total 110")
	}
	status := engine.Status().Snapshot()
	if status.ReservedBytes != 90 {
		t.Fatalf("reserved bytes = %d, want 90", status.ReservedBytes)
	}
}

func TestActualUntrackedUsageIsIncludedWithFullReservations(t *testing.T) {
	api := newFakeAPI(
		qbit.Torrent{Hash: "active", Size: 70, Completed: 10, State: "downloading", Tags: DefaultAdmittedTag},
		qbit.Torrent{Hash: "waiting", Size: 20, State: "stoppedDL", Tags: DefaultWaitingTag},
	)
	// Ten bytes belong to the active torrent and twenty-five are untracked.
	// Full commitment would be 25 + 70 + 20 = 115.
	engine := newTestEngine(t, api, &fakeUsage{used: 35}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("start") != 0 {
		t.Fatal("untracked filesystem use was ignored during admission")
	}
}

func TestManualPauseOfAdmittedTorrentIsRespected(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{Hash: "paused", Size: 40, State: "stoppedDL", Tags: DefaultAdmittedTag})
	engine := newTestEngine(t, api, &fakeUsage{used: 10}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("start") != 0 || api.count("stop") != 0 {
		t.Fatalf("manual pause was not respected: %#v", api.calls)
	}
}

func TestManuallyStartedWaitingTorrentIsRestoppedBeforeAdmission(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{Hash: "waiting", Size: 40, State: "downloading", Tags: DefaultWaitingTag})
	engine := newTestEngine(t, api, &fakeUsage{}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("stop") != 1 || api.count("start") != 0 {
		t.Fatalf("waiting torrent was not safely re-stopped: %#v", api.calls)
	}
	api.clearCalls()
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if api.count("start") != 1 {
		t.Fatalf("stopped waiting torrent was not admitted on a later poll: %#v", api.calls)
	}
}

func TestKeepTagPreventsDeletionAndStillConsumesQuota(t *testing.T) {
	api := newFakeAPI(
		qbit.Torrent{
			Hash: "kept", Size: 80, Completed: 80, Progress: 1, State: "stoppedUP", Tags: DefaultAdmittedTag + ", " + DefaultKeepTag,
			CompletionOn: fixedNow.Add(-72 * time.Hour).Unix(), Ratio: 8,
		},
		qbit.Torrent{Hash: "waiting", Size: 30, State: "stoppedDL", Tags: DefaultWaitingTag},
	)
	engine := newTestEngine(t, api, &fakeUsage{used: 80}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("delete") != 0 || api.count("start") != 0 {
		t.Fatalf("keep policy was violated: %#v", api.calls)
	}
	status := engine.Status().Snapshot()
	if status.Kept != 1 || status.ReservedBytes != 80 {
		t.Fatalf("keep status = %#v", status)
	}
}

func TestMetadataDownloadIsNotStoppedPrematurely(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{Hash: "magnet", Size: 0, State: "metaDL"})
	engine := newTestEngine(t, api, &fakeUsage{}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("metadata fetch was mutated: %#v", api.calls)
	}
}

func TestStorageFailureFailsClosed(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{Hash: "new", Size: 20, State: "downloading"})
	engine := newTestEngine(t, api, &fakeUsage{err: errors.New("statfs failed")}, 100)
	if err := engine.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() unexpectedly succeeded")
	}
	if len(api.calls) != 0 {
		t.Fatalf("API was mutated without reliable storage data: %#v", api.calls)
	}
	if engine.Status().Snapshot().Healthy {
		t.Fatal("failed reconciliation reported healthy")
	}
}

func TestInterruptedAdmissionRetriesWithoutOverbooking(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{
		Hash: "transition", Size: 80, State: "stoppedDL", Tags: DefaultAdmittedTag + "," + DefaultWaitingTag,
	})
	engine := newTestEngine(t, api, &fakeUsage{}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("start") != 1 || api.hasTag("transition", DefaultWaitingTag) {
		t.Fatalf("interrupted admission was not completed: %#v", api.calls)
	}
}

func TestHistoricalCompletionDoesNotDeleteIncompleteOrUncertainTorrent(t *testing.T) {
	tests := []qbit.Torrent{
		{Hash: "missing-bytes", Size: 40, Completed: 30, Progress: 0.75, AmountLeft: 10, State: "downloading"},
		{Hash: "checking", Size: 40, Completed: 40, Progress: 1, State: "checkingUP"},
		{Hash: "missing-files", Size: 40, Completed: 40, Progress: 1, State: "missingFiles"},
		{Hash: "missing-state", Size: 40, Completed: 40, Progress: 1, State: ""},
	}
	for i := range tests {
		tests[i].Tags = DefaultAdmittedTag
		tests[i].CompletionOn = fixedNow.Add(-72 * time.Hour).Unix()
		tests[i].Ratio = 5
	}
	api := newFakeAPI(tests...)
	engine := newTestEngine(t, api, &fakeUsage{used: 100}, 200)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("delete") != 0 {
		t.Fatalf("uncertain or incomplete torrent was deleted: %#v", api.calls)
	}
}

func TestInterruptedAdmissionRechecksCapacityBeforeStarting(t *testing.T) {
	api := newFakeAPI(qbit.Torrent{
		Hash: "transition", Size: 80, State: "stoppedDL", Tags: DefaultAdmittedTag + "," + DefaultWaitingTag,
	})
	engine := newTestEngine(t, api, &fakeUsage{used: 50}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("start") != 0 || !api.hasTag("transition", DefaultWaitingTag) {
		t.Fatalf("over-capacity interrupted admission was resumed: %#v", api.calls)
	}
}

func TestPolicyRejectsNonFiniteRatio(t *testing.T) {
	policy := DefaultPolicy(100, time.Hour, 1)
	policy.MinRatio = math.NaN()
	if _, err := New(newFakeAPI(), &fakeUsage{}, policy, nil, nil); err == nil {
		t.Fatal("New() accepted NaN minimum ratio")
	}
}

func TestSaveAndTemporaryPathsMustStayInsideDownloadRoot(t *testing.T) {
	tests := []struct {
		name         string
		savePath     string
		downloadPath string
		wantAdmitted bool
	}{
		{name: "root", savePath: "/downloads", wantAdmitted: true},
		{name: "descendants", savePath: "/downloads/series", downloadPath: "/downloads/.incomplete", wantAdmitted: true},
		{name: "lookalike prefix", savePath: "/downloads2"},
		{name: "parent traversal", savePath: "/downloads/../config"},
		{name: "outside temporary path", savePath: "/downloads", downloadPath: "/tmp"},
		{name: "missing save path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := newRawFakeAPI(qbit.Torrent{
				Hash: "path-test", Size: 20, State: "stoppedDL", SavePath: test.savePath, DownloadPath: test.downloadPath,
			})
			engine := newTestEngine(t, api, &fakeUsage{}, 100)
			if err := engine.Reconcile(context.Background()); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if test.wantAdmitted {
				if !api.hasTag("path-test", DefaultAdmittedTag) || api.count("start") != 1 {
					t.Fatalf("safe path not admitted: torrent=%#v calls=%#v", api.torrent("path-test"), api.calls)
				}
			} else if !api.hasTag("path-test", DefaultRejectedTag) || api.count("start") != 0 {
				t.Fatalf("unsafe path not rejected: torrent=%#v calls=%#v", api.torrent("path-test"), api.calls)
			}
		})
	}
}

func TestAdmittedTorrentThatDriftsOffVolumeIsStoppedAndRejected(t *testing.T) {
	api := newRawFakeAPI(qbit.Torrent{
		Hash:         "drift",
		Size:         20,
		Completed:    20,
		Progress:     1,
		State:        "uploading",
		Tags:         DefaultAdmittedTag,
		SavePath:     "/config",
		CompletionOn: fixedNow.Add(-72 * time.Hour).Unix(),
		Ratio:        5,
	})
	engine := newTestEngine(t, api, &fakeUsage{}, 100)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if api.count("delete") != 0 || api.count("stop") != 1 || !api.hasTag("drift", DefaultRejectedTag) || api.hasTag("drift", DefaultAdmittedTag) {
		t.Fatalf("off-volume drift was not contained: torrent=%#v calls=%#v", api.torrent("drift"), api.calls)
	}
}

func newTestEngine(t *testing.T, api *fakeAPI, usage *fakeUsage, capBytes int64) *Engine {
	t.Helper()
	engine, err := New(api, usage, DefaultPolicy(capBytes, 24*time.Hour, 1), func() time.Time { return fixedNow }, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return engine
}

type fakeUsage struct {
	used int64
	err  error
}

func (f *fakeUsage) UsedBytes() (int64, error) {
	return f.used, f.err
}

type apiCall struct {
	operation   string
	hash        string
	tags        []string
	deleteFiles bool
}

type fakeAPI struct {
	torrents []qbit.Torrent
	calls    []apiCall
	listErr  error
	callErr  map[string]error
}

func newFakeAPI(torrents ...qbit.Torrent) *fakeAPI {
	for i := range torrents {
		if torrents[i].SavePath == "" {
			torrents[i].SavePath = "/downloads"
		}
	}
	return newRawFakeAPI(torrents...)
}

func newRawFakeAPI(torrents ...qbit.Torrent) *fakeAPI {
	return &fakeAPI{torrents: torrents, callErr: make(map[string]error)}
}

func (f *fakeAPI) List(context.Context) ([]qbit.Torrent, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return slices.Clone(f.torrents), nil
}

func (f *fakeAPI) Stop(_ context.Context, hash string) error {
	if err := f.callErr["stop"]; err != nil {
		return err
	}
	f.calls = append(f.calls, apiCall{operation: "stop", hash: hash})
	f.torrentPtr(hash).State = "stoppedDL"
	return nil
}

func (f *fakeAPI) Start(_ context.Context, hash string) error {
	if err := f.callErr["start"]; err != nil {
		return err
	}
	f.calls = append(f.calls, apiCall{operation: "start", hash: hash})
	f.torrentPtr(hash).State = "downloading"
	return nil
}

func (f *fakeAPI) AddTags(_ context.Context, hash string, tags ...string) error {
	if err := f.callErr["addTags"]; err != nil {
		return err
	}
	f.calls = append(f.calls, apiCall{operation: "addTags", hash: hash, tags: slices.Clone(tags)})
	torrent := f.torrentPtr(hash)
	tagSet := parseTagSet(torrent.Tags)
	for _, tag := range tags {
		tagSet[tag] = struct{}{}
	}
	torrent.Tags = joinTagSet(tagSet)
	return nil
}

func (f *fakeAPI) RemoveTags(_ context.Context, hash string, tags ...string) error {
	if err := f.callErr["removeTags"]; err != nil {
		return err
	}
	f.calls = append(f.calls, apiCall{operation: "removeTags", hash: hash, tags: slices.Clone(tags)})
	torrent := f.torrentPtr(hash)
	tagSet := parseTagSet(torrent.Tags)
	for _, tag := range tags {
		delete(tagSet, tag)
	}
	torrent.Tags = joinTagSet(tagSet)
	return nil
}

func (f *fakeAPI) Delete(_ context.Context, hash string, deleteFiles bool) error {
	if err := f.callErr["delete"]; err != nil {
		return err
	}
	f.calls = append(f.calls, apiCall{operation: "delete", hash: hash, deleteFiles: deleteFiles})
	for i := range f.torrents {
		if f.torrents[i].Hash == hash {
			f.torrents = append(f.torrents[:i], f.torrents[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeAPI) count(operation string) int {
	return len(f.hashesFor(operation))
}

func (f *fakeAPI) hashesFor(operation string) []string {
	var hashes []string
	for _, call := range f.calls {
		if call.operation == operation {
			hashes = append(hashes, call.hash)
		}
	}
	return hashes
}

func (f *fakeAPI) hasTag(hash, tag string) bool {
	_, exists := parseTagSet(f.torrent(hash).Tags)[tag]
	return exists
}

func (f *fakeAPI) torrent(hash string) qbit.Torrent {
	return *f.torrentPtr(hash)
}

func (f *fakeAPI) torrentPtr(hash string) *qbit.Torrent {
	for i := range f.torrents {
		if f.torrents[i].Hash == hash {
			return &f.torrents[i]
		}
	}
	panic("torrent not found: " + hash)
}

func (f *fakeAPI) clearCalls() {
	f.calls = nil
}

func parseTagSet(tags string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, tag := range strings.Split(tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			result[tag] = struct{}{}
		}
	}
	return result
}

func joinTagSet(tags map[string]struct{}) string {
	values := make([]string, 0, len(tags))
	for tag := range tags {
		values = append(values, tag)
	}
	slices.Sort(values)
	return strings.Join(values, ", ")
}
