package chatanalyzer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGoogleModelDiscoveryFollowsPagination(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Query().Get("pageToken") {
		case "":
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-3-flash","supportedGenerationMethods":["generateContent"]}],"nextPageToken":"page-two"}`)
		case "page-two":
			fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]}]}`)
		default:
			t.Fatalf("unexpected page token %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer server.Close()

	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		Model:             "auto",
		GoogleAutoModel:   true,
		GoogleModelsURL:   server.URL,
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	got, err := analyzer.modelForRequest(context.Background())
	if err != nil {
		t.Fatalf("modelForRequest() error = %v", err)
	}
	if got != "gemma-4-31b-it" || requests != 2 {
		t.Fatalf("model/requests = %q/%d, want paginated Gemma/2", got, requests)
	}
}

func TestGoogleModelCacheRefreshesAndUsesBoundedStaleSelection(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	fail := false
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]}]}`)
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:            modelProviderGoogle,
		Model:               "auto",
		GoogleAutoModel:     true,
		GoogleModelsURL:     server.URL,
		GoogleModelPolicy:   "gemma_parameter",
		GoogleModelCacheTTL: time.Hour,
		GoogleModelStaleTTL: 4 * time.Hour,
		MaxAttempts:         1,
		Now:                 func() time.Time { return now },
	})
	if _, err := analyzer.modelForRequest(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Minute)
	if _, err := analyzer.modelForRequest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests within cache TTL = %d, want 1", requests)
	}
	fail = true
	now = now.Add(90 * time.Minute)
	if got, err := analyzer.modelForRequest(context.Background()); err != nil || got != "gemma-4-31b-it" {
		t.Fatalf("bounded stale model = %q, error=%v", got, err)
	}
	now = now.Add(3 * time.Hour)
	if _, err := analyzer.modelForRequest(context.Background()); err == nil {
		t.Fatal("modelForRequest() error = nil after stale TTL")
	}
}

func TestGoogleModelRejectedByGenerationEndpointFallsBackToNextCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-it","supportedGenerationMethods":["generateContent"]}]}`)
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		Model:             "auto",
		GoogleAutoModel:   true,
		GoogleModelsURL:   server.URL,
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	first, err := analyzer.modelForRequest(context.Background())
	if err != nil || first != "gemma-4-31b-it" {
		t.Fatalf("initial model = %q, error=%v", first, err)
	}
	analyzer.invalidateGoogleModelOnPermanentError(first, &modelHTTPError{StatusCode: http.StatusBadRequest})
	second, err := analyzer.modelForRequest(context.Background())
	if err != nil || second != "gemma-4-26b-it" {
		t.Fatalf("fallback model = %q, error=%v", second, err)
	}
}

func TestModelRequestRetriesRetryAfterAndTransientStatuses(t *testing.T) {
	attempts := 0
	var sleeps []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		switch attempts {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"{\"reports\":[],\"votes\":[],\"ignored\":[{\"messageId\":105,\"reason\":\"not actionable\"}]}"}}]}`)
		}
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:       modelProviderOpenAI,
		BaseURL:        server.URL,
		Model:          "test-model",
		MaxAttempts:    3,
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  10 * time.Second,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	})
	_, _, _, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if err != nil {
		t.Fatalf("AnalyzeBatch() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if len(sleeps) != 2 || sleeps[0] != 2*time.Second || sleeps[1] != 2*time.Second {
		t.Fatalf("retry sleeps = %v, want [2s 2s]", sleeps)
	}
}

func TestRetryDelayHonorsProviderValueAboveLocalBackoffCap(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		RetryMaxDelay: 10 * time.Second,
		Now:           func() time.Time { return now },
	})
	if got := analyzer.retryDelay(1, "120"); got != 2*time.Minute {
		t.Fatalf("numeric Retry-After delay = %v, want 2m", got)
	}
	httpDate := now.Add(75 * time.Second).Format(http.TimeFormat)
	if got := analyzer.retryDelay(1, httpDate); got != 75*time.Second {
		t.Fatalf("date Retry-After delay = %v, want 75s", got)
	}
}

func TestModelRequestRetriesTransientTransportTimeout(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, context.DeadlineExceeded
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		HTTPClient:  client,
		MaxAttempts: 2,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	})
	body, _, err := analyzer.doRequest(context.Background(), func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "https://model.invalid/test", nil)
	}, 1024)
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if attempts != 2 || string(body) != `{"ok":true}` {
		t.Fatalf("attempts/body = %d/%s", attempts, body)
	}
}

func TestModelCallDelayAlsoPacesRetryAttempts(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	attempts := 0
	var sleeps []time.Duration
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		status := http.StatusOK
		if attempts == 1 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		HTTPClient:     client,
		CallDelay:      5 * time.Second,
		MaxAttempts:    2,
		RetryBaseDelay: time.Second,
		Now:            func() time.Time { return now },
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			now = now.Add(delay)
			return nil
		},
	})
	if _, _, err := analyzer.doRequest(context.Background(), func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "https://model.invalid/test", nil)
	}, 1024); err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if attempts != 2 || !now.Equal(time.Date(2026, 7, 22, 0, 0, 5, 0, time.UTC)) {
		t.Fatalf("attempts/time = %d/%s, want two attempts separated by five seconds", attempts, now)
	}
	if len(sleeps) != 2 || sleeps[0] != time.Second || sleeps[1] != 4*time.Second {
		t.Fatalf("retry/pacing sleeps = %v, want [1s 4s]", sleeps)
	}
}

func TestModelCallDelayAppliesBetweenBatchCalls(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"{\"reports\":[],\"votes\":[],\"ignored\":[{\"messageId\":105,\"reason\":\"not actionable\"}]}"}}]}`)
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:    modelProviderOpenAI,
		BaseURL:     server.URL,
		Model:       "test-model",
		CallDelay:   5 * time.Second,
		MaxAttempts: 1,
		Now:         func() time.Time { return now },
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			now = now.Add(delay)
			return nil
		},
	})
	items := []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}
	if _, _, _, err := analyzer.AnalyzeBatch(context.Background(), items, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := analyzer.AnalyzeBatch(context.Background(), items, nil); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] != 5*time.Second {
		t.Fatalf("call pacing sleeps = %v, want [5s]", sleeps)
	}
}

func TestBatchHealthUsesRequestedModelWhenProviderOmitsModelName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"reports\":[],\"votes\":[],\"ignored\":[{\"messageId\":105,\"reason\":\"not actionable\"}]}"}}]}`)
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:    modelProviderOpenAI,
		BaseURL:     server.URL,
		Model:       "configured-model",
		MaxAttempts: 1,
	})
	decision, _, selected, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "configured-model" || decision.ModelMeta.SelectedModel != "configured-model" {
		t.Fatalf("selected model = %q/%q, want configured-model", selected, decision.ModelMeta.SelectedModel)
	}
}

func TestVerifiedFreePolicyRequiresExplicitMetadata(t *testing.T) {
	verified := true
	models := []GoogleAIModel{
		{Name: "models/gemma-4-31b-it", SupportedGenerationMethods: []string{"generateContent"}},
		{Name: "models/gemma-4-4b-it", FreeTierEligible: &verified, SupportedGenerationMethods: []string{"generateContent"}},
	}
	selected, ok := SelectGoogleAIModel(models, "verified_free_parameter")
	if !ok || selected.Name != "models/gemma-4-4b-it" {
		t.Fatalf("verified-free selection = %+v/%v", selected, ok)
	}
}
