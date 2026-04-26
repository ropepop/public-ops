package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/store"
)

type recordedRequest struct {
	path    string
	payload map[string]any
}

type telegramRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func newTelegramRecorder(t *testing.T) (*telegramRecorder, *Client, func()) {
	t.Helper()

	recorder := &telegramRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{}
		if r.Body != nil {
			defer r.Body.Close()
			_ = json.NewDecoder(r.Body).Decode(&payload)
		}

		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, recordedRequest{
			path:    r.URL.Path,
			payload: payload,
		})
		recorder.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))

	client := NewClient("test-token", 2*time.Second)
	client.baseURL = server.URL
	client.redactedBaseURL = server.URL
	client.httpClient = server.Client()

	return recorder, client, server.Close
}

func (r *telegramRecorder) lastRequest(t *testing.T, path string) recordedRequest {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.requests) - 1; i >= 0; i-- {
		if r.requests[i].path == path {
			return r.requests[i]
		}
	}
	t.Fatalf("no request recorded for %s", path)
	return recordedRequest{}
}

func extractInlineButtons(t *testing.T, payload map[string]any) [][]map[string]any {
	t.Helper()

	replyMarkup, ok := payload["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("reply_markup missing or wrong type: %T", payload["reply_markup"])
	}
	rawRows, ok := replyMarkup["inline_keyboard"].([]any)
	if !ok {
		t.Fatalf("inline_keyboard missing or wrong type: %T", replyMarkup["inline_keyboard"])
	}

	rows := make([][]map[string]any, 0, len(rawRows))
	for _, rawRow := range rawRows {
		rowItems, ok := rawRow.([]any)
		if !ok {
			t.Fatalf("row has wrong type: %T", rawRow)
		}
		row := make([]map[string]any, 0, len(rowItems))
		for _, rawButton := range rowItems {
			button, ok := rawButton.(map[string]any)
			if !ok {
				t.Fatalf("button has wrong type: %T", rawButton)
			}
			row = append(row, button)
		}
		rows = append(rows, row)
	}
	return rows
}

func flattenButtonTexts(rows [][]map[string]any) []string {
	out := make([]string, 0)
	for _, row := range rows {
		for _, button := range row {
			if text, ok := button["text"].(string); ok {
				out = append(out, text)
			}
		}
	}
	return out
}

type checkinHarness struct {
	service   *Service
	store     *store.SQLiteStore
	recorder  *telegramRecorder
	closeFunc func()
}

func newCheckinHarness(t *testing.T) *checkinHarness {
	t.Helper()

	recorder, client, closeFunc := newTelegramRecorder(t)
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	now := time.Now().In(loc)
	serviceDate := now.Format("2006-01-02")
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, serviceDate+".json")

	rigaDep := now.Add(10 * time.Minute)
	rigaArr := now.Add(50 * time.Minute)
	rigaEastDep := now.Add(20 * time.Minute)
	rigaEastArr := now.Add(70 * time.Minute)
	rindzeleDep := now.Add(30 * time.Minute)
	rindzeleArr := now.Add(80 * time.Minute)

	payload := fmt.Sprintf(`{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"train-riga-jelgava",
      "service_date":"%s",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"%s",
      "arrival_at":"%s",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"%s"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"%s"}
      ]
    },
    {
      "id":"train-riga-east-cesis",
      "service_date":"%s",
      "from_station":"Riga East",
      "to_station":"Cesis",
      "departure_at":"%s",
      "arrival_at":"%s",
      "stops":[
        {"station_name":"Riga East","seq":1,"departure_at":"%s"},
        {"station_name":"Cesis","seq":2,"arrival_at":"%s"}
      ]
    },
    {
      "id":"train-rindzele-valmiera",
      "service_date":"%s",
      "from_station":"Rindzele",
      "to_station":"Valmiera",
      "departure_at":"%s",
      "arrival_at":"%s",
      "stops":[
        {"station_name":"Rindzele","seq":1,"departure_at":"%s"},
        {"station_name":"Valmiera","seq":2,"arrival_at":"%s"}
      ]
    }
  ]
}`,
		serviceDate,
		rigaDep.Format(time.RFC3339), rigaArr.Format(time.RFC3339), rigaDep.Format(time.RFC3339), rigaArr.Format(time.RFC3339),
		serviceDate,
		rigaEastDep.Format(time.RFC3339), rigaEastArr.Format(time.RFC3339), rigaEastDep.Format(time.RFC3339), rigaEastArr.Format(time.RFC3339),
		serviceDate,
		rindzeleDep.Format(time.RFC3339), rindzeleArr.Format(time.RFC3339), rindzeleDep.Format(time.RFC3339), rindzeleArr.Format(time.RFC3339),
	)
	if err := os.WriteFile(snapshotPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, dir, loc, 3)
	if err := manager.LoadToday(context.Background(), now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		client,
		nil,
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		i18n.NewCatalog(),
		loc,
		1,
		true,
		"",
	)

	return &checkinHarness{
		service:  service,
		store:    st,
		recorder: recorder,
		closeFunc: func() {
			closeFunc()
			_ = st.Close()
		},
	}
}

func (h *checkinHarness) ensureEnglish(t *testing.T, userID int64) {
	t.Helper()
	if _, err := h.store.EnsureUserSettings(context.Background(), userID); err != nil {
		t.Fatalf("ensure settings: %v", err)
	}
	if err := h.store.SetLanguage(context.Background(), userID, domain.LanguageEN); err != nil {
		t.Fatalf("set language: %v", err)
	}
}

func (h *checkinHarness) ensureLatvian(t *testing.T, userID int64) {
	t.Helper()
	if _, err := h.store.EnsureUserSettings(context.Background(), userID); err != nil {
		t.Fatalf("ensure settings: %v", err)
	}
	if err := h.store.SetLanguage(context.Background(), userID, domain.LanguageLV); err != nil {
		t.Fatalf("set language: %v", err)
	}
}

func (h *checkinHarness) close() {
	h.closeFunc()
}

func TestActiveStationInputRoutesPlainTextToSearch(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)
	h.service.setCheckinTextFlow(userID, checkinFlowStationText, time.Now())

	err := h.service.handleMessage(context.Background(), &Message{
		From: &User{ID: userID},
		Chat: Chat{ID: userID},
		Text: "ri",
	})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	text, _ := req.payload["text"].(string)
	if !strings.Contains(text, `Stations matching "ri". Choose one:`) {
		t.Fatalf("expected station match prompt, got %q", text)
	}
}

func TestStartCommandBypassesActiveCheckInSession(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)
	h.service.setCheckinTextFlow(userID, checkinFlowStationText, time.Now())

	err := h.service.handleMessage(context.Background(), &Message{
		From: &User{ID: userID},
		Chat: Chat{ID: userID},
		Text: "/start",
	})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	sendMessages := make([]recordedRequest, 0)
	for _, request := range h.recorder.requests {
		if request.path == "/sendMessage" {
			sendMessages = append(sendMessages, request)
		}
	}
	if len(sendMessages) != 2 {
		t.Fatalf("expected 2 sendMessage requests, got %d", len(sendMessages))
	}
	startText, _ := sendMessages[0].payload["text"].(string)
	if !strings.Contains(startText, "This bot shares real-time alerts") {
		t.Fatalf("expected start message, got %q", startText)
	}
	openAppText, _ := sendMessages[1].payload["text"].(string)
	if !strings.Contains(openAppText, "Open the train app") {
		t.Fatalf("expected open app prompt after start, got %q", openAppText)
	}
}

func TestSingleStationMatchAutoAdvancesToDepartures(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)
	h.service.setCheckinTextFlow(userID, checkinFlowStationText, time.Now())

	err := h.service.handleMessage(context.Background(), &Message{
		From: &User{ID: userID},
		Chat: Chat{ID: userID},
		Text: "riga",
	})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	text, _ := req.payload["text"].(string)
	if !strings.Contains(text, "Departures via Riga") {
		t.Fatalf("expected departures list for Riga, got %q", text)
	}
}

func TestMultipleStationMatchesRenderRankedButtons(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)
	h.service.setCheckinTextFlow(userID, checkinFlowStationText, time.Now())

	err := h.service.handleMessage(context.Background(), &Message{
		From: &User{ID: userID},
		Chat: Chat{ID: userID},
		Text: "ri",
	})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	rows := extractInlineButtons(t, req.payload)
	if got := rows[0][0]["text"]; got != "Riga" {
		t.Fatalf("expected first result Riga, got %v", got)
	}
	if got := rows[1][0]["text"]; got != "Riga East" {
		t.Fatalf("expected second result Riga East, got %v", got)
	}
	if got := rows[2][0]["text"]; got != "Rindzele" {
		t.Fatalf("expected third result Rindzele, got %v", got)
	}
}

func TestNoStationMatchKeepsSessionAndOffersFallbacks(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)
	h.service.setCheckinTextFlow(userID, checkinFlowStationText, time.Now())

	err := h.service.handleMessage(context.Background(), &Message{
		From: &User{ID: userID},
		Chat: Chat{ID: userID},
		Text: "zzz",
	})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	sess, ok := h.service.getCheckinSession(userID, time.Now())
	if !ok || sess.Flow != checkinFlowStationText {
		t.Fatalf("expected station input session to remain active, got %+v ok=%v", sess, ok)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	rows := extractInlineButtons(t, req.payload)
	labels := flattenButtonTexts(rows)
	expected := []string{"Try again", "Choose by time", "Cancel"}
	for _, want := range expected {
		found := false
		for _, got := range labels {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected button %q in %v", want, labels)
		}
	}
}

func TestRouteOriginInputTransitionsToDestinationPrompt(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)
	h.service.setCheckinTextFlow(userID, checkinFlowRouteOrigin, time.Now())

	err := h.service.handleMessage(context.Background(), &Message{
		From: &User{ID: userID},
		Chat: Chat{ID: userID},
		Text: "riga",
	})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	sess, ok := h.service.getCheckinSession(userID, time.Now())
	if !ok || sess.Flow != checkinFlowRouteDest {
		t.Fatalf("expected route destination flow, got %+v ok=%v", sess, ok)
	}
	if sess.OriginStationID != "riga" {
		t.Fatalf("expected origin station riga, got %q", sess.OriginStationID)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	text, _ := req.payload["text"].(string)
	if !strings.Contains(text, "destination from Riga") {
		t.Fatalf("expected destination prompt for Riga, got %q", text)
	}
}

func TestTimePickerBackButtonReturnsToCheckInEntry(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)

	err := h.service.editCheckInWindowPicker(context.Background(), &CallbackQuery{
		From: User{ID: userID},
		Message: &Message{
			MessageID: 7,
			Chat:      Chat{ID: userID},
		},
	}, domain.LanguageEN)
	if err != nil {
		t.Fatalf("edit window picker: %v", err)
	}

	req := h.recorder.lastRequest(t, "/editMessageText")
	rows := extractInlineButtons(t, req.payload)
	lastRow := rows[len(rows)-1]
	callbackData, _ := lastRow[0]["callback_data"].(string)
	if callbackData != "checkin:start" {
		t.Fatalf("expected back button to return to check-in entry, got %q", callbackData)
	}
}

func TestMyRideWithoutActiveRideLinksToCheckInEntry(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)

	err := h.service.sendMyRide(context.Background(), userID, userID, domain.LanguageEN)
	if err != nil {
		t.Fatalf("send my ride: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	rows := extractInlineButtons(t, req.payload)
	callbackData, _ := rows[0][0]["callback_data"].(string)
	if callbackData != "checkin:start" {
		t.Fatalf("expected check-in entry callback, got %q", callbackData)
	}
}

func TestMyRideInlineKeyboardUsesSingleButtonRowsForLatvian(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID int64 = 42
	const trainID = "train-riga-jelgava"
	h.ensureLatvian(t, userID)

	train, err := h.service.schedules.GetTrain(context.Background(), trainID)
	if err != nil {
		t.Fatalf("get train: %v", err)
	}
	if train == nil {
		t.Fatalf("expected train %q to exist in test snapshot", trainID)
	}

	now := time.Now().In(h.service.loc)
	if err := h.service.rides.CheckIn(context.Background(), userID, trainID, now, train.ArrivalAt.In(h.service.loc)); err != nil {
		t.Fatalf("check in ride: %v", err)
	}
	if err := h.service.sendMyRide(context.Background(), userID, userID, domain.LanguageLV); err != nil {
		t.Fatalf("send my ride: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	rows := extractInlineButtons(t, req.payload)
	if len(rows) != 4 {
		t.Fatalf("expected 4 inline rows, got %d", len(rows))
	}
	for i, row := range rows {
		if len(row) != 1 {
			t.Fatalf("expected row %d to have exactly one button, got %d", i, len(row))
		}
	}

	wantButtons := []struct {
		text     string
		callback string
	}{
		{text: h.service.catalog.T(domain.LanguageLV, "btn_report_inspection"), callback: BuildCallback("ride", "report")},
		{text: h.service.catalog.T(domain.LanguageLV, "btn_refresh"), callback: BuildCallback("ride", "refresh")},
		{text: h.service.catalog.T(domain.LanguageLV, "btn_mute_30m"), callback: BuildCallback("ride", "mute", "30", trainID)},
		{text: h.service.catalog.T(domain.LanguageLV, "btn_checkout"), callback: BuildCallback("ride", "checkout")},
	}
	for i, want := range wantButtons {
		text, _ := rows[i][0]["text"].(string)
		callback, _ := rows[i][0]["callback_data"].(string)
		if text != want.text {
			t.Fatalf("row %d text = %q, want %q", i, text, want.text)
		}
		if callback != want.callback {
			t.Fatalf("row %d callback = %q, want %q", i, callback, want.callback)
		}
	}
}

func TestStatusInlineKeyboardUsesSingleButtonRowsForLatvian(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID int64 = 42
	const trainID = "train-riga-jelgava"
	h.ensureLatvian(t, userID)

	if err := h.service.sendStatus(context.Background(), userID, userID, trainID, domain.LanguageLV); err != nil {
		t.Fatalf("send status: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	rows := extractInlineButtons(t, req.payload)
	if len(rows) != 3 {
		t.Fatalf("expected 3 inline rows, got %d", len(rows))
	}
	for i, row := range rows {
		if len(row) != 1 {
			t.Fatalf("expected row %d to have exactly one button, got %d", i, len(row))
		}
	}

	wantButtons := []struct {
		text     string
		callback string
	}{
		{text: h.service.catalog.T(domain.LanguageLV, "btn_checkin_confirm"), callback: BuildCallback("checkin", "train", trainID)},
		{text: h.service.catalog.T(domain.LanguageLV, "btn_report_inspection"), callback: BuildCallback("ride", "report")},
		{text: h.service.catalog.T(domain.LanguageLV, "btn_refresh"), callback: BuildCallback("status", "view", trainID)},
	}
	for i, want := range wantButtons {
		text, _ := rows[i][0]["text"].(string)
		callback, _ := rows[i][0]["callback_data"].(string)
		if text != want.text {
			t.Fatalf("row %d text = %q, want %q", i, text, want.text)
		}
		if callback != want.callback {
			t.Fatalf("row %d callback = %q, want %q", i, callback, want.callback)
		}
	}
}

func TestReportWithoutActiveRideLinksToCheckInEntry(t *testing.T) {
	h := newCheckinHarness(t)
	defer h.close()

	const userID = 42
	h.ensureEnglish(t, userID)

	err := h.service.sendReportPrompt(context.Background(), userID, userID, domain.LanguageEN)
	if err != nil {
		t.Fatalf("send report prompt: %v", err)
	}

	req := h.recorder.lastRequest(t, "/sendMessage")
	rows := extractInlineButtons(t, req.payload)
	callbackData, _ := rows[0][0]["callback_data"].(string)
	if callbackData != "checkin:start" {
		t.Fatalf("expected check-in entry callback, got %q", callbackData)
	}
}
