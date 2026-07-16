package web

import (
	"fmt"
	"testing"
)

func TestBrowserSocketConnectionLimits(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		server := &Server{clients: map[*client]struct{}{}}
		clients := make([]*client, 0, maxBrowserSocketsPerSession)
		for i := 0; i < maxBrowserSocketsPerSession; i++ {
			candidate := &client{sessionID: "session-a", email: "member@example.test"}
			if !server.tryAddClient(candidate) {
				t.Fatalf("connection %d within the per-session limit was rejected", i+1)
			}
			clients = append(clients, candidate)
		}
		if server.tryAddClient(&client{sessionID: "session-a", email: "member@example.test"}) {
			t.Fatal("connection above the per-session limit was accepted")
		}
		server.removeClient(clients[0])
		if !server.tryAddClient(&client{sessionID: "session-a", email: "member@example.test"}) {
			t.Fatal("released per-session capacity was not reusable")
		}
	})

	t.Run("identity", func(t *testing.T) {
		server := &Server{clients: map[*client]struct{}{}}
		for i := 0; i < maxBrowserSocketsPerIdentity; i++ {
			candidate := &client{sessionID: fmt.Sprintf("session-%d", i), email: "MEMBER@example.test"}
			if !server.tryAddClient(candidate) {
				t.Fatalf("connection %d within the per-identity limit was rejected", i+1)
			}
		}
		if server.tryAddClient(&client{sessionID: "one-more-session", email: "member@example.test"}) {
			t.Fatal("connection above the case-insensitive per-identity limit was accepted")
		}
	})

	t.Run("server", func(t *testing.T) {
		server := &Server{clients: map[*client]struct{}{}}
		for i := 0; i < maxBrowserSocketConnections; i++ {
			candidate := &client{
				sessionID: fmt.Sprintf("session-%d", i),
				email:     fmt.Sprintf("member-%d@example.test", i),
			}
			if !server.tryAddClient(candidate) {
				t.Fatalf("connection %d within the server limit was rejected", i+1)
			}
		}
		if server.tryAddClient(&client{sessionID: "overflow", email: "overflow@example.test"}) {
			t.Fatal("connection above the server limit was accepted")
		}
	})
}
