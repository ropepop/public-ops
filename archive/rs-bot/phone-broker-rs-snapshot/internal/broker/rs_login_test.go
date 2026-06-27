package broker

import (
	"context"
	"testing"
	"time"
)

func TestRSLoginPhoneLast4Redaction(t *testing.T) {
	cases := map[string]string{
		"+371 2 000 0000":   "0000",
		"20000000":          "0000",
		"123":               "123",
		"abcd1234efgh":      "1234",
		"+1 (415) 555-9999": "9999",
	}
	for input, want := range cases {
		if got := normalizeRSLoginPhoneLast4(input); got != want {
			t.Fatalf("normalizeRSLoginPhoneLast4(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRSLoginPhoneValidation(t *testing.T) {
	good := []string{"+371 20000000", "20000000", "+1 (415) 555-9999", "123456", "1234567890123456"}
	bad := []string{"", "12345", "12345678901234567", "abcdefg", "+-()"}
	for _, input := range good {
		if !isValidRSLoginPhone(input) {
			t.Fatalf("isValidRSLoginPhone(%q) = false, want true", input)
		}
	}
	for _, input := range bad {
		if isValidRSLoginPhone(input) {
			t.Fatalf("isValidRSLoginPhone(%q) = true, want false", input)
		}
	}
}

func TestRSLoginCodeValidation(t *testing.T) {
	good := []string{"1234", "123456", "12345678", "Rajpud-qigjon-sehxo9", "27079944", "a1b2c3d4"}
	bad := []string{"", "ab", "abc"}
	for _, input := range good {
		if !isValidRSLoginCode(input) {
			t.Fatalf("isValidRSLoginCode(%q) = false, want true", input)
		}
	}
	for _, input := range bad {
		if isValidRSLoginCode(input) {
			t.Fatalf("isValidRSLoginCode(%q) = true, want false", input)
		}
	}
}

func TestRSLoginStartRequiresValidPhone(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartRSLogin(context.Background(), "abc", time.Now()); err == nil {
		t.Fatalf("StartRSLogin with bad phone should fail")
	}
}

func TestRSLoginStartSnapshotExposesLast4Only(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	snapshot, err := b.StartRSLogin(ctx, "+371 20000000", time.Now())
	if err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	if snapshot.PhoneLast4 != "0000" {
		t.Fatalf("phoneLast4 = %q, want 0000", snapshot.PhoneLast4)
	}
	full := b.RSLoginSnapshot()
	if full.PhoneLast4 != "0000" {
		t.Fatalf("snapshot phoneLast4 = %q, want 0000", full.PhoneLast4)
	}
}

func TestRSLoginSnapshotIdlesByDefault(t *testing.T) {
	b, err := New(Config{UpstreamBaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := b.RSLoginSnapshot()
	if snapshot.State != rsLoginStateIdle {
		t.Fatalf("idle snapshot state = %q, want idle", snapshot.State)
	}
	if snapshot.PhoneLast4 != "" {
		t.Fatalf("idle snapshot phoneLast4 = %q, want empty", snapshot.PhoneLast4)
	}
}

func TestRSLoginSMSRequiresActiveLogin(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SubmitRSLoginSMS(context.Background(), "12345", time.Now()); err == nil {
		t.Fatalf("SubmitRSLoginSMS without active login should fail")
	}
}

func TestRSLoginSMSCodeMustBeNumeric(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SubmitRSLoginSMS(context.Background(), "abcd", time.Now()); err == nil {
		t.Fatalf("SubmitRSLoginSMS with non-numeric code should fail")
	}
}

func TestRSLoginSingleAttemptPolicy(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.StartRSLogin(ctx, "+371 20000000", time.Now()); err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	b.mu.Lock()
	b.rsLogin.State = rsLoginStateWaitingForSMS
	b.mu.Unlock()

	if _, err := b.SubmitRSLoginSMS(ctx, "12345", time.Now()); err != nil {
		t.Fatalf("first SubmitRSLoginSMS should succeed: %v", err)
	}
	if _, err := b.SubmitRSLoginSMS(ctx, "12345", time.Now()); err == nil {
		t.Fatalf("second SubmitRSLoginSMS should fail (single attempt policy)")
	}
}

func TestRSLoginCancelClearsState(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.StartRSLogin(ctx, "+371 20000000", time.Now()); err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	b.mu.Lock()
	b.rsLogin.State = rsLoginStateWaitingForSMS
	b.mu.Unlock()

	if _, err := b.CancelRSLogin(ctx, time.Now()); err != nil {
		t.Fatalf("CancelRSLogin failed: %v", err)
	}
	snapshot := b.RSLoginSnapshot()
	if snapshot.State != rsLoginStateIdle {
		t.Fatalf("after cancel snapshot state = %q, want idle", snapshot.State)
	}
}

func TestRSLoginAnalyticsRollupsIncrementOnOutcome(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.StartRSLogin(ctx, "+371 20000000", time.Now()); err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	if _, err := b.CancelRSLogin(ctx, time.Now()); err != nil {
		t.Fatalf("CancelRSLogin failed: %v", err)
	}
	analytics := b.Analytics(time.Now())
	if analytics.RSLogin.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", analytics.RSLogin.Attempts)
	}
	if analytics.RSLogin.Failures != 1 {
		t.Fatalf("failures = %d, want 1", analytics.RSLogin.Failures)
	}
	if analytics.RSLogin.FailureByReason[rsLoginFailureCanceled] != 1 {
		t.Fatalf("FailureByReason[canceled] = %d, want 1", analytics.RSLogin.FailureByReason[rsLoginFailureCanceled])
	}
	if analytics.RSLogin.LastPhoneLast4 != "0000" {
		t.Fatalf("LastPhoneLast4 = %q, want 0000", analytics.RSLogin.LastPhoneLast4)
	}
}

func TestRSLoginSnapshotNeverExposesFullPhone(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const fullPhone = "+371 20000000"
	if _, err := b.StartRSLogin(ctx, fullPhone, time.Now()); err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	snapshot := b.Snapshot(time.Now())
	if snapshot.RSLogin.PhoneLast4 == fullPhone {
		t.Fatalf("public snapshot leaked full phone number: %q", snapshot.RSLogin.PhoneLast4)
	}
	if snapshot.RSLogin.PhoneLast4 != "0000" {
		t.Fatalf("phoneLast4 = %q, want 0000", snapshot.RSLogin.PhoneLast4)
	}
	state := b.Snapshot(time.Now())
	if state.RSLogin.PhoneLast4 != "0000" {
		t.Fatalf("state.rsLogin.phoneLast4 = %q, want 0000", state.RSLogin.PhoneLast4)
	}
}

func TestRSLoginStartPreemptsRunningQRJob(t *testing.T) {
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

	if _, err := b.EnqueueQRJob(ctx, QRJobInput{ChatID: "1001", UserID: "42", Code: "12345", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	upstream.WaitForCommand(t, "generate_rigassatiksme_qr_batch")
	if _, err := b.StartRSLogin(ctx, "+371 20000000", time.Now()); err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	cancelCommand := upstream.WaitForCommand(t, "cancel_rigassatiksme_qr_batch")
	if cancelCommand["reason"] != "login_preempted" {
		t.Fatalf("preempt reason = %q, want login_preempted", cancelCommand["reason"])
	}
}

func TestTicketPresencePreemptsRSLogin(t *testing.T) {
	upstream := newFakePhone(t)
	defer upstream.Close()

	b, err := New(Config{
		UpstreamBaseURL:  upstream.URL,
		PhoneSendTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.StartRSLogin(ctx, "+371 20000000", time.Now()); err != nil {
		t.Fatalf("StartRSLogin failed: %v", err)
	}
	b.mu.Lock()
	b.rsLogin.State = rsLoginStateWaitingForSMS
	b.mu.Unlock()

	if err := b.UpdateTicketPresence(ctx, TicketPresenceInput{Viewers: 1, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	snapshot := b.RSLoginSnapshot()
	if snapshot.State != rsLoginStateIdle {
		t.Fatalf("snapshot state after ticket preemption = %q, want idle", snapshot.State)
	}
	analytics := b.Analytics(time.Now())
	if analytics.RSLogin.FailureByReason[rsLoginFailureTicketPreempted] != 1 {
		t.Fatalf("expected failure rollup for ticket_preempted, got %#v", analytics.RSLogin.FailureByReason)
	}
	if analytics.RSLogin.LastState != rsLoginStateFailed {
		t.Fatalf("last state = %q, want failed", analytics.RSLogin.LastState)
	}
	if analytics.RSLogin.LastFailureReason != rsLoginFailureTicketPreempted {
		t.Fatalf("last failure reason = %q, want ticket_preempted", analytics.RSLogin.LastFailureReason)
	}
}

func TestRSLoginFailureByReasonRollup(t *testing.T) {
	b, err := New(Config{UpstreamBaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, phone := range []string{"+371 20000000", "+371 20000001"} {
		if _, err := b.StartRSLogin(ctx, phone, time.Now()); err != nil {
			t.Fatalf("StartRSLogin failed: %v", err)
		}
		if _, err := b.CancelRSLogin(ctx, time.Now()); err != nil {
			t.Fatalf("CancelRSLogin failed: %v", err)
		}
	}
	analytics := b.Analytics(time.Now())
	if analytics.RSLogin.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", analytics.RSLogin.Attempts)
	}
	if analytics.RSLogin.Failures != 2 {
		t.Fatalf("failures = %d, want 2", analytics.RSLogin.Failures)
	}
	if analytics.RSLogin.FailureByReason[rsLoginFailureCanceled] != 2 {
		t.Fatalf("FailureByReason[canceled] = %d, want 2", analytics.RSLogin.FailureByReason[rsLoginFailureCanceled])
	}
}
