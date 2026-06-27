package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetChatByUsername(t *testing.T) {
	var gotChatID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/getChat" {
			t.Fatalf("path = %q, want /getChat", r.URL.Path)
		}
		gotChatID = r.URL.Query().Get("chat_id")
		_ = json.NewEncoder(w).Encode(apiResponse[Chat]{
			OK: true,
			Result: Chat{
				ID:       42,
				Type:     "private",
				Username: "darja_smm_prod",
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-token", time.Second)
	client.baseURL = server.URL
	client.redactedBaseURL = server.URL

	chat, err := client.GetChat(t.Context(), "@darja_smm_prod")
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if gotChatID != "@darja_smm_prod" {
		t.Fatalf("chat_id query = %q, want @darja_smm_prod", gotChatID)
	}
	if chat.ID != 42 || chat.Username != "darja_smm_prod" || chat.Type != "private" {
		t.Fatalf("chat = %#v, want private @darja_smm_prod ID 42", chat)
	}
}
