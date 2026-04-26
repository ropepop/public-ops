package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"satiksmebot/internal/config"
	"satiksmebot/internal/spacetime"
)

type spacetimeSchemaVerifier func(context.Context, *http.Client, spacetime.SchemaTarget) error

func verifyConfiguredSpacetimeSchema(ctx context.Context, cfg config.Config, verifier spacetimeSchemaVerifier) error {
	targets := configuredSpacetimeSchemaTargets(cfg)
	if len(targets) == 0 {
		return nil
	}
	timeout := time.Duration(cfg.HTTPTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	for _, target := range targets {
		if err := verifier(ctx, client, target); err != nil {
			return fmt.Errorf("verify Spacetime schema for host %s database %s: %w", target.Host, target.Database, err)
		}
	}
	return nil
}

func configuredSpacetimeSchemaTargets(cfg config.Config) []spacetime.SchemaTarget {
	targets := make([]spacetime.SchemaTarget, 0, 2)
	seen := map[string]struct{}{}
	add := func(host, database string) {
		host = strings.TrimRight(strings.TrimSpace(host), "/")
		database = strings.TrimSpace(database)
		if host == "" || database == "" {
			return
		}
		key := host + "\n" + database
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, spacetime.SchemaTarget{Host: host, Database: database})
	}
	if cfg.SatiksmeWebSpacetimeEnabled {
		add(cfg.SatiksmeWebSpacetimeHost, cfg.SatiksmeWebSpacetimeDatabase)
	}
	if cfg.SatiksmeRuntimeSpacetimeEnabled {
		add(cfg.SatiksmeRuntimeSpacetimeHost, cfg.SatiksmeRuntimeSpacetimeDatabase)
	}
	return targets
}
