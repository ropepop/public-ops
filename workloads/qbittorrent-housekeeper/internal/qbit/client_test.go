package qbit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNoAuthenticationCallsAPIDirectly(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		paths = append(paths, request.URL.Path)
		mu.Unlock()
		switch request.URL.Path {
		case "/api/v2/torrents/info":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"hash":"abc","size":42,"completed":7,"state":"stoppedDL"}]`))
		case "/api/v2/torrents/delete":
			assertFormValue(t, request, "hashes", "abc")
			assertFormValue(t, request, "deleteFiles", "true")
			writer.WriteHeader(http.StatusOK)
		case "/api/v2/auth/login":
			t.Error("no-auth client unexpectedly called login")
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", "", time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	torrents, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(torrents) != 1 || torrents[0].Hash != "abc" || torrents[0].Size != 42 {
		t.Fatalf("List() = %#v", torrents)
	}
	if err := client.Delete(context.Background(), "abc", true); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(paths, []string{"/api/v2/torrents/info", "/api/v2/torrents/delete"}) {
		t.Fatalf("request paths = %v", paths)
	}
}

func TestOptionalAuthenticationLogsInAndRetries(t *testing.T) {
	var loginCount int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/torrents/info":
			cookie, err := request.Cookie("SID")
			if err != nil || cookie.Value != "session" {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[]`))
		case "/api/v2/auth/login":
			loginCount++
			assertFormValue(t, request, "username", "operator")
			assertFormValue(t, request, "password", "secret")
			http.SetCookie(writer, &http.Cookie{Name: "SID", Value: "session", Path: "/"})
			_, _ = writer.Write([]byte("Ok."))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "operator", "secret", time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if loginCount != 1 {
		t.Fatalf("login count = %d, want 1", loginCount)
	}
}

func TestAuthenticationFailureIsReportedWithoutPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/torrents/info":
			writer.WriteHeader(http.StatusForbidden)
		case "/api/v2/auth/login":
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("Fails."))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "operator", "do-not-leak", time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("List() error = %v", err)
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("password leaked in error: %v", err)
	}
}

func TestTagAndLifecycleForms(t *testing.T) {
	type requestRecord struct {
		path string
		form url.Values
	}
	var records []requestRecord
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		records = append(records, requestRecord{path: request.URL.Path, form: request.Form})
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { return client.Stop(ctx, "hash") },
		func() error { return client.Start(ctx, "hash") },
		func() error { return client.AddTags(ctx, "hash", "one", "two") },
		func() error { return client.RemoveTags(ctx, "hash", "one") },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	wantPaths := []string{
		"/api/v2/torrents/stop",
		"/api/v2/torrents/start",
		"/api/v2/torrents/addTags",
		"/api/v2/torrents/removeTags",
	}
	if len(records) != len(wantPaths) {
		t.Fatalf("records = %#v", records)
	}
	for i, wantPath := range wantPaths {
		if records[i].path != wantPath || records[i].form.Get("hashes") != "hash" {
			t.Fatalf("record %d = %#v", i, records[i])
		}
	}
	if got := records[2].form.Get("tags"); got != "one,two" {
		t.Fatalf("addTags tags = %q", got)
	}
}

func assertFormValue(t *testing.T, request *http.Request, key, want string) {
	t.Helper()
	if err := request.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}
	if got := request.Form.Get(key); got != want {
		t.Errorf("%s = %q, want %q; form=%s", key, got, want, fmt.Sprint(request.Form))
	}
}
