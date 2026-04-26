package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"satiksmebot/internal/catalog"
	"satiksmebot/internal/config"
	"satiksmebot/internal/spacetime"
	"satiksmebot/internal/store"
	"satiksmebot/internal/web"
)

func main() {
	catalogCfg, err := config.LoadCatalogOnly()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	sqlitePathFlag := flag.String("sqlite-path", envOr("DB_PATH", "./satiksme_bot.db"), "path to the existing SQLite database")
	catalogPathFlag := flag.String("catalog-path", catalogCfg.CatalogOutputPath, "path to the generated catalog json")
	bundleDirFlag := flag.String("bundle-dir", envOr("SATIKSME_WEB_BUNDLE_DIR", "./data/public-bundles"), "directory for the published static bundle")
	hostFlag := flag.String("spacetime-host", envOr("SATIKSME_RUNTIME_SPACETIME_HOST", envOr("SATIKSME_WEB_SPACETIME_HOST", "")), "SpacetimeDB host")
	databaseFlag := flag.String("spacetime-database", envOr("SATIKSME_RUNTIME_SPACETIME_DATABASE", envOr("SATIKSME_WEB_SPACETIME_DATABASE", "")), "SpacetimeDB database identity or name")
	issuerFlag := flag.String("spacetime-issuer", envOr("SATIKSME_RUNTIME_SPACETIME_OIDC_ISSUER", envOr("SATIKSME_WEB_SPACETIME_OIDC_ISSUER", "")), "OIDC issuer used to sign runtime service tokens")
	audienceFlag := flag.String("spacetime-audience", envOr("SATIKSME_RUNTIME_SPACETIME_OIDC_AUDIENCE", envOr("SATIKSME_WEB_SPACETIME_OIDC_AUDIENCE", "satiksme-bot-web")), "OIDC audience for runtime service tokens")
	keyFileFlag := flag.String("spacetime-key-file", envOr("SATIKSME_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE", envOr("SATIKSME_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE", "")), "RSA private key used to sign runtime service tokens")
	serviceSubjectFlag := flag.String("service-subject", envOr("SATIKSME_RUNTIME_SPACETIME_SERVICE_SUBJECT", "service:satiksme-bot"), "service token subject")
	serviceRolesFlag := flag.String("service-roles", envOr("SATIKSME_RUNTIME_SPACETIME_SERVICE_ROLES", "satiksme_service"), "comma-separated service roles")
	tokenTTLFlag := flag.Int("token-ttl-sec", envOrInt("SATIKSME_RUNTIME_SPACETIME_TOKEN_TTL_SEC", 900), "service token lifetime in seconds")
	retentionHoursFlag := flag.Int("retention-hours", envOrInt("DATA_RETENTION_HOURS", 24), "live-window retention used when exporting recent SQLite state")
	flag.Parse()

	if strings.TrimSpace(*hostFlag) == "" {
		log.Fatal("spacetime host is required")
	}
	if strings.TrimSpace(*databaseFlag) == "" {
		log.Fatal("spacetime database is required")
	}
	if strings.TrimSpace(*keyFileFlag) == "" {
		log.Fatal("spacetime key file is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              *hostFlag,
		Database:          *databaseFlag,
		Issuer:            *issuerFlag,
		Audience:          *audienceFlag,
		JWTPrivateKeyFile: *keyFileFlag,
		ServiceSubject:    *serviceSubjectFlag,
		ServiceRoles:      parseCSV(*serviceRolesFlag),
		TokenTTL:          time.Duration(*tokenTTLFlag) * time.Second,
		HTTPTimeout:       30 * time.Second,
	})
	if err != nil {
		log.Fatalf("configure spacetime syncer: %v", err)
	}

	catalogData, err := catalog.LoadCatalog(*catalogPathFlag)
	if err != nil {
		log.Fatalf("load catalog: %v", err)
	}
	bundlePublisher := web.NewStaticBundlePublisher(*bundleDirFlag, syncer)
	if _, err := bundlePublisher.PublishCatalog(ctx, catalogData, time.Now().UTC()); err != nil {
		log.Fatalf("publish bundle: %v", err)
	}

	cutoff := time.Now().UTC().Add(-time.Duration(*retentionHoursFlag) * time.Hour)
	stateSnapshot, err := store.ExportSQLiteStateSnapshot(ctx, *sqlitePathFlag, cutoff)
	if err != nil {
		log.Fatalf("export sqlite snapshot: %v", err)
	}
	if err := syncer.ImportStateSnapshot(ctx, stateSnapshot); err != nil {
		log.Fatalf("import sqlite snapshot: %v", err)
	}

	summary := map[string]any{
		"sqlitePath":  *sqlitePathFlag,
		"catalogPath": *catalogPathFlag,
		"bundleDir":   *bundleDirFlag,
		"spacetime": map[string]any{
			"host":     *hostFlag,
			"database": *databaseFlag,
		},
		"cutoff": cutoff.Format(time.RFC3339),
		"counts": map[string]int{
			"stops":            len(catalogData.Stops),
			"routes":           len(catalogData.Routes),
			"stopSightings":    len(stateSnapshot.StopSightings),
			"vehicleSightings": len(stateSnapshot.VehicleSightings),
			"incidentVotes":    len(stateSnapshot.IncidentVotes),
			"incidentComments": len(stateSnapshot.IncidentComments),
			"reportDumpItems":  len(stateSnapshot.ReportDumpItems),
		},
	}
	body, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println(string(body))
}

func envOr(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
