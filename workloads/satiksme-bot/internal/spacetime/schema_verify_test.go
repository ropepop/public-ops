package spacetime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyExpectedSchemaSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, http.MethodPost; got != want {
			t.Fatalf("request method = %q, want %q", got, want)
		}
		if got, want := r.URL.Path, "/v1/database/live-db/call/"+schemaInfoProcedureName; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
		if got, want := strings.TrimSpace(r.Header.Get("Content-Type")), "application/json"; got != want {
			t.Fatalf("content type = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(fmt.Sprintf("{\"module\":\"%s\",\"schemaVersion\":\"%s\"}", ExpectedSchemaModule, ExpectedSchemaVersion)))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err != nil {
		t.Fatalf("VerifyExpectedSchema() error = %v, want nil", err)
	}
}

func TestVerifyExpectedSchemaMissingProcedure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`External attempt to call nonexistent reducer "satiksmebot_schema_info" failed.`))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err == nil {
		t.Fatalf("VerifyExpectedSchema() error = nil, want missing procedure error")
	}
	if !strings.Contains(err.Error(), schemaInfoProcedureName) {
		t.Fatalf("VerifyExpectedSchema() error = %q, want mention of %q", err, schemaInfoProcedureName)
	}
}

func TestVerifyExpectedSchemaVersionMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"module":"satiksme-bot","schemaVersion":"old-version"}`))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err == nil {
		t.Fatalf("VerifyExpectedSchema() error = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), `schemaVersion="old-version"`) {
		t.Fatalf("VerifyExpectedSchema() error = %q, want observed version", err)
	}
}

func TestVerifyExpectedSchemaMalformedPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"module":"satiksme-bot"}`))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err == nil {
		t.Fatalf("VerifyExpectedSchema() error = nil, want malformed payload error")
	}
	if !strings.Contains(err.Error(), "missing module or schemaVersion") {
		t.Fatalf("VerifyExpectedSchema() error = %q, want malformed payload message", err)
	}
}

func TestExpectedSchemaVersionMatchesSpacetimeModule(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	text := string(body)
	moduleMatch := regexp.MustCompile(`const SATIKSMEBOT_SCHEMA_MODULE = '([^']+)'`).FindStringSubmatch(text)
	if len(moduleMatch) != 2 {
		t.Fatalf("Spacetime module is missing SATIKSMEBOT_SCHEMA_MODULE")
	}
	versionMatch := regexp.MustCompile(`const SATIKSMEBOT_SCHEMA_VERSION = '([^']+)'`).FindStringSubmatch(text)
	if len(versionMatch) != 2 {
		t.Fatalf("Spacetime module is missing SATIKSMEBOT_SCHEMA_VERSION")
	}
	if got, want := moduleMatch[1], ExpectedSchemaModule; got != want {
		t.Fatalf("Spacetime module schema module = %q, want %q", got, want)
	}
	if got, want := versionMatch[1], ExpectedSchemaVersion; got != want {
		t.Fatalf("Spacetime module schema version = %q, want %q", got, want)
	}
}

func spacetimeModuleIndexPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "spacetimedb", "src", "index.ts")
}
