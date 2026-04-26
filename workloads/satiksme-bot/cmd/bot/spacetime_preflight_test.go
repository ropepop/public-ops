package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"satiksmebot/internal/config"
	"satiksmebot/internal/spacetime"
)

func TestConfiguredSpacetimeSchemaTargets(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		SatiksmeWebSpacetimeEnabled:      true,
		SatiksmeWebSpacetimeHost:         "https://web.example.com/",
		SatiksmeWebSpacetimeDatabase:     "web-db",
		SatiksmeRuntimeSpacetimeEnabled:  true,
		SatiksmeRuntimeSpacetimeHost:     "https://runtime.example.com/",
		SatiksmeRuntimeSpacetimeDatabase: "runtime-db",
	}

	targets := configuredSpacetimeSchemaTargets(cfg)
	if len(targets) != 2 {
		t.Fatalf("configuredSpacetimeSchemaTargets() length = %d, want 2", len(targets))
	}
	if got, want := targets[0], (spacetime.SchemaTarget{Host: "https://web.example.com", Database: "web-db"}); got != want {
		t.Fatalf("configuredSpacetimeSchemaTargets()[0] = %#v, want %#v", got, want)
	}
	if got, want := targets[1], (spacetime.SchemaTarget{Host: "https://runtime.example.com", Database: "runtime-db"}); got != want {
		t.Fatalf("configuredSpacetimeSchemaTargets()[1] = %#v, want %#v", got, want)
	}
}

func TestConfiguredSpacetimeSchemaTargetsDeduplicatesTargets(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		SatiksmeWebSpacetimeEnabled:      true,
		SatiksmeWebSpacetimeHost:         "https://maincloud.spacetimedb.com/",
		SatiksmeWebSpacetimeDatabase:     "shared-db",
		SatiksmeRuntimeSpacetimeEnabled:  true,
		SatiksmeRuntimeSpacetimeHost:     "https://maincloud.spacetimedb.com",
		SatiksmeRuntimeSpacetimeDatabase: "shared-db",
	}

	targets := configuredSpacetimeSchemaTargets(cfg)
	if len(targets) != 1 {
		t.Fatalf("configuredSpacetimeSchemaTargets() length = %d, want 1", len(targets))
	}
}

func TestVerifyConfiguredSpacetimeSchemaSkipsWhenDisabled(t *testing.T) {
	t.Parallel()

	called := false
	err := verifyConfiguredSpacetimeSchema(context.Background(), config.Config{}, func(context.Context, *http.Client, spacetime.SchemaTarget) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("verifyConfiguredSpacetimeSchema() error = %v, want nil", err)
	}
	if called {
		t.Fatalf("verifyConfiguredSpacetimeSchema() unexpectedly invoked verifier")
	}
}

func TestVerifyConfiguredSpacetimeSchemaFailsOnVerifierError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("schema mismatch")
	cfg := config.Config{
		HTTPTimeoutSec:                   17,
		SatiksmeRuntimeSpacetimeEnabled:  true,
		SatiksmeRuntimeSpacetimeHost:     "https://maincloud.spacetimedb.com",
		SatiksmeRuntimeSpacetimeDatabase: "runtime-db",
	}

	var gotTarget spacetime.SchemaTarget
	var gotTimeout int
	err := verifyConfiguredSpacetimeSchema(context.Background(), cfg, func(_ context.Context, client *http.Client, target spacetime.SchemaTarget) error {
		gotTarget = target
		gotTimeout = int(client.Timeout.Seconds())
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("verifyConfiguredSpacetimeSchema() error = %v, want %v", err, expectedErr)
	}
	if got, want := gotTarget, (spacetime.SchemaTarget{Host: "https://maincloud.spacetimedb.com", Database: "runtime-db"}); got != want {
		t.Fatalf("verifyConfiguredSpacetimeSchema() target = %#v, want %#v", got, want)
	}
	if gotTimeout != 17 {
		t.Fatalf("verifyConfiguredSpacetimeSchema() client timeout = %d, want 17", gotTimeout)
	}
}
