package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"chatgptbroker/internal/spacetime"
)

func TestServerSubmitGetCancel(t *testing.T) {
	queue := &fakeQueue{jobs: map[string]spacetime.Job{}}
	server := httptest.NewServer(NewServer(queue, "Pixel Broker", 24*time.Hour).Handler())
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/v1/jobs", "application/json", bytes.NewBufferString(`{
		"telegramChatId": 1001,
		"telegramUserId": 1001,
		"prompt": "hello"
	}`))
	if err != nil {
		t.Fatalf("post job: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created struct {
		Job spacetime.Job `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Job.ProjectName != "Pixel Broker" {
		t.Fatalf("project = %q", created.Job.ProjectName)
	}

	resp, err = http.Post(server.URL+"/api/v1/jobs/"+created.Job.ID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d", resp.StatusCode)
	}
}

type fakeQueue struct {
	mu   sync.Mutex
	jobs map[string]spacetime.Job
}

func (f *fakeQueue) SubmitJob(_ context.Context, id, _, _, _, _, project, _ string, _ time.Duration) (spacetime.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job := spacetime.Job{
		ID:           id,
		Status:       "queued",
		PublicStatus: "Queued",
		ProjectName:  project,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	f.jobs[id] = job
	return job, nil
}

func (f *fakeQueue) ListJobs(_ context.Context) ([]spacetime.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]spacetime.Job, 0, len(f.jobs))
	for _, job := range f.jobs {
		out = append(out, job)
	}
	return out, nil
}

func (f *fakeQueue) GetJob(_ context.Context, id string) (spacetime.Job, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.jobs[id]
	return job, ok, nil
}

func (f *fakeQueue) RequestCancel(_ context.Context, id string) (spacetime.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job := f.jobs[id]
	job.Status = "cancelled"
	job.PublicStatus = "Cancelled"
	job.UpdatedAt = time.Now().UTC()
	f.jobs[id] = job
	return job, nil
}

func (f *fakeQueue) Notifications(_ context.Context) ([]spacetime.Notification, error) {
	return nil, nil
}

func (f *fakeQueue) MarkNotified(_ context.Context, _ string) error {
	return nil
}
