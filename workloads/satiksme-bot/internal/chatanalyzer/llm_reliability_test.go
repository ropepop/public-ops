package chatanalyzer

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGoogleAutoModelRateLimitFallsBackOnceAndCachesAlternative(t *testing.T) {
	now := time.Date(2026, 7, 22, 2, 30, 0, 0, time.UTC)
	var generationModels []string
	modelListRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			modelListRequests++
			fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`)
		case "/v1/chat/completions":
			var request openAIChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, request.Model)
			if request.Model == "gemma-4-31b-it" {
				w.Header().Set("Retry-After", "1200")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": request.Model,
				"choices": []any{map[string]any{"message": map[string]any{
					"role":    "assistant",
					"content": `{"reports":[],"votes":[],"ignored":[{"messageId":105,"reason":"not actionable"}]}`,
				}}},
			})
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:           modelProviderGoogle,
		BaseURL:            server.URL + "/v1",
		Model:              "auto",
		GoogleAutoModel:    true,
		GoogleModelsURL:    server.URL + "/models",
		GoogleModelPolicy:  "gemma_parameter",
		GoogleRateLimitTTL: 10 * time.Minute,
		MaxAttempts:        1,
		Now:                func() time.Time { return now },
	})
	items := []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}
	decision, _, selected, err := analyzer.AnalyzeBatch(context.Background(), items, nil)
	if err != nil {
		t.Fatalf("AnalyzeBatch() error = %v", err)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("generation models = %q, want %q", got, want)
	}
	if selected != "gemma-4-26b-a4b-it" || decision.ModelMeta.SelectedModel != selected {
		t.Fatalf("selected model = %q meta=%+v", selected, decision.ModelMeta)
	}
	if modelListRequests != 2 {
		t.Fatalf("model list requests = %d, want initial selection plus fallback selection", modelListRequests)
	}
	if got, want := analyzer.rejectedModels["gemma-4-31b-it"], now.Add(20*time.Minute); !got.Equal(want) {
		t.Fatalf("31B cooldown = %s, want provider Retry-After through %s", got, want)
	}

	if _, _, selected, err = analyzer.AnalyzeBatch(context.Background(), items, nil); err != nil {
		t.Fatalf("second AnalyzeBatch() error = %v", err)
	}
	if selected != "gemma-4-26b-a4b-it" {
		t.Fatalf("second selected model = %q, want cached fallback", selected)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("generation models after cached call = %q, want %q", got, want)
	}
	if modelListRequests != 2 {
		t.Fatalf("model list requests after cached call = %d, want 2", modelListRequests)
	}

	now = now.Add(20 * time.Minute)
	if _, _, selected, err = analyzer.AnalyzeBatch(context.Background(), items, nil); err != nil {
		t.Fatalf("post-cooldown AnalyzeBatch() error = %v", err)
	}
	if selected != "gemma-4-26b-a4b-it" {
		t.Fatalf("post-cooldown selected model = %q, want successful fallback", selected)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it,gemma-4-26b-a4b-it,gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("generation models after cooldown = %q, want %q", got, want)
	}
	if modelListRequests != 4 {
		t.Fatalf("model list requests after cooldown = %d, want preferred model reconsidered plus fallback", modelListRequests)
	}
}

func TestGoogleAutoModelLongRetryAfterFallsBackBeforeContextDeadline(t *testing.T) {
	var generationModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`)
		case "/v1/chat/completions":
			var request openAIChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, request.Model)
			if request.Model == "gemma-4-31b-it" {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": request.Model,
				"choices": []any{map[string]any{"message": map[string]any{
					"role":    "assistant",
					"content": `{"reports":[],"votes":[],"ignored":[{"messageId":105,"reason":"not actionable"}]}`,
				}}},
			})
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		BaseURL:           server.URL + "/v1",
		Model:             "auto",
		GoogleAutoModel:   true,
		GoogleModelsURL:   server.URL + "/models",
		GoogleModelPolicy: "gemma_parameter",
	})
	if analyzer.maxAttempts != defaultModelMaxAttempts {
		t.Fatalf("max attempts = %d, want production default %d", analyzer.maxAttempts, defaultModelMaxAttempts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, selected, err := analyzer.AnalyzeBatch(ctx, []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if err != nil {
		t.Fatalf("AnalyzeBatch() error = %v, want fallback before long Retry-After exhausts context", err)
	}
	if selected != "gemma-4-26b-a4b-it" {
		t.Fatalf("selected model = %q, want 26B fallback", selected)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("generation models = %q, want %q", got, want)
	}
}

func TestGoogleAutoModelTimeoutFallsBackWithFreshRequestDeadline(t *testing.T) {
	now := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	var generationModels []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/models":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`,
				)),
			}, nil
		case "/v1/chat/completions":
			var payload openAIChatCompletionRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, payload.Model)
			if payload.Model == "gemma-4-31b-it" {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}
			if err := request.Context().Err(); err != nil {
				t.Fatalf("fallback request context is already done: %v", err)
			}
			if deadline, ok := request.Context().Deadline(); !ok || !deadline.After(time.Now()) {
				t.Fatalf("fallback request deadline = %v ok=%t, want a live deadline", deadline, ok)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"model":"gemma-4-26b-a4b-it","choices":[{"message":{"role":"assistant","content":"{\"reports\":[],\"votes\":[],\"ignored\":[{\"messageId\":105,\"reason\":\"not actionable\"}]}"}}]}`,
				)),
			}, nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, fmt.Errorf("unexpected request")
		}
	})}
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:           modelProviderGoogle,
		BaseURL:            "https://model.invalid/v1",
		Model:              "auto",
		HTTPClient:         client,
		Timeout:            20 * time.Millisecond,
		GoogleAutoModel:    true,
		GoogleModelsURL:    "https://model.invalid/models",
		GoogleModelPolicy:  "gemma_parameter",
		GoogleRateLimitTTL: 10 * time.Minute,
		MaxAttempts:        1,
		Now:                func() time.Time { return now },
	})

	decision, _, selected, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if err != nil {
		t.Fatalf("AnalyzeBatch() error = %v, want timeout fallback", err)
	}
	if selected != "gemma-4-26b-a4b-it" || decision.ModelMeta.SelectedModel != selected {
		t.Fatalf("selected model = %q meta=%+v, want 26B fallback", selected, decision.ModelMeta)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("generation models = %q, want %q", got, want)
	}
	if got, want := analyzer.rejectedModels["gemma-4-31b-it"], now.Add(10*time.Minute); !got.Equal(want) {
		t.Fatalf("timed-out model cooldown = %s, want %s", got, want)
	}
}

func TestGoogleAutoModelDoesNotFallbackAfterCallerDeadline(t *testing.T) {
	var generationModels []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/models":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`,
				)),
			}, nil
		case "/v1/chat/completions":
			var payload openAIChatCompletionRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, payload.Model)
			<-request.Context().Done()
			return nil, request.Context().Err()
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, fmt.Errorf("unexpected request")
		}
	})}
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		BaseURL:           "https://model.invalid/v1",
		Model:             "auto",
		HTTPClient:        client,
		Timeout:           time.Second,
		GoogleAutoModel:   true,
		GoogleModelsURL:   "https://model.invalid/models",
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, _, err := analyzer.AnalyzeBatch(ctx, []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AnalyzeBatch() error = %v, want caller deadline", err)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it"; got != want {
		t.Fatalf("generation models = %q, want no work after caller deadline", got)
	}
}

func TestGoogleAutoModelTimeoutFallbackIsBoundedAcrossBothModels(t *testing.T) {
	var generationModels []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/models":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`,
				)),
			}, nil
		case "/v1/chat/completions":
			var payload openAIChatCompletionRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, payload.Model)
			<-request.Context().Done()
			return nil, request.Context().Err()
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, fmt.Errorf("unexpected request")
		}
	})}
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		BaseURL:           "https://model.invalid/v1",
		Model:             "auto",
		HTTPClient:        client,
		Timeout:           15 * time.Millisecond,
		GoogleAutoModel:   true,
		GoogleModelsURL:   "https://model.invalid/models",
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	started := time.Now()
	_, _, selected, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AnalyzeBatch() error = %v, want final model deadline", err)
	}
	if selected != "gemma-4-26b-a4b-it" {
		t.Fatalf("selected model = %q, want final attempted model", selected)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("bounded generation models = %q, want %q", got, want)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded timeout fallback took %s", elapsed)
	}
}

func TestGoogleAutoModelRateLimitFallbackIsBounded(t *testing.T) {
	var generationModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`)
		case "/v1/chat/completions":
			var request openAIChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, request.Model)
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		BaseURL:           server.URL + "/v1",
		Model:             "auto",
		GoogleAutoModel:   true,
		GoogleModelsURL:   server.URL + "/models",
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	_, _, _, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	var statusErr *modelHTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("AnalyzeBatch() error = %v, want final 429", err)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("bounded generation models = %q, want %q", got, want)
	}
}

func TestGoogleAutoModelInvalidAcceptedResponseFallsBackOnce(t *testing.T) {
	var generationModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`)
		case "/v1/chat/completions":
			var request openAIChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, request.Model)
			content := `{"reports":[`
			if request.Model == "gemma-4-26b-a4b-it" {
				content = `{"reports":[],"votes":[],"ignored":[{"messageId":105,"reason":"not actionable"}]}`
			}
			fmt.Fprintf(w, `{"model":%q,"choices":[{"message":{"role":"assistant","content":%q}}]}`, request.Model, content)
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		BaseURL:           server.URL + "/v1",
		Model:             "auto",
		GoogleAutoModel:   true,
		GoogleModelsURL:   server.URL + "/models",
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	decision, _, selected, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if err != nil {
		t.Fatalf("AnalyzeBatch() error = %v, want structured-output fallback", err)
	}
	if selected != "gemma-4-26b-a4b-it" || decision.ModelMeta.SelectedModel != selected {
		t.Fatalf("selected model = %q meta=%+v, want 26B fallback", selected, decision.ModelMeta)
	}
	if _, _, selected, err = analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil); err != nil {
		t.Fatalf("cached fallback AnalyzeBatch() error = %v", err)
	}
	if selected != "gemma-4-26b-a4b-it" {
		t.Fatalf("cached selected model = %q, want 26B", selected)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("generation models = %q, want %q", got, want)
	}
}

func TestAnalyzerErrorCodeClassifiesInvalidModelOutput(t *testing.T) {
	validationErr := &modelOutputError{cause: errors.New("invalid batch decision JSON: unexpected end of JSON input")}
	if got, want := analyzerErrorCode(validationErr), "model_output_invalid"; got != want {
		t.Fatalf("analyzerErrorCode() = %q, want %q", got, want)
	}
	if got, want := analyzerErrorCodeText(validationErr.Error()), "model_output_invalid"; got != want {
		t.Fatalf("analyzerErrorCodeText() = %q, want %q", got, want)
	}
}

func TestGoogleAutoModelInvalidAcceptedResponseFallbackIsBounded(t *testing.T) {
	var generationModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"models":[{"name":"models/gemma-4-31b-it","supportedGenerationMethods":["generateContent"]},{"name":"models/gemma-4-26b-a4b-it","supportedGenerationMethods":["generateContent"]}]}`)
		case "/v1/chat/completions":
			var request openAIChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			generationModels = append(generationModels, request.Model)
			fmt.Fprintf(w, `{"model":%q,"choices":[{"message":{"role":"assistant","content":"{\\\"reports\\\":["},"finish_reason":"length"}]}`, request.Model)
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	analyzer := NewOpenAIAnalyzer(OpenAIAnalyzerOptions{
		Provider:          modelProviderGoogle,
		BaseURL:           server.URL + "/v1",
		Model:             "auto",
		GoogleAutoModel:   true,
		GoogleModelsURL:   server.URL + "/models",
		GoogleModelPolicy: "gemma_parameter",
		MaxAttempts:       1,
	})
	_, _, _, err := analyzer.AnalyzeBatch(context.Background(), []BatchItem{{Message: testMessage("chat:1", 105, 4, "test")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid batch decision JSON") || !strings.Contains(err.Error(), "model output was truncated") {
		t.Fatalf("AnalyzeBatch() error = %v, want final structured-output error", err)
	}
	if got, want := strings.Join(generationModels, ","), "gemma-4-31b-it,gemma-4-26b-a4b-it"; got != want {
		t.Fatalf("bounded generation models = %q, want %q", got, want)
	}
}
