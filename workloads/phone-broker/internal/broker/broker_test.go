package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestTicketPresencePreemptsRunningQRJobAndRetriesAfterGrace(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      40 * time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["digits"] != "12345" {
		t.Fatalf("first generate command digits = %#v", first["digits"])
	}

	if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 1, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr")
	if cancelCommand["requestId"] != job.ID {
		t.Fatalf("cancel requestId = %#v, want %q", cancelCommand["requestId"], job.ID)
	}
	got, ok := b.Job(job.ID)
	if !ok {
		t.Fatalf("job disappeared")
	}
	if got.Status != JobWaiting || got.Reason != "ticket_active" || got.Attempts != 1 {
		t.Fatalf("preempted job = %#v, want waiting ticket_active after one attempt", got)
	}

	if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 0, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	second := upstream.WaitForCommand(t, "generate_control_code")
	if second["requestId"] != job.ID {
		t.Fatalf("retry requestId = %#v, want %q", second["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("qr image"))

	deadline := time.After(2 * time.Second)
	for {
		got, _ = b.Job(job.ID)
		if got.Status == JobSucceeded {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("job did not succeed after retry: %#v", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
	image, ok := b.JobImage(job.ID)
	if !ok {
		t.Fatalf("completed job image missing")
	}
	if string(image.Bytes) != "qr image" || image.MIME != "image/png" {
		t.Fatalf("image = %#v", image)
	}
}

func TestTicketLeaseBlocksQueuedQRJobUntilRelease(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	lease, err := b.AcquireTicketLease(ctx, TicketLeaseInput{
		LeaseID:   "control-code:request-1",
		RequestID: "request-1",
		Reason:    "control_code_request",
		TTL:       time.Second,
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.ID != "control-code:request-1" || lease.Reason != "control_code_request" || lease.RequestID != "request-1" {
		t.Fatalf("lease = %#v", lease)
	}

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "12345", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case command := <-upstream.commands:
		t.Fatalf("QR command started while ticket lease was active: %#v", command)
	case <-time.After(60 * time.Millisecond):
	}
	snap := b.Snapshot(time.Now())
	if snap.CurrentOwner != "ticket" || snap.DesiredOwner != "ticket" || snap.ActiveLease == nil || snap.ActiveLease.ID != lease.ID || snap.BlockedJobs != 1 {
		t.Fatalf("snapshot while leased = %#v", snap)
	}

	if err := b.ReleaseTicketLease(ctx, TicketLeaseInput{LeaseID: lease.ID, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("generate requestId after lease release = %#v, want %q", command["requestId"], job.ID)
	}
}

func TestTicketLeasePreemptsRunningQRJobWithFlowScopedCancel(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "12345", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	upstream.WaitForCommand(t, "generate_control_code")

	if _, err := b.AcquireTicketLease(ctx, TicketLeaseInput{
		LeaseID:   "viewer:session-a",
		RequestID: "session-a",
		Reason:    "stream_viewer",
		TTL:       time.Second,
		Now:       time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr")
	if cancelCommand["requestId"] != job.ID {
		t.Fatalf("cancel requestId = %#v, want %q", cancelCommand["requestId"], job.ID)
	}
	if cancelCommand["owner"] != "rigassatiksme" || cancelCommand["app"] != "rigas_satiksme" || cancelCommand["flow"] != "monthly_ticket" {
		t.Fatalf("cancel command is not flow-scoped: %#v", cancelCommand)
	}
	got, ok := b.Job(job.ID)
	if !ok {
		t.Fatal("job disappeared")
	}
	if got.Status != JobWaiting || got.Reason != "ticket_lease_active" {
		t.Fatalf("preempted job = %#v, want waiting ticket_lease_active", got)
	}
	snap := b.Snapshot(time.Now())
	if snap.LastPreemptionReason != "ticket_lease_active" || snap.LastPreemptionAt == "" {
		t.Fatalf("snapshot missing preemption evidence: %#v", snap)
	}
}

func TestTicketLeaseHTTPAPIExposesStateAndRelease(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL, RunnerInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	body := strings.NewReader(`{"leaseId":"control-code:api-request","requestId":"api-request","reason":"control_code_request","ttlMillis":60000}`)
	resp, err := http.Post(server.URL+"/api/v1/phone/leases/ticket", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		payload := readResponseBody(t, resp)
		t.Fatalf("acquire status = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	var acquired struct {
		OK    bool                `json:"ok"`
		Lease TicketLeaseSnapshot `json:"lease"`
		State StateSnapshot       `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&acquired); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !acquired.OK || acquired.Lease.ID != "control-code:api-request" || acquired.State.CurrentOwner != "ticket" || acquired.State.ActiveLease == nil {
		t.Fatalf("acquired response = %#v", acquired)
	}

	releaseBody := strings.NewReader(`{"leaseId":"control-code:api-request"}`)
	resp, err = http.Post(server.URL+"/api/v1/phone/leases/ticket/release", "application/json", releaseBody)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		payload := readResponseBody(t, resp)
		t.Fatalf("release status = %d, want 200; body=%s", resp.StatusCode, payload)
	}
	var released struct {
		OK    bool          `json:"ok"`
		State StateSnapshot `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&released); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !released.OK || released.State.ActiveLease != nil || released.State.CurrentOwner != "none" {
		t.Fatalf("released response = %#v", released)
	}
}

func TestTicketPreemptionsDoNotConsumeRecoverableQRFailureBudget(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      8 * time.Millisecond,
		MaxTicketQRBlock: 8 * time.Millisecond,
		RunnerInterval:   2 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < maxRecoverableJobAttempts+2; i++ {
		command := upstream.WaitForCommand(t, "generate_control_code")
		if command["requestId"] != job.ID {
			t.Fatalf("preempted generate requestId = %#v, want %q", command["requestId"], job.ID)
		}
		if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 1, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
		cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr")
		if cancelCommand["requestId"] != job.ID {
			t.Fatalf("cancel requestId = %#v, want %q", cancelCommand["requestId"], job.ID)
		}
		if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 0, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Millisecond)
	}

	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("post-ticket generate requestId = %#v, want %q", command["requestId"], job.ID)
	}
	upstream.SendControlCodeFailure(t, job.ID, "qr_image_missing")

	retry := upstream.WaitForCommand(t, "generate_control_code")
	if retry["requestId"] != job.ID {
		t.Fatalf("recoverable retry requestId = %#v, want %q", retry["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("app image after ticket preemptions"))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" {
		t.Fatalf("job reason = %q, want generated", got.Reason)
	}
	image, ok := b.JobImage(job.ID)
	if !ok || string(image.Bytes) != "app image after ticket preemptions" {
		t.Fatalf("image = %#v ok=%v", image, ok)
	}
}

func TestTicketLeasePreemptionsDoNotConsumeRecoverableQRFailureBudget(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      8 * time.Millisecond,
		RunnerInterval:   2 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < maxRecoverableJobAttempts+2; i++ {
		command := upstream.WaitForCommand(t, "generate_control_code")
		if command["requestId"] != job.ID {
			t.Fatalf("preempted generate requestId = %#v, want %q", command["requestId"], job.ID)
		}
		leaseID := "control-code:lease-preemption"
		if _, err := b.AcquireTicketLease(ctx, TicketLeaseInput{
			LeaseID:   leaseID,
			RequestID: "lease-preemption",
			Reason:    "control_code_request",
			TTL:       time.Second,
			Now:       time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr")
		if cancelCommand["requestId"] != job.ID {
			t.Fatalf("cancel requestId = %#v, want %q", cancelCommand["requestId"], job.ID)
		}
		got, ok := b.Job(job.ID)
		if !ok {
			t.Fatal("job disappeared")
		}
		if got.Reason != "ticket_lease_active" {
			t.Fatalf("preempted reason = %q, want ticket_lease_active", got.Reason)
		}
		if err := b.ReleaseTicketLease(ctx, TicketLeaseInput{LeaseID: leaseID, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Millisecond)
	}

	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("post-lease generate requestId = %#v, want %q", command["requestId"], job.ID)
	}
	upstream.SendControlCodeFailure(t, job.ID, "qr_image_missing")

	retry := upstream.WaitForCommand(t, "generate_control_code")
	if retry["requestId"] != job.ID {
		t.Fatalf("recoverable retry requestId = %#v, want %q", retry["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("app image after ticket lease preemptions"))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" {
		t.Fatalf("job reason = %q, want generated", got.Reason)
	}
	image, ok := b.JobImage(job.ID)
	if !ok || string(image.Bytes) != "app image after ticket lease preemptions" {
		t.Fatalf("image = %#v ok=%v", image, ok)
	}
}

func TestQueuedQRWaitsUntilTicketViewerLeaves(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      5 * time.Millisecond,
		MaxTicketQRBlock: 45 * time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	now := time.Now()
	if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 1, Now: now}); err != nil {
		t.Fatal(err)
	}
	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    now,
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case command := <-upstream.commands:
		t.Fatalf("QR command started while ticket viewer was present: %#v", command)
	case <-time.After(75 * time.Millisecond):
	}

	if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 0, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", first["requestId"], job.ID)
	}
}

func TestTicketViewerPresencePreemptsRunningQREvenAfterPriorityWindow(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Second,
		MaxTicketQRBlock: 30 * time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", first["requestId"], job.ID)
	}

	time.Sleep(45 * time.Millisecond)
	if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 1, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}

	cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr")
	if cancelCommand["requestId"] != job.ID {
		t.Fatalf("cancel requestId = %#v, want %q", cancelCommand["requestId"], job.ID)
	}
	got, ok := b.Job(job.ID)
	if !ok {
		t.Fatal("job disappeared")
	}
	if got.Status != JobWaiting || got.Reason != "ticket_active" {
		t.Fatalf("preempted job = %#v, want waiting ticket_active", got)
	}
}

func TestQueuedQRStartsWithTicketSocketsButNoViewers(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Second,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	b.beginTicketSocket()
	defer b.endTicketSocket()

	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap := b.Snapshot(time.Now()); snap.DesiredOwner != "rigassatiksme" || len(snap.DesiredPriority) == 0 || snap.DesiredPriority[0] != "rigassatiksme" {
		t.Fatalf("desired priority with ticket sockets/no viewers = owner %q priority %#v", snap.DesiredOwner, snap.DesiredPriority)
	}
	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", command["requestId"], job.ID)
	}
}

func TestQRJobUsesRigasSatiksmeBatchProtocolAndRequiresAppGeneratedImage(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	if starts := upstream.StartRequests(); starts != 0 {
		t.Fatalf("QR control path sent %d session/start requests; RS automation must not prewarm ViVi", starts)
	}
	if command["app"] != "rigas_satiksme" {
		t.Fatalf("generate command app = %#v, want rigas_satiksme", command["app"])
	}
	if command["flow"] != "monthly_ticket" {
		t.Fatalf("generate command flow = %#v, want monthly_ticket", command["flow"])
	}
	jobs, _ := command["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("batch jobs len = %d, want 1: %#v", len(jobs), jobs)
	}
	first, _ := jobs[0].(map[string]any)
	if first["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", first["requestId"], job.ID)
	}
	if first["digits"] != "54321" {
		t.Fatalf("generate command digits = %#v", first["digits"])
	}
	upstream.SendGeneratedTicketState(t, job.ID, "54321")
	assertJobNotSucceededBriefly(t, b, job.ID)
	upstream.SendControlCodeSuccess(t, job.ID, "54321")
	assertJobNotSucceededBriefly(t, b, job.ID)

	upstream.SendResult(t, job.ID, "image/png", []byte("app generated png from Riga Satiksme"))
	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" {
		t.Fatalf("succeeded reason = %q, want generated", got.Reason)
	}
	image, ok := b.JobImage(job.ID)
	if !ok {
		t.Fatalf("completed job image missing")
	}
	if image.MIME != "image/png" || string(image.Bytes) != "app generated png from Riga Satiksme" {
		t.Fatalf("image = %#v", image)
	}
}

func TestQRJobRetriesPhoneTimeoutAndKeepsSameRequest(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("first generate requestId = %#v, want %q", first["requestId"], job.ID)
	}

	second := upstream.WaitForCommand(t, "generate_control_code")
	if second["requestId"] != job.ID {
		t.Fatalf("retry requestId = %#v, want %q", second["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("qr image after phone timeout retry"))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" || got.Attempts != 2 {
		t.Fatalf("completed retry job = %#v, want generated after two attempts", got)
	}
	image, ok := b.JobImage(job.ID)
	if !ok {
		t.Fatalf("completed retry image missing")
	}
	if image.MIME != "image/png" || string(image.Bytes) != "qr image after phone timeout retry" {
		t.Fatalf("image = %#v", image)
	}
}

func TestQRJobTimeoutSendsCancelWhileWaitingForPhoneResult(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("first generate requestId = %#v, want %q", first["requestId"], job.ID)
	}

	cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr")
	if cancelCommand["requestId"] != job.ID || cancelCommand["reason"] != "job_timeout" {
		t.Fatalf("timeout cancel command = %#v, want same request with job_timeout", cancelCommand)
	}
}

func TestQRJobStoresRigasSatiksmeGeneratedScreenshotCroppedFivePercentTopAndBottom(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	const sourceWidth = 1080
	const sourceHeight = 2424
	cropPixels := rigasSatiksmeGeneratedScreenshotCropPixels(sourceHeight)
	expectedHeight := sourceHeight - 2*cropPixels
	upstream.WaitForCommand(t, "generate_control_code")
	upstream.SendResult(t, job.ID, "image/png", rigasSatiksmeScreenshotFixturePNG(t, sourceWidth, sourceHeight))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" {
		t.Fatalf("succeeded reason = %q, want generated", got.Reason)
	}
	stored, ok := b.JobImage(job.ID)
	if !ok {
		t.Fatalf("completed job image missing")
	}
	if stored.MIME != "image/png" {
		t.Fatalf("stored MIME = %q, want image/png", stored.MIME)
	}
	decoded, err := png.Decode(bytes.NewReader(stored.Bytes))
	if err != nil {
		t.Fatalf("stored PNG decode: %v", err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != sourceWidth || bounds.Dy() != expectedHeight {
		t.Fatalf("stored dimensions = %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), sourceWidth, expectedHeight)
	}
	if gotTop := color.NRGBAModel.Convert(decoded.At(bounds.Min.X, bounds.Min.Y)); gotTop != rigasSatiksmeBodyPixel {
		t.Fatalf("top pixel = %#v, want cropped screenshot body pixel %#v", gotTop, rigasSatiksmeBodyPixel)
	}
	if gotBottom := color.NRGBAModel.Convert(decoded.At(bounds.Min.X, bounds.Max.Y-1)); gotBottom != rigasSatiksmeBodyPixel {
		t.Fatalf("bottom pixel = %#v, want cropped screenshot body pixel %#v", gotBottom, rigasSatiksmeBodyPixel)
	}
}

func TestQRJobRejectsWrongAppImageSource(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	upstream.WaitForCommand(t, "generate_control_code")
	upstream.SendResultWithSource(t, job.ID, "image/png", []byte("wrong app image"), "com.pv.vivi", expectedRigasSatiksmeTicketFlow)

	got := waitForJobStatus(t, b, job.ID, JobFailed)
	if got.Reason != "wrong_qr_source" {
		t.Fatalf("failed reason = %q, want wrong_qr_source", got.Reason)
	}
	if _, ok := b.JobImage(job.ID); ok {
		t.Fatalf("wrong app image must not be stored")
	}
}

func TestQRJobReconnectsControlSocketAndStillAcceptsAppImage(t *testing.T) {
	upstream := newFakePhone(t)
	upstream.disconnectNextGenerate = true
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "13579", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("first requestId = %#v, want %q", first["requestId"], job.ID)
	}
	second := upstream.WaitForCommand(t, "generate_control_code")
	if second["requestId"] != job.ID {
		t.Fatalf("reconnected requestId = %#v, want %q", second["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("rs image"))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" {
		t.Fatalf("job reason = %q, want generated", got.Reason)
	}
	image, ok := b.JobImage(job.ID)
	if !ok || string(image.Bytes) != "rs image" {
		t.Fatalf("image = %#v ok=%v", image, ok)
	}
}

func TestQRJobReconnectsAfterDisconnectedHealthSuccessWithoutBurningAttempt(t *testing.T) {
	upstream := newFakePhone(t)
	upstream.closeAfterGenerateWithHealth = true
	upstream.closeAfterGenerateHealthDelay = 50 * time.Millisecond
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "98765", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", command["requestId"], job.ID)
	}
	if _, ok := b.JobImage(job.ID); ok {
		t.Fatalf("job unexpectedly stored an image from health-only success")
	}

	reconnect := upstream.WaitForCommand(t, "generate_control_code")
	if reconnect["requestId"] != job.ID {
		t.Fatalf("reconnected requestId = %#v, want %q", reconnect["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("app image after health-only reconnect"))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Attempts != 1 || got.Reason != "generated" {
		t.Fatalf("completed reconnected job = %#v, want generated on the original attempt", got)
	}
	image, ok := b.JobImage(job.ID)
	if !ok || image.MIME != "image/png" || string(image.Bytes) != "app image after health-only reconnect" {
		t.Fatalf("completed retry image = %#v ok=%v", image, ok)
	}
}

func TestQRJobDoesNotLetOpenSocketHealthSuccessPreemptAppImage(t *testing.T) {
	upstream := newFakePhone(t)
	upstream.setHealthAfterGenerateNoClose = true
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "98765", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", command["requestId"], job.ID)
	}
	time.Sleep(2 * controlCodeHealthPollInterval)
	got, ok := b.Job(job.ID)
	if !ok || got.Status != JobRunning || got.Attempts != 1 {
		t.Fatalf("job after open-socket health success = %#v ok=%v, want still running first attempt", got, ok)
	}

	upstream.SendResult(t, job.ID, "image/png", []byte("app image after health generated"))
	got = waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Attempts != 1 || got.Reason != "generated" {
		t.Fatalf("completed job = %#v, want generated on first attempt", got)
	}
}

func TestQRJobReconnectsAfterHealthFailedGeneratedWithoutBurningAttempt(t *testing.T) {
	upstream := newFakePhone(t)
	upstream.closeAfterGenerateWithHealth = true
	upstream.closeAfterGenerateHealthStatus = "failed"
	upstream.closeAfterGenerateHealthReason = "generated"
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "98765", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", command["requestId"], job.ID)
	}
	retry := upstream.WaitForCommand(t, "generate_control_code")
	if retry["requestId"] != job.ID {
		t.Fatalf("retry requestId = %#v, want %q", retry["requestId"], job.ID)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("app image after failed generated health retry"))

	got := waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Attempts != 1 || got.Reason != "generated" {
		t.Fatalf("completed reconnected job = %#v, want generated without treating shared health as an RS result", got)
	}
}

func TestQRJobRetriesFailedGeneratedImageResultAsImageMissing(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "24680", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("first requestId = %#v, want %q", first["requestId"], job.ID)
	}
	upstream.SendFailedRigasSatiksmeResult(t, job.ID, "generated")

	second := upstream.WaitForCommand(t, "generate_control_code")
	if second["requestId"] != job.ID {
		t.Fatalf("retry requestId = %#v, want %q", second["requestId"], job.ID)
	}
	got, ok := b.Job(job.ID)
	if !ok {
		t.Fatalf("job disappeared")
	}
	if got.Attempts != 2 || got.Status != JobRunning {
		t.Fatalf("retry job = %#v, want running second attempt after generated failure is treated as image-missing", got)
	}

	upstream.SendResult(t, job.ID, "image/png", []byte("rs image after generated-without-image retry"))
	got = waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Attempts != 2 || got.Reason != "generated" {
		t.Fatalf("succeeded retry job = %#v, want generated after normalized image-missing retry", got)
	}
}

func TestNormalizeRigasSatiksmeQRFailureReasonTreatsGeneratedAsImageMissing(t *testing.T) {
	if got := normalizeRigasSatiksmeQRFailureReason("generated"); got != "qr_image_missing" {
		t.Fatalf("generated failure reason = %q, want qr_image_missing", got)
	}
	if got := normalizeRigasSatiksmeQRFailureReason("  "); got != "qr_image_missing" {
		t.Fatalf("blank failure reason = %q, want qr_image_missing", got)
	}
	if got := normalizeRigasSatiksmeQRFailureReason("rs_monthly_ticket_flow_failed"); got != "rs_monthly_ticket_flow_failed" {
		t.Fatalf("specific failure reason = %q", got)
	}
	if got := normalizeRigasSatiksmeQRFailureReason("rs_monthly_ticket_unknown_state"); got != "rs_monthly_ticket_unknown_state" {
		t.Fatalf("semantic failure reason = %q", got)
	}
}

func TestSemanticRigasSatiksmeFailureRetryPolicy(t *testing.T) {
	transient := []string{
		"phone_timeout",
		"qr_image_missing",
	}
	for _, reason := range transient {
		if !shouldRetryQRJob(reason, 1) {
			t.Fatalf("reason %q should retry once", reason)
		}
	}
	stable := []string{
		"wrong_code",
		"rs_auth_blocked",
		"rs_phone_automation_unavailable",
		"rs_app_launch_failed",
		"rs_app_foreground_failed",
		"rs_register_trip_missing",
		"rs_manual_code_field_missing",
		"rs_confirm_button_missing",
		"rs_monthly_ticket_unknown_state",
		"rs_monthly_ticket_state_timeout",
	}
	for _, reason := range stable {
		if shouldRetryQRJob(reason, 1) {
			t.Fatalf("reason %q should not retry", reason)
		}
	}
}

func TestQRJobsWithinBurstWindowUseRigasSatiksmeBatchCommand(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	now := time.Now()
	firstJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "11111", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	secondJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "22222", Now: now.Add(90 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	if command["owner"] != "rigassatiksme" || command["app"] != "rigas_satiksme" || command["flow"] != "monthly_ticket" {
		t.Fatalf("batch envelope = %#v, want RS monthly-ticket owner/app/flow", command)
	}
	if command["ticketPriorityActive"] != false {
		t.Fatalf("ticketPriorityActive = %#v, want false", command["ticketPriorityActive"])
	}
	jobs, ok := command["jobs"].([]any)
	if !ok || len(jobs) != 2 {
		t.Fatalf("batch jobs = %#v, want 2 jobs", command["jobs"])
	}
	firstPayload, _ := jobs[0].(map[string]any)
	secondPayload, _ := jobs[1].(map[string]any)
	if firstPayload["requestId"] != firstJob.ID || firstPayload["digits"] != "11111" {
		t.Fatalf("first batch job = %#v", firstPayload)
	}
	if secondPayload["requestId"] != secondJob.ID || secondPayload["digits"] != "22222" {
		t.Fatalf("second batch job = %#v", secondPayload)
	}
}

func TestQRJobsWithinBurstUseSingleRigasSatiksmeBatchCommand(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	now := time.Now()
	firstJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "68803", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	secondJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "58011", Now: now.Add(80 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	thirdJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "27515", Now: now.Add(190 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	if command["owner"] != "rigassatiksme" || command["app"] != "rigas_satiksme" || command["flow"] != "monthly_ticket" {
		t.Fatalf("batch envelope = %#v, want RS monthly-ticket owner/app/flow", command)
	}
	jobs, ok := command["jobs"].([]any)
	if !ok || len(jobs) != 3 {
		t.Fatalf("batch jobs = %#v, want 3 jobs", command["jobs"])
	}
	want := []struct {
		id   string
		code string
	}{
		{firstJob.ID, "68803"},
		{secondJob.ID, "58011"},
		{thirdJob.ID, "27515"},
	}
	for index, expected := range want {
		item, ok := jobs[index].(map[string]any)
		if !ok {
			t.Fatalf("job %d payload = %#v", index, jobs[index])
		}
		if item["requestId"] != expected.id || item["digits"] != expected.code {
			t.Fatalf("job %d = %#v, want requestId=%q digits=%q", index, item, expected.id, expected.code)
		}
	}
}

func TestRigasSatiksmeBatchResultsCompleteMatchingJobsOutOfOrder(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	now := time.Now()
	firstJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "68803", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	secondJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "58011", Now: now.Add(80 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	thirdJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "27515", Now: now.Add(190 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}

	upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	upstream.SendResult(t, secondJob.ID, "image/png", []byte("second image"))
	upstream.SendFailedRigasSatiksmeResult(t, thirdJob.ID, "code_rejected_by_rs")
	upstream.SendResult(t, firstJob.ID, "image/png", []byte("first image"))

	second := waitForJobStatus(t, b, secondJob.ID, JobSucceeded)
	if second.Reason != "generated" {
		t.Fatalf("second job = %#v", second)
	}
	third := waitForJobStatus(t, b, thirdJob.ID, JobFailed)
	if third.Reason != "code_rejected_by_rs" {
		t.Fatalf("third job = %#v", third)
	}
	first := waitForJobStatus(t, b, firstJob.ID, JobSucceeded)
	if first.Reason != "generated" {
		t.Fatalf("first job = %#v", first)
	}
	firstImage, ok := b.JobImage(firstJob.ID)
	if !ok || string(firstImage.Bytes) != "first image" {
		t.Fatalf("first image = %#v ok=%v", firstImage, ok)
	}
	secondImage, ok := b.JobImage(secondJob.ID)
	if !ok || string(secondImage.Bytes) != "second image" {
		t.Fatalf("second image = %#v ok=%v", secondImage, ok)
	}
}

func TestTicketLeasePreemptsRunningRigasSatiksmeBatchWithoutBurningAttempts(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	now := time.Now()
	firstJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "68803", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	secondJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "58011", Now: now.Add(80 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	thirdJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "27515", Now: now.Add(190 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	command := upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	batchID, _ := command["batchId"].(string)
	if batchID == "" {
		t.Fatalf("batch command missing batchId: %#v", command)
	}

	if _, err := b.AcquireTicketLease(ctx, TicketLeaseInput{
		LeaseID:   "viewer:batch-preempt",
		RequestID: "batch-preempt",
		Reason:    "stream_viewer",
		TTL:       time.Second,
		Now:       time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr_batch")
	if cancelCommand["batchId"] != batchID {
		t.Fatalf("cancel batchId = %#v, want %q", cancelCommand["batchId"], batchID)
	}
	requestIDs, ok := cancelCommand["requestIds"].([]any)
	if !ok || len(requestIDs) != 3 {
		t.Fatalf("cancel requestIds = %#v, want 3", cancelCommand["requestIds"])
	}
	for _, jobID := range []string{firstJob.ID, secondJob.ID, thirdJob.ID} {
		got, ok := b.Job(jobID)
		if !ok {
			t.Fatalf("job %s disappeared", jobID)
		}
		if got.Status != JobWaiting || got.Reason != "ticket_lease_active" || got.Attempts != 1 {
			t.Fatalf("preempted job %s = %#v, want waiting ticket_lease_active after one attempt", jobID, got)
		}
	}
}

func TestQRJobDoesNotFinalizeFromSharedControlCodeHealth(t *testing.T) {
	upstream := newFakePhone(t)
	upstream.setHealthAfterGenerateNoClose = true
	upstream.closeAfterGenerateHealthStatus = "failed"
	upstream.closeAfterGenerateHealthReason = "wrong_code"
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	command := upstream.WaitForCommand(t, "generate_control_code")
	if command["requestId"] != job.ID {
		t.Fatalf("generate requestId = %#v, want %q", command["requestId"], job.ID)
	}

	time.Sleep(2 * controlCodeHealthPollInterval)
	got, ok := b.Job(job.ID)
	if !ok || got.Status != JobRunning || got.Attempts != 1 {
		t.Fatalf("job after shared control-code health = %#v ok=%v, want still running first RS attempt", got, ok)
	}
	upstream.SendResult(t, job.ID, "image/png", []byte("explicit rs result"))
	got = waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Reason != "generated" || got.Attempts != 1 {
		t.Fatalf("job after explicit RS result = %#v", got)
	}
}

func TestRigasSatiksmeBatchReconcilesFinalFailureFromRSHealth(t *testing.T) {
	upstream := newFakePhone(t)
	upstream.closeAfterBatchCommandWithRigasSatiksmeHealth = true
	upstream.closeAfterBatchHealthJobIndex = 1
	upstream.closeAfterBatchHealthStatus = "failed"
	upstream.closeAfterBatchHealthReason = "rs_manual_code_field_missing"
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	now := time.Now()
	firstJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "68803", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	secondJob, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "58011", Now: now.Add(80 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}

	upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	second := waitForJobStatus(t, b, secondJob.ID, JobFailed)
	if second.Reason != "rs_manual_code_field_missing" {
		t.Fatalf("reconciled second job = %#v", second)
	}
	retry := upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	retryJobs, _ := retry["jobs"].([]any)
	if len(retryJobs) != 1 {
		t.Fatalf("retry batch jobs = %#v, want one remaining job", retryJobs)
	}
	retryJob, _ := retryJobs[0].(map[string]any)
	if retryJob["requestId"] != firstJob.ID {
		t.Fatalf("retry batch requestId = %#v, want %q", retryJob["requestId"], firstJob.ID)
	}
	upstream.SendFailedRigasSatiksmeResult(t, firstJob.ID, "code_rejected_by_rs")
	first := waitForJobStatus(t, b, firstJob.ID, JobFailed)
	if first.Reason != "code_rejected_by_rs" {
		t.Fatalf("remaining first job = %#v", first)
	}
	if snap := b.Snapshot(time.Now()); snap.RunningQRJob {
		t.Fatalf("running QR job remained after RS health reconciliation: %#v", snap)
	}
}

func TestQRJobRetriesGeneratedControlCodeResultAsImageMissing(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("first requestId = %#v, want %q", first["requestId"], job.ID)
	}
	upstream.SendControlCodeFailure(t, job.ID, "generated")

	second := upstream.WaitForCommand(t, "generate_control_code")
	if second["requestId"] != job.ID {
		t.Fatalf("retry requestId = %#v, want %q", second["requestId"], job.ID)
	}
	got, ok := b.Job(job.ID)
	if !ok {
		t.Fatalf("job disappeared")
	}
	if got.Attempts != 2 || got.Status != JobRunning {
		t.Fatalf("retry job = %#v, want running second attempt after generated control-code result is treated as image-missing", got)
	}

	upstream.SendResult(t, job.ID, "image/png", []byte("rs image after generated control-code retry"))
	got = waitForJobStatus(t, b, job.ID, JobSucceeded)
	if got.Attempts != 2 || got.Reason != "generated" {
		t.Fatalf("succeeded retry job = %#v, want generated after normalized control-code image-missing retry", got)
	}
}

func TestQRJobFailsFromControlCodeResultReason(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	upstream.WaitForCommand(t, "generate_control_code")
	upstream.SendControlCodeFailure(t, job.ID, "control_code_submit_missing")

	got := waitForJobStatus(t, b, job.ID, JobFailed)
	if got.Reason != "control_code_submit_missing" {
		t.Fatalf("failed reason = %q, want control_code_submit_missing", got.Reason)
	}
}

func TestQRJobFinalizesSpecificRigasSatiksmePhoneFailuresWithoutBrokerRetry(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	first := upstream.WaitForCommand(t, "generate_control_code")
	if first["requestId"] != job.ID {
		t.Fatalf("first requestId = %#v, want %q", first["requestId"], job.ID)
	}
	upstream.SendControlCodeFailure(t, job.ID, "rs_monthly_ticket_stale_code")

	got := waitForJobStatus(t, b, job.ID, JobFailed)
	if got.Attempts != 1 || got.Reason != "rs_monthly_ticket_stale_code" {
		t.Fatalf("specific phone failure = %#v, want final failure without retry", got)
	}
}

func TestHTTPQRJobAPIValidatesCodeAndReturnsImage(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		TicketGrace:      time.Millisecond,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/v1/qr/jobs", "application/json", strings.NewReader(`{"chatId":"1001","userId":"42","code":"1234"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid code status = %d, want 400", resp.StatusCode)
	}

	resp, err = http.Post(server.URL+"/api/v1/qr/jobs", "application/json", strings.NewReader(`{"chatId":"1001","userId":"42","code":"54321"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create job status = %d, want 202", resp.StatusCode)
	}
	var created struct {
		OK  bool  `json:"ok"`
		Job QRJob `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Job.ID == "" || created.Job.Status != JobWaiting {
		t.Fatalf("created payload = %#v", created)
	}

	upstream.WaitForCommand(t, "generate_control_code")
	upstream.SendResult(t, created.Job.ID, "image/png", []byte("png"))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		imageResp, err := http.Get(server.URL + "/api/v1/qr/jobs/" + created.Job.ID + "/image")
		if err != nil {
			t.Fatal(err)
		}
		if imageResp.StatusCode == http.StatusOK {
			defer imageResp.Body.Close()
			if got := imageResp.Header.Get("Content-Type"); got != "image/png" {
				t.Fatalf("image content type = %q", got)
			}
			return
		}
		_ = imageResp.Body.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("image endpoint did not return completed QR image")
}

func TestTicketStreamProxyAllowsLargeH264Frames(t *testing.T) {
	payload := make([]byte, 96*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			t.Errorf("accept upstream stream websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test done")
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
			t.Errorf("write upstream stream frame: %v", err)
		}
	}))
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/api/v1/stream", &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(websocketProxyReadLimitBytes)
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read proxied stream frame: %v", err)
	}
	if typ != websocket.MessageBinary {
		t.Fatalf("proxied stream message type = %v, want binary", typ)
	}
	if len(data) != len(payload) {
		t.Fatalf("proxied stream frame length = %d, want %d", len(data), len(payload))
	}
	for i := range payload {
		if data[i] != payload[i] {
			t.Fatalf("proxied stream frame byte %d = %d, want %d", i, data[i], payload[i])
		}
	}
}

func TestHTTPQRJobAPIRedactsSensitiveJobFields(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	const (
		chatID = "secret-chat-redaction"
		userID = "secret-user-redaction"
		code   = "54321"
	)
	forbidden := []string{code, chatID, userID, "\"code\"", "\"chatId\"", "\"userId\""}

	resp, err := http.Post(server.URL+"/api/v1/qr/jobs", "application/json", strings.NewReader(`{"chatId":"`+chatID+`","userId":"`+userID+`","code":"`+code+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted {
		body := readResponseBody(t, resp)
		t.Fatalf("create job status = %d, want 202; body=%s", resp.StatusCode, string(body))
	}
	createBody := readResponseBody(t, resp)
	assertJobBodyRedacted(t, createBody, forbidden...)
	var created struct {
		OK  bool  `json:"ok"`
		Job QRJob `json:"job"`
	}
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Job.ID == "" || created.Job.Code != "" || created.Job.ChatID != "" || created.Job.UserID != "" {
		t.Fatalf("created payload exposed private job fields: %#v", created)
	}

	for _, target := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/qr/jobs/" + created.Job.ID},
		{method: http.MethodGet, path: "/api/v1/qr/jobs/latest?userId=" + userID},
		{method: http.MethodPost, path: "/api/v1/qr/jobs/" + created.Job.ID + "/cancel"},
	} {
		req, err := http.NewRequest(target.method, server.URL+target.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			body := readResponseBody(t, resp)
			t.Fatalf("%s %s status = %d, want 200; body=%s", target.method, target.path, resp.StatusCode, string(body))
		}
		body := readResponseBody(t, resp)
		assertJobBodyRedacted(t, body, forbidden...)
	}
}

func TestSnapshotDesiredPriorityPrefersTicketOverQueuedQR(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if snap := b.Snapshot(now); snap.DesiredOwner != "none" || len(snap.DesiredPriority) != 0 {
		t.Fatalf("idle desired priority = owner %q priority %#v, want none/empty", snap.DesiredOwner, snap.DesiredPriority)
	}
	if _, err := b.EnqueueQRJob(context.Background(), QRJobInput{ChatID: "1001", UserID: "42", Code: "12345", Now: now}); err != nil {
		t.Fatal(err)
	}
	if snap := b.Snapshot(now); snap.DesiredOwner != "rigassatiksme" || len(snap.DesiredPriority) != 1 || snap.DesiredPriority[0] != "rigassatiksme" {
		t.Fatalf("QR desired priority = owner %q priority %#v", snap.DesiredOwner, snap.DesiredPriority)
	}
	if err := b.UpdateTicketPresence(context.Background(), TicketPresenceInput{Viewers: 1, Now: now}); err != nil {
		t.Fatal(err)
	}
	if snap := b.Snapshot(now); snap.DesiredOwner != "ticket" || len(snap.DesiredPriority) != 2 || snap.DesiredPriority[0] != "ticket" || snap.DesiredPriority[1] != "rigassatiksme" {
		t.Fatalf("ticket desired priority = owner %q priority %#v", snap.DesiredOwner, snap.DesiredPriority)
	}
}

func TestSnapshotJSONRedactsSensitiveJobFields(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	job, err := b.EnqueueQRJob(context.Background(), QRJobInput{ChatID: "secret-chat", UserID: "secret-user", Code: "54321", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(b.Snapshot(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for i, forbidden := range []string{"54321", job.ID, "secret-chat", "secret-user", "runningJobId"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("snapshot JSON leaked sensitive field at index %d", i)
		}
	}
	if !strings.Contains(body, "\"queueDepth\":1") || !strings.Contains(body, "\"status\":\"waiting\"") {
		t.Fatalf("snapshot JSON missing safe queue/job status fields")
	}
}

func TestAnalyticsEndpointSummarizesUserImpactWithoutSensitiveFields(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	base := time.Date(2026, 5, 21, 7, 52, 20, 0, time.UTC)
	failed, err := b.EnqueueQRJob(context.Background(), QRJobInput{ChatID: "secret-chat-a", UserID: "secret-user-a", Code: "54321", Now: base})
	if err != nil {
		t.Fatal(err)
	}
	slowSuccess, err := b.EnqueueQRJob(context.Background(), QRJobInput{ChatID: "secret-chat-b", UserID: "secret-user-b", Code: "12345", Now: base.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}

	b.mu.Lock()
	failedJob := b.jobs[failed.ID]
	failedJob.Status = JobFailed
	failedJob.Reason = "rs_monthly_ticket_image_capture_failed"
	failedJob.Attempts = 3
	failedJob.CreatedAt = base.Format(time.RFC3339Nano)
	failedJob.StartedAt = base.Add(150 * time.Second).Format(time.RFC3339Nano)
	failedJob.CompletedAt = base.Add(164 * time.Second).Format(time.RFC3339Nano)
	failedJob.UpdatedAt = failedJob.CompletedAt
	failedJob.Phone = RSQRPhoneSummary{
		SourceApp:           expectedRigasSatiksmeSourceApp,
		TicketFlow:          expectedRigasSatiksmeTicketFlow,
		TotalDurationMillis: 14000,
		Phases: map[string]int64{
			"rs_monthly_ticket_start_state":    1200,
			"rs_monthly_ticket_proof_finished": 9000,
		},
	}
	succeededJob := b.jobs[slowSuccess.ID]
	succeededJob.Status = JobSucceeded
	succeededJob.Reason = "generated"
	succeededJob.Attempts = 2
	succeededJob.CreatedAt = base.Add(time.Minute).Format(time.RFC3339Nano)
	succeededJob.StartedAt = base.Add(80 * time.Second).Format(time.RFC3339Nano)
	succeededJob.CompletedAt = base.Add(136 * time.Second).Format(time.RFC3339Nano)
	succeededJob.UpdatedAt = succeededJob.CompletedAt
	b.mu.Unlock()

	resp, err := http.Get(server.URL + "/api/v1/analytics")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body := readResponseBody(t, resp)
		t.Fatalf("analytics status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	body := readResponseBody(t, resp)
	for _, forbidden := range []string{"54321", "12345", failed.ID, slowSuccess.ID, "secret-chat-a", "secret-chat-b", "secret-user-a", "secret-user-b", "\"code\"", "\"chatId\"", "\"userId\""} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("analytics response leaked sensitive value %q in body %s", forbidden, string(body))
		}
	}

	var decoded struct {
		OK        bool              `json:"ok"`
		Analytics AnalyticsSnapshot `json:"analytics"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK {
		t.Fatalf("analytics ok=false: %#v", decoded)
	}
	if decoded.Analytics.RSQR.Totals.Jobs != 2 || decoded.Analytics.RSQR.Totals.Failed != 1 || decoded.Analytics.RSQR.Totals.Succeeded != 1 {
		t.Fatalf("analytics totals = %#v", decoded.Analytics.RSQR.Totals)
	}
	if decoded.Analytics.RSQR.Totals.Retried != 2 || decoded.Analytics.RSQR.Totals.SlowSuccess != 1 {
		t.Fatalf("analytics retry/slow totals = %#v", decoded.Analytics.RSQR.Totals)
	}
	if got := decoded.Analytics.RSQR.ByReason["rs_monthly_ticket_image_capture_failed"]; got != 1 {
		t.Fatalf("analytics byReason image_capture_failed = %d, want 1", got)
	}
	if decoded.Analytics.RSQR.SuccessLatencySec.Count != 1 || decoded.Analytics.RSQR.SuccessLatencySec.P90 <= 0 {
		t.Fatalf("analytics success latency stats = %#v", decoded.Analytics.RSQR.SuccessLatencySec)
	}
	if len(decoded.Analytics.RSQR.RecentIncidents) != 2 {
		t.Fatalf("analytics recent incidents len = %d, want 2: %#v", len(decoded.Analytics.RSQR.RecentIncidents), decoded.Analytics.RSQR.RecentIncidents)
	}
	incident := decoded.Analytics.RSQR.RecentIncidents[0]
	if incident.ActorHash == "" || incident.ActorHash == "secret-user-a" || incident.Reason != "rs_monthly_ticket_image_capture_failed" || incident.TotalSec <= 0 || incident.QueueSec <= 0 || incident.FinalAttemptSec <= 0 {
		t.Fatalf("analytics incident missing sanitized actor/reason/timings: %#v", incident)
	}
	if incident.Phone.TotalDurationMillis != 14000 || incident.Phone.Phases["rs_monthly_ticket_start_state"] != 1200 || incident.Phone.SourceApp != expectedRigasSatiksmeSourceApp {
		t.Fatalf("analytics incident missing safe phone phase summary: %#v", incident.Phone)
	}
	if len(decoded.Analytics.RSQR.UserImpact) != 2 {
		t.Fatalf("analytics user impact len = %d, want 2: %#v", len(decoded.Analytics.RSQR.UserImpact), decoded.Analytics.RSQR.UserImpact)
	}
	impactByActor := map[string]RSQRUserImpact{}
	for _, impact := range decoded.Analytics.RSQR.UserImpact {
		impactByActor[impact.ActorHash] = impact
	}
	failedImpact := impactByActor[incident.ActorHash]
	if failedImpact.ActorHash == "" || failedImpact.Jobs != 1 || failedImpact.Failed != 1 || failedImpact.Retried != 1 || failedImpact.LastReason != "rs_monthly_ticket_image_capture_failed" || failedImpact.LastAt == "" {
		t.Fatalf("analytics user impact missing failed actor rollup: %#v", failedImpact)
	}
}

func TestAnalyticsMarksRSSuccessSlowAtFifteenSecondTarget(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	job, err := b.EnqueueQRJob(context.Background(), QRJobInput{ChatID: "chat", UserID: "user", Code: "12345", Now: base})
	if err != nil {
		t.Fatal(err)
	}

	b.mu.Lock()
	stored := b.jobs[job.ID]
	stored.Status = JobSucceeded
	stored.Reason = "generated"
	stored.Attempts = 1
	stored.StartedAt = base.Format(time.RFC3339Nano)
	stored.CompletedAt = base.Add(16 * time.Second).Format(time.RFC3339Nano)
	stored.UpdatedAt = stored.CompletedAt
	b.mu.Unlock()

	analytics := b.Analytics(base.Add(20 * time.Second))
	if analytics.RSQR.Totals.SlowSuccess != 1 {
		t.Fatalf("slow success count = %d, want 1 for RS success over 15 seconds", analytics.RSQR.Totals.SlowSuccess)
	}
	if len(analytics.RSQR.RecentIncidents) != 1 || !analytics.RSQR.RecentIncidents[0].SlowSuccess {
		t.Fatalf("recent incidents should include slow 16s RS success: %#v", analytics.RSQR.RecentIncidents)
	}
}

func TestQRJobStoresPhonePhaseSummaryFromRigasSatiksmeResult(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		RunnerInterval:   5 * time.Millisecond,
		PhoneSendTimeout: 200 * time.Millisecond,
		JobTimeout:       time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	job, err := b.EnqueueQRJob(ctx, QRJobInput{
		ChatID: "secret-chat",
		UserID: "secret-user",
		Code:   "24680",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	upstream.WaitForCommand(t, "generate_control_code")
	upstream.SendFailedRigasSatiksmeResultWithPhases(t, job.ID, "rs_monthly_ticket_stale_code", map[string]int64{
		"rs_monthly_ticket_start_state":    1100,
		"rs_monthly_ticket_proof_finished": 8200,
	})

	got := waitForJobStatus(t, b, job.ID, JobFailed)
	if got.Attempts != 1 || got.Reason != "rs_monthly_ticket_stale_code" {
		t.Fatalf("specific phone failure = %#v, want final failure without retry", got)
	}
	b.mu.Lock()
	stored := cloneJob(*b.jobs[job.ID])
	b.mu.Unlock()
	if stored.Phone.TotalDurationMillis != 0 || stored.Phone.Phases["rs_monthly_ticket_start_state"] != 1100 {
		t.Fatalf("final phone failure should retain phone phase summary for incident tracing: %#v", stored.Phone)
	}
}

func TestDurableStateRestoresQueuedAndRunningJobs(t *testing.T) {
	statePath := t.TempDir() + "/jobs.json"
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL, StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	job, err := b.EnqueueQRJob(context.Background(), QRJobInput{
		ChatID: "1001",
		UserID: "42",
		Code:   "12345",
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := New(Config{UpstreamBaseURL: upstream.URL, StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := restored.Job(job.ID)
	if !ok || got.Status != JobWaiting || got.Code != "12345" {
		t.Fatalf("restored queued job = %#v, ok=%v", got, ok)
	}

	runningState, err := json.Marshal(persistedState{Jobs: []QRJob{{
		ID:        "running-job",
		ChatID:    "1001",
		UserID:    "42",
		Code:      "54321",
		Status:    JobRunning,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, runningState, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err = New(Config{UpstreamBaseURL: upstream.URL, StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	got, ok = restored.Job("running-job")
	if !ok || got.Status != JobWaiting || got.Reason != "broker_restarted" {
		t.Fatalf("restored running job = %#v, ok=%v", got, ok)
	}
}

func TestHealthExposesUpstreamControlCodeRequestForCommandAcceptance(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()
	upstream.mu.Lock()
	upstream.controlCodeRequest = map[string]any{
		"requestId": "req-accepted",
		"status":    "running",
		"reason":    "generated",
	}
	upstream.mu.Unlock()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d body = %s", resp.StatusCode, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	raw, ok := payload["controlCodeRequest"].(map[string]any)
	if !ok {
		t.Fatalf("health did not expose upstream controlCodeRequest: %s", body)
	}
	if raw["requestId"] != "req-accepted" || raw["status"] != "running" {
		t.Fatalf("controlCodeRequest = %#v", raw)
	}
	if _, ok := payload["state"].(map[string]any); !ok {
		t.Fatalf("health response lost broker state: %s", body)
	}
}

func assertJobBodyRedacted(t *testing.T, body []byte, forbidden ...string) {
	t.Helper()
	text := string(body)
	for _, value := range forbidden {
		if strings.Contains(text, value) {
			t.Fatalf("job response leaked sensitive value %q in body %s", value, text)
		}
	}
}

func readResponseBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

type fakePhone struct {
	*httptest.Server
	commands                                      chan map[string]any
	currentResults                                chan map[string]any
	closeAfterGenerateWithHealth                  bool
	setHealthAfterGenerateNoClose                 bool
	closeAfterGenerateHealthDelay                 time.Duration
	closeAfterGenerateHealthStatus                string
	closeAfterGenerateHealthReason                string
	closeAfterBatchCommandWithRigasSatiksmeHealth bool
	closeAfterBatchHealthJobIndex                 int
	closeAfterBatchHealthStatus                   string
	closeAfterBatchHealthReason                   string
	disconnectNextGenerate                        bool
	startRequests                                 int
	controlCodeRequest                            map[string]any
	rigasSatiksmeBatch                            map[string]any
	mu                                            sync.Mutex
}

func newFakePhone(t *testing.T) *fakePhone {
	t.Helper()
	f := &fakePhone{
		commands: make(chan map[string]any, 16),
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			f.mu.Lock()
			f.startRequests++
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/health":
			f.mu.Lock()
			controlCodeRequest := cloneMap(f.controlCodeRequest)
			rigasSatiksmeBatch := cloneMap(f.rigasSatiksmeBatch)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                 true,
				"controlCodeRequest": controlCodeRequest,
				"rigasSatiksmeBatch": rigasSatiksmeBatch,
			})
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			results := make(chan map[string]any, 4)
			f.mu.Lock()
			f.currentResults = results
			f.mu.Unlock()
			go func() {
				for result := range results {
					body, _ := json.Marshal(result)
					_ = conn.Write(ctx, websocket.MessageText, body)
				}
			}()
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var command map[string]any
				if err := json.Unmarshal(data, &command); err != nil {
					continue
				}
				f.commands <- command
				generateCommand, isGenerateCommand := fakeLegacyGenerateCommand(command)
				if isGenerateCommand {
					f.mu.Lock()
					disconnect := f.disconnectNextGenerate
					if disconnect {
						f.disconnectNextGenerate = false
					}
					f.mu.Unlock()
					if disconnect {
						return
					}
				}
				if f.closeAfterGenerateWithHealth && isGenerateCommand {
					requestID, _ := generateCommand["requestId"].(string)
					value, _ := generateCommand["digits"].(string)
					f.mu.Lock()
					f.closeAfterGenerateWithHealth = false
					status := f.closeAfterGenerateHealthStatus
					reason := f.closeAfterGenerateHealthReason
					f.mu.Unlock()
					if status == "" {
						status = "succeeded"
					}
					if reason == "" {
						reason = "generated"
					}
					setResult := func(status string, reason string, value string) {
						f.mu.Lock()
						f.controlCodeRequest = map[string]any{
							"requestId": requestID,
							"status":    status,
							"reason":    reason,
							"value":     value,
						}
						f.mu.Unlock()
					}
					if f.closeAfterGenerateHealthDelay > 0 {
						setResult("running", "", "")
						delay := f.closeAfterGenerateHealthDelay
						go func() {
							time.Sleep(delay)
							setResult(status, reason, "")
						}()
					} else {
						setResult(status, reason, value)
					}
					return
				}
				if f.setHealthAfterGenerateNoClose && isGenerateCommand {
					requestID, _ := generateCommand["requestId"].(string)
					value, _ := generateCommand["digits"].(string)
					f.mu.Lock()
					f.setHealthAfterGenerateNoClose = false
					status := f.closeAfterGenerateHealthStatus
					reason := f.closeAfterGenerateHealthReason
					if status == "" {
						status = "succeeded"
					}
					if reason == "" {
						reason = "generated"
					}
					f.controlCodeRequest = map[string]any{
						"requestId": requestID,
						"status":    status,
						"reason":    reason,
						"value":     value,
					}
					f.mu.Unlock()
				}
				if command["type"] == "generate_rigassatiksme_qr_batch" {
					f.mu.Lock()
					closeWithHealth := f.closeAfterBatchCommandWithRigasSatiksmeHealth
					jobIndex := f.closeAfterBatchHealthJobIndex
					status := f.closeAfterBatchHealthStatus
					reason := f.closeAfterBatchHealthReason
					f.closeAfterBatchCommandWithRigasSatiksmeHealth = false
					f.mu.Unlock()
					if closeWithHealth {
						jobs, _ := command["jobs"].([]any)
						if jobIndex < 0 || jobIndex >= len(jobs) {
							jobIndex = 0
						}
						requestID := ""
						if jobIndex < len(jobs) {
							if item, ok := jobs[jobIndex].(map[string]any); ok {
								requestID, _ = item["requestId"].(string)
							}
						}
						if status == "" {
							status = "failed"
						}
						if reason == "" {
							reason = "rs_monthly_ticket_unknown_state"
						}
						f.mu.Lock()
						f.rigasSatiksmeBatch = map[string]any{
							"batchId":             command["batchId"],
							"status":              "completed",
							"lastResultRequestId": requestID,
							"lastResultStatus":    status,
							"lastResultReason":    reason,
							"phases": map[string]any{
								"rs_monthly_ticket_drive_started": float64(25),
							},
						}
						f.mu.Unlock()
						return
					}
				}
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept stream websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakePhone) StartRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startRequests
}

func (f *fakePhone) WaitForCommand(t *testing.T, commandType string) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case command := <-f.commands:
			if command["type"] == commandType {
				return command
			}
			if commandType == "generate_control_code" {
				if legacy, ok := fakeLegacyGenerateCommand(command); ok {
					return legacy
				}
			}
			if commandType == "cancel_rigassatiksme_qr" {
				if legacy, ok := fakeLegacyCancelCommand(command); ok {
					return legacy
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s command", commandType)
		}
	}
}

func fakeLegacyGenerateCommand(command map[string]any) (map[string]any, bool) {
	if command["type"] == "generate_control_code" {
		return command, true
	}
	if command["type"] != "generate_rigassatiksme_qr_batch" {
		return nil, false
	}
	jobs, _ := command["jobs"].([]any)
	if len(jobs) != 1 {
		return nil, false
	}
	job, _ := jobs[0].(map[string]any)
	if job == nil {
		return nil, false
	}
	legacy := cloneMap(command)
	legacy["type"] = "generate_control_code"
	legacy["requestId"] = job["requestId"]
	legacy["digits"] = job["digits"]
	legacy["createdAt"] = job["createdAt"]
	legacy["resultImage"] = true
	return legacy, true
}

func fakeLegacyCancelCommand(command map[string]any) (map[string]any, bool) {
	if command["type"] == "cancel_rigassatiksme_qr" {
		return command, true
	}
	if command["type"] != "cancel_rigassatiksme_qr_batch" {
		return nil, false
	}
	requestIDs, _ := command["requestIds"].([]any)
	if len(requestIDs) != 1 {
		return nil, false
	}
	legacy := cloneMap(command)
	legacy["type"] = "cancel_rigassatiksme_qr"
	legacy["requestId"] = requestIDs[0]
	return legacy, true
}

func (f *fakePhone) SendResult(t *testing.T, requestID string, mime string, image []byte) {
	f.SendResultWithSource(t, requestID, mime, image, expectedRigasSatiksmeSourceApp, expectedRigasSatiksmeTicketFlow)
}

func (f *fakePhone) SendResultWithSource(t *testing.T, requestID string, mime string, image []byte, sourceApp string, ticketFlow string) {
	t.Helper()
	f.mu.Lock()
	results := f.currentResults
	f.mu.Unlock()
	if results == nil {
		t.Fatalf("no active phone session for result")
	}
	results <- map[string]any{
		"type":        "rigassatiksme_qr_result",
		"requestId":   requestID,
		"ok":          true,
		"imageMime":   mime,
		"imageBase64": base64.StdEncoding.EncodeToString(image),
		"sourceApp":   sourceApp,
		"ticketFlow":  ticketFlow,
	}
}

func (f *fakePhone) SendFailedRigasSatiksmeResult(t *testing.T, requestID string, reason string) {
	t.Helper()
	f.SendFailedRigasSatiksmeResultWithPhases(t, requestID, reason, nil)
}

func (f *fakePhone) SendFailedRigasSatiksmeResultWithPhases(t *testing.T, requestID string, reason string, phases map[string]int64) {
	t.Helper()
	f.mu.Lock()
	results := f.currentResults
	f.mu.Unlock()
	if results == nil {
		t.Fatalf("no active phone session for result")
	}
	phasePayload := map[string]any{}
	for key, value := range phases {
		phasePayload[key] = value
	}
	results <- map[string]any{
		"type":      "rigassatiksme_qr_result",
		"requestId": requestID,
		"ok":        false,
		"accepted":  false,
		"reason":    reason,
		"phases":    phasePayload,
	}
}

func (f *fakePhone) SendGeneratedTicketState(t *testing.T, requestID string, value string) {
	t.Helper()
	f.mu.Lock()
	results := f.currentResults
	f.mu.Unlock()
	if results == nil {
		t.Fatalf("no active phone session for result")
	}
	results <- map[string]any{
		"type":             "ticket_state_event",
		"ticketState":      "generated_result",
		"requestId":        requestID,
		"reason":           "generated",
		"value":            value,
		"streamEpoch":      1,
		"frameSequence":    2,
		"minFrameSequence": 2,
	}
}

func (f *fakePhone) SendControlCodeSuccess(t *testing.T, requestID string, value string) {
	t.Helper()
	f.mu.Lock()
	results := f.currentResults
	f.mu.Unlock()
	if results == nil {
		t.Fatalf("no active phone session for result")
	}
	results <- map[string]any{
		"type":      "control_code_result",
		"requestId": requestID,
		"ok":        true,
		"accepted":  true,
		"reason":    "generated",
		"value":     value,
	}
}

func (f *fakePhone) SendControlCodeFailure(t *testing.T, requestID string, reason string) {
	t.Helper()
	f.mu.Lock()
	results := f.currentResults
	f.mu.Unlock()
	if results == nil {
		t.Fatalf("no active phone session for result")
	}
	results <- map[string]any{
		"type":      "control_code_result",
		"requestId": requestID,
		"ok":        false,
		"accepted":  false,
		"reason":    reason,
	}
}

var (
	rigasSatiksmeTopTrimPixel    = color.NRGBA{R: 0xff, G: 0x00, B: 0x00, A: 0xff}
	rigasSatiksmeBodyPixel       = color.NRGBA{R: 0x00, G: 0x00, B: 0xff, A: 0xff}
	rigasSatiksmeBottomTrimPixel = color.NRGBA{R: 0x00, G: 0xff, B: 0x00, A: 0xff}
)

func rigasSatiksmeScreenshotFixturePNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	topTrim := rigasSatiksmeGeneratedScreenshotCropPixels(height)
	bottomTrim := topTrim
	for y := 0; y < height; y++ {
		fill := rigasSatiksmeBodyPixel
		switch {
		case y < topTrim:
			fill = rigasSatiksmeTopTrimPixel
		case y >= height-bottomTrim:
			fill = rigasSatiksmeBottomTrimPixel
		}
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, fill)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encode screenshot fixture: %v", err)
	}
	return out.Bytes()
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func assertJobNotSucceededBriefly(t *testing.T, b *Broker, jobID string) {
	t.Helper()
	deadline := time.After(120 * time.Millisecond)
	for {
		got, ok := b.Job(jobID)
		if !ok {
			t.Fatalf("job disappeared")
		}
		if got.Status == JobSucceeded {
			t.Fatalf("job succeeded without app-generated image: %#v", got)
		}
		select {
		case <-deadline:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForJobStatus(t *testing.T, b *Broker, jobID string, status string) QRJob {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		got, ok := b.Job(jobID)
		if !ok {
			t.Fatalf("job disappeared")
		}
		if got.Status == status {
			return got
		}
		select {
		case <-deadline:
			t.Fatalf("job did not reach %s: %#v", status, got)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
