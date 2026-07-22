package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"satiksmebot/internal/model"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) HealthCheck(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	return tx.Rollback()
}

func (s *SQLiteStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)

	for _, version := range versions {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if exists > 0 {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile(path.Join("migrations", version))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration tx %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec migration %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("mark migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

func (s *SQLiteStore) InsertStopSighting(ctx context.Context, sighting model.StopSighting) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stop_sightings(id, stop_id, user_id, is_hidden, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, sighting.ID, sighting.StopID, sighting.UserID, boolToInt(sighting.Hidden), sighting.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) InsertStopSightingWithVote(ctx context.Context, sighting model.StopSighting, vote model.IncidentVote, event model.IncidentVoteEvent, dedupeWindow time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if !sighting.Hidden {
		claimed, err := claimReportDedupeTx(ctx, tx, "stop", sighting.UserID, sighting.StopID, sighting.CreatedAt, dedupeWindow)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if !claimed {
			_ = tx.Rollback()
			return ErrDuplicateReport
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stop_sightings(id, stop_id, user_id, is_hidden, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, sighting.ID, sighting.StopID, sighting.UserID, boolToInt(sighting.Hidden), sighting.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := recordIncidentVoteTx(ctx, tx, vote, event); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetLastStopSightingByUserScope(ctx context.Context, userID int64, stopID string) (*model.StopSighting, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, stop_id, user_id, is_hidden, created_at
		FROM stop_sightings
		WHERE user_id = ? AND stop_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, userID, stopID)
	var (
		item   model.StopSighting
		hidden int
		at     string
	)
	if err := row.Scan(&item.ID, &item.StopID, &item.UserID, &hidden, &at); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Hidden = hidden != 0
	parsedAt, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return nil, err
	}
	item.CreatedAt = parsedAt
	return &item, nil
}

func (s *SQLiteStore) ListStopSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.StopSighting, error) {
	query := `
		SELECT id, stop_id, user_id, is_hidden, created_at
		FROM stop_sightings
		WHERE created_at >= ?
	`
	args := []any{since.UTC().Format(time.RFC3339)}
	if strings.TrimSpace(stopID) != "" {
		query += ` AND stop_id = ?`
		args = append(args, stopID)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.StopSighting, 0)
	for rows.Next() {
		var (
			item   model.StopSighting
			hidden int
			at     string
		)
		if err := rows.Scan(&item.ID, &item.StopID, &item.UserID, &hidden, &at); err != nil {
			return nil, err
		}
		item.Hidden = hidden != 0
		parsedAt, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = parsedAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) InsertVehicleSighting(ctx context.Context, sighting model.VehicleSighting) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vehicle_sightings(
			id, stop_id, user_id, mode, route_label, direction, destination,
			departure_seconds, live_row_id, scope_key, is_hidden, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sighting.ID, sighting.StopID, sighting.UserID, sighting.Mode, sighting.RouteLabel, sighting.Direction, sighting.Destination, sighting.DepartureSeconds, sighting.LiveRowID, sighting.ScopeKey, boolToInt(sighting.Hidden), sighting.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) InsertVehicleSightingWithVote(ctx context.Context, sighting model.VehicleSighting, vote model.IncidentVote, event model.IncidentVoteEvent, dedupeWindow time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if !sighting.Hidden {
		claimed, err := claimReportDedupeTx(ctx, tx, "vehicle", sighting.UserID, sighting.ScopeKey, sighting.CreatedAt, dedupeWindow)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if !claimed {
			_ = tx.Rollback()
			return ErrDuplicateReport
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vehicle_sightings(
			id, stop_id, user_id, mode, route_label, direction, destination,
			departure_seconds, live_row_id, scope_key, is_hidden, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sighting.ID, sighting.StopID, sighting.UserID, sighting.Mode, sighting.RouteLabel, sighting.Direction, sighting.Destination, sighting.DepartureSeconds, sighting.LiveRowID, sighting.ScopeKey, boolToInt(sighting.Hidden), sighting.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := recordIncidentVoteTx(ctx, tx, vote, event); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func claimReportDedupeTx(ctx context.Context, tx *sql.Tx, reportKind string, userID int64, scopeKey string, reportAt time.Time, window time.Duration) (bool, error) {
	if window <= 0 {
		return true, nil
	}
	reportAt = reportAt.UTC()
	cutoff := reportAt.Add(-window).UTC()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO report_dedupe_claims(report_kind, user_id, scope_key, last_report_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(report_kind, user_id, scope_key) DO UPDATE SET
			last_report_at = excluded.last_report_at
		WHERE report_dedupe_claims.last_report_at <= ?
	`, strings.TrimSpace(reportKind), userID, strings.TrimSpace(scopeKey), reportAt.Format(time.RFC3339), cutoff.Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) GetLastVehicleSightingByUserScope(ctx context.Context, userID int64, scopeKey string) (*model.VehicleSighting, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, stop_id, user_id, mode, route_label, direction, destination,
		       departure_seconds, live_row_id, scope_key, is_hidden, created_at
		FROM vehicle_sightings
		WHERE user_id = ? AND scope_key = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, userID, scopeKey)
	var (
		item   model.VehicleSighting
		hidden int
		at     string
	)
	if err := row.Scan(&item.ID, &item.StopID, &item.UserID, &item.Mode, &item.RouteLabel, &item.Direction, &item.Destination, &item.DepartureSeconds, &item.LiveRowID, &item.ScopeKey, &hidden, &at); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Hidden = hidden != 0
	parsedAt, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return nil, err
	}
	item.CreatedAt = parsedAt
	return &item, nil
}

func (s *SQLiteStore) ListVehicleSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.VehicleSighting, error) {
	query := `
		SELECT id, stop_id, user_id, mode, route_label, direction, destination,
		       departure_seconds, live_row_id, scope_key, is_hidden, created_at
		FROM vehicle_sightings
		WHERE created_at >= ?
	`
	args := []any{since.UTC().Format(time.RFC3339)}
	if strings.TrimSpace(stopID) != "" {
		query += ` AND stop_id = ?`
		args = append(args, stopID)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.VehicleSighting, 0)
	for rows.Next() {
		var (
			item   model.VehicleSighting
			hidden int
			at     string
		)
		if err := rows.Scan(&item.ID, &item.StopID, &item.UserID, &item.Mode, &item.RouteLabel, &item.Direction, &item.Destination, &item.DepartureSeconds, &item.LiveRowID, &item.ScopeKey, &hidden, &at); err != nil {
			return nil, err
		}
		item.Hidden = hidden != 0
		parsedAt, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = parsedAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) InsertAreaReport(ctx context.Context, report model.AreaReport) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO area_reports(
			id, user_id, latitude, longitude, radius_meters, description,
			scope_key, is_hidden, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, report.ID, report.UserID, report.Latitude, report.Longitude, report.RadiusMeters, strings.TrimSpace(report.Description), strings.TrimSpace(report.ScopeKey), boolToInt(report.Hidden), report.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) InsertAreaReportWithVote(ctx context.Context, report model.AreaReport, vote model.IncidentVote, event model.IncidentVoteEvent, dedupeWindow time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if !report.Hidden {
		claimed, err := claimReportDedupeTx(ctx, tx, "area", report.UserID, report.ScopeKey, report.CreatedAt, dedupeWindow)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if !claimed {
			_ = tx.Rollback()
			return ErrDuplicateReport
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO area_reports(
			id, user_id, latitude, longitude, radius_meters, description,
			scope_key, is_hidden, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, report.ID, report.UserID, report.Latitude, report.Longitude, report.RadiusMeters, strings.TrimSpace(report.Description), strings.TrimSpace(report.ScopeKey), boolToInt(report.Hidden), report.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := recordIncidentVoteTx(ctx, tx, vote, event); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetLastAreaReportByUserScope(ctx context.Context, userID int64, scopeKey string) (*model.AreaReport, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, latitude, longitude, radius_meters, description, scope_key, is_hidden, created_at
		FROM area_reports
		WHERE user_id = ? AND scope_key = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, userID, strings.TrimSpace(scopeKey))
	var (
		item   model.AreaReport
		hidden int
		at     string
	)
	if err := row.Scan(&item.ID, &item.UserID, &item.Latitude, &item.Longitude, &item.RadiusMeters, &item.Description, &item.ScopeKey, &hidden, &at); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Hidden = hidden != 0
	parsedAt, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return nil, err
	}
	item.CreatedAt = parsedAt
	return &item, nil
}

func (s *SQLiteStore) ListAreaReportsSince(ctx context.Context, since time.Time, limit int) ([]model.AreaReport, error) {
	query := `
		SELECT id, user_id, latitude, longitude, radius_meters, description, scope_key, is_hidden, created_at
		FROM area_reports
		WHERE created_at >= ?
		ORDER BY created_at DESC
	`
	args := []any{since.UTC().Format(time.RFC3339)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.AreaReport, 0)
	for rows.Next() {
		var (
			item   model.AreaReport
			hidden int
			at     string
		)
		if err := rows.Scan(&item.ID, &item.UserID, &item.Latitude, &item.Longitude, &item.RadiusMeters, &item.Description, &item.ScopeKey, &hidden, &at); err != nil {
			return nil, err
		}
		item.Hidden = hidden != 0
		parsedAt, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = parsedAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertIncidentVote(ctx context.Context, vote model.IncidentVote) error {
	createdAt := vote.CreatedAt
	if createdAt.IsZero() {
		createdAt = vote.UpdatedAt
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := vote.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_votes(incident_id, user_id, nickname, vote_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(incident_id, user_id) DO UPDATE SET
			nickname = excluded.nickname,
			vote_value = excluded.vote_value,
			updated_at = excluded.updated_at
	`, vote.IncidentID, vote.UserID, strings.TrimSpace(vote.Nickname), string(vote.Value), createdAt.UTC().Format(time.RFC3339), updatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) RecordIncidentVote(ctx context.Context, vote model.IncidentVote, event model.IncidentVoteEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := recordIncidentVoteTx(ctx, tx, vote, event); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func recordIncidentVoteTx(ctx context.Context, tx *sql.Tx, vote model.IncidentVote, event model.IncidentVoteEvent) error {
	createdAt := vote.CreatedAt
	if createdAt.IsZero() {
		createdAt = vote.UpdatedAt
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := vote.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	eventAt := event.CreatedAt
	if eventAt.IsZero() {
		eventAt = updatedAt
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_votes(incident_id, user_id, nickname, vote_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(incident_id, user_id) DO UPDATE SET
			nickname = excluded.nickname,
			vote_value = excluded.vote_value,
			updated_at = excluded.updated_at
	`, vote.IncidentID, vote.UserID, strings.TrimSpace(vote.Nickname), string(vote.Value), createdAt.UTC().Format(time.RFC3339), updatedAt.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_vote_events(id, incident_id, user_id, nickname, vote_value, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.IncidentID, event.UserID, strings.TrimSpace(event.Nickname), string(event.Value), string(event.Source), eventAt.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) ListIncidentVotes(ctx context.Context, incidentID string) ([]model.IncidentVote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT incident_id, user_id, nickname, vote_value, created_at, updated_at
		FROM incident_votes
		WHERE incident_id = ?
		ORDER BY updated_at DESC, user_id ASC
	`, strings.TrimSpace(incidentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.IncidentVote, 0)
	for rows.Next() {
		var (
			item         model.IncidentVote
			valueRaw     string
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(&item.IncidentID, &item.UserID, &item.Nickname, &valueRaw, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, err
		}
		item.Value = model.IncidentVoteValue(valueRaw)
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, err
		}
		updatedAt, err := time.Parse(time.RFC3339, updatedAtRaw)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt
		item.UpdatedAt = updatedAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]model.IncidentVoteEvent, error) {
	query := `
		SELECT id, incident_id, user_id, nickname, vote_value, source, created_at
		FROM incident_vote_events
	`
	args := make([]any, 0, 2)
	clauses := make([]string, 0, 2)
	if strings.TrimSpace(incidentID) != "" {
		clauses = append(clauses, `incident_id = ?`)
		args = append(args, strings.TrimSpace(incidentID))
	}
	if !since.IsZero() {
		clauses = append(clauses, `created_at >= ?`)
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.IncidentVoteEvent, 0)
	for rows.Next() {
		var (
			item         model.IncidentVoteEvent
			valueRaw     string
			sourceRaw    string
			createdAtRaw string
		)
		if err := rows.Scan(&item.ID, &item.IncidentID, &item.UserID, &item.Nickname, &valueRaw, &sourceRaw, &createdAtRaw); err != nil {
			return nil, err
		}
		item.Value = model.IncidentVoteValue(valueRaw)
		item.Source = model.IncidentVoteSource(sourceRaw)
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) CountMapReportsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM stop_sightings WHERE user_id = ? AND is_hidden = 0 AND created_at >= ?) +
			(SELECT COUNT(*) FROM vehicle_sightings WHERE user_id = ? AND is_hidden = 0 AND created_at >= ?) +
			(SELECT COUNT(*) FROM area_reports WHERE user_id = ? AND is_hidden = 0 AND created_at >= ?)
	`, userID, since.UTC().Format(time.RFC3339), userID, since.UTC().Format(time.RFC3339), userID, since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) CountIncidentVoteEventsByUserSince(ctx context.Context, userID int64, source model.IncidentVoteSource, since time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_vote_events
		WHERE user_id = ? AND source = ? AND created_at >= ?
	`, userID, string(source), since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) CountIncidentCommentsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_comments
		WHERE user_id = ? AND created_at >= ?
	`, userID, since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) CountIncidentCommentsByIncidentSince(ctx context.Context, incidentID string, since time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_comments
		WHERE incident_id = ? AND created_at >= ?
	`, strings.TrimSpace(incidentID), since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) InsertIncidentComment(ctx context.Context, comment model.IncidentComment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_comments(id, incident_id, user_id, nickname, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, comment.ID, comment.IncidentID, comment.UserID, strings.TrimSpace(comment.Nickname), strings.TrimSpace(comment.Body), comment.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]model.IncidentComment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_id, user_id, nickname, body, created_at
		FROM incident_comments
		WHERE incident_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, strings.TrimSpace(incidentID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.IncidentComment, 0)
	for rows.Next() {
		var (
			item         model.IncidentComment
			createdAtRaw string
		)
		if err := rows.Scan(&item.ID, &item.IncidentID, &item.UserID, &item.Nickname, &item.Body, &createdAtRaw); err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) CleanupExpired(ctx context.Context, cutoff time.Time) (CleanupResult, error) {
	result := CleanupResult{}
	stopRes, err := s.db.ExecContext(ctx, `DELETE FROM stop_sightings WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return result, err
	}
	vehicleRes, err := s.db.ExecContext(ctx, `DELETE FROM vehicle_sightings WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return result, err
	}
	areaRes, err := s.db.ExecContext(ctx, `DELETE FROM area_reports WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return result, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM incident_vote_events WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM report_dedupe_claims WHERE last_report_at < ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM chat_analyzer_messages WHERE received_at < ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	result.StopSightingsDeleted, _ = stopRes.RowsAffected()
	result.VehicleSightingsDeleted, _ = vehicleRes.RowsAffected()
	result.AreaReportsDeleted, _ = areaRes.RowsAffected()
	return result, nil
}

func (s *SQLiteStore) EnqueueReportDump(ctx context.Context, item ReportDumpItem) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO report_dump_queue(id, payload, attempts, created_at, next_attempt_at, last_attempt_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.Payload, item.Attempts, item.CreatedAt.UTC().Format(time.RFC3339), item.NextAttemptAt.UTC().Format(time.RFC3339), formatOptionalTime(item.LastAttemptAt), item.LastError)
	return err
}

func (s *SQLiteStore) NextReportDump(ctx context.Context, now time.Time) (*ReportDumpItem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, payload, attempts, created_at, next_attempt_at, last_attempt_at, last_error
		FROM report_dump_queue
		WHERE next_attempt_at <= ?
		ORDER BY next_attempt_at ASC, created_at ASC
		LIMIT 1
	`, now.UTC().Format(time.RFC3339))
	return scanReportDumpItem(row)
}

func (s *SQLiteStore) PeekNextReportDump(ctx context.Context) (*ReportDumpItem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, payload, attempts, created_at, next_attempt_at, last_attempt_at, last_error
		FROM report_dump_queue
		ORDER BY next_attempt_at ASC, created_at ASC
		LIMIT 1
	`)
	return scanReportDumpItem(row)
}

func scanReportDumpItem(row *sql.Row) (*ReportDumpItem, error) {
	var (
		item           ReportDumpItem
		createdAtRaw   string
		nextAttemptRaw string
		lastAttemptRaw string
	)
	if err := row.Scan(&item.ID, &item.Payload, &item.Attempts, &createdAtRaw, &nextAttemptRaw, &lastAttemptRaw, &item.LastError); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return nil, err
	}
	item.NextAttemptAt, err = time.Parse(time.RFC3339, nextAttemptRaw)
	if err != nil {
		return nil, err
	}
	item.LastAttemptAt, err = parseOptionalTime(lastAttemptRaw)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *SQLiteStore) DeleteReportDump(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM report_dump_queue WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) UpdateReportDumpFailure(ctx context.Context, id string, attempts int, nextAttemptAt, lastAttemptAt time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE report_dump_queue
		SET attempts = ?, next_attempt_at = ?, last_attempt_at = ?, last_error = ?
		WHERE id = ?
	`, attempts, nextAttemptAt.UTC().Format(time.RFC3339), lastAttemptAt.UTC().Format(time.RFC3339), lastError, id)
	return err
}

func (s *SQLiteStore) PendingReportDumpCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_dump_queue`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) GetChatAnalyzerCheckpoint(ctx context.Context, chatID string) (int64, bool, error) {
	var lastMessageID int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT last_message_id
		FROM chat_analyzer_checkpoints
		WHERE chat_id = ?
	`, strings.TrimSpace(chatID)).Scan(&lastMessageID); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return lastMessageID, true, nil
}

func (s *SQLiteStore) SetChatAnalyzerCheckpoint(ctx context.Context, chatID string, lastMessageID int64, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_analyzer_checkpoints(chat_id, last_message_id, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			last_message_id = MAX(chat_analyzer_checkpoints.last_message_id, excluded.last_message_id),
			updated_at = excluded.updated_at
	`, strings.TrimSpace(chatID), lastMessageID, updatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) EnqueueChatAnalyzerMessage(ctx context.Context, item model.ChatAnalyzerMessage) (bool, error) {
	status := item.Status
	if status == "" {
		status = model.ChatAnalyzerMessagePending
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO chat_analyzer_messages(
			id, chat_id, message_id, sender_id, sender_stable_id, sender_nickname, raw_text,
			message_date, received_at, reply_to_message_id, status, attempts,
			analysis_json, applied_action_id, applied_target_key, batch_id, last_error, processed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, strings.TrimSpace(item.ChatID), item.MessageID, item.SenderID, strings.TrimSpace(item.SenderStableID), strings.TrimSpace(item.SenderNickname), item.Text, item.MessageDate.UTC().Format(time.RFC3339), item.ReceivedAt.UTC().Format(time.RFC3339), item.ReplyToMessageID, string(status), item.Attempts, item.AnalysisJSON, item.AppliedActionID, item.AppliedTargetKey, item.BatchID, item.LastError, formatOptionalTime(item.ProcessedAt))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) ListPendingChatAnalyzerMessages(ctx context.Context, limit int) ([]model.ChatAnalyzerMessage, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chat_id, message_id, sender_id, sender_stable_id, sender_nickname, raw_text,
		       message_date, received_at, reply_to_message_id, status, attempts,
		       analysis_json, applied_action_id, applied_target_key, batch_id, last_error, processed_at
		FROM chat_analyzer_messages
		WHERE status = ?
		ORDER BY received_at ASC, message_id ASC
		LIMIT ?
	`, string(model.ChatAnalyzerMessagePending), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ChatAnalyzerMessage, 0)
	for rows.Next() {
		item, err := scanChatAnalyzerMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) MarkChatAnalyzerMessageProcessed(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, lastError string, processedAt time.Time) error {
	return s.MarkChatAnalyzerMessageProcessedInBatch(ctx, id, status, analysisJSON, appliedActionID, appliedTargetKey, "", lastError, processedAt)
}

func (s *SQLiteStore) MarkChatAnalyzerMessageProcessedInBatch(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, batchID, lastError string, processedAt time.Time) error {
	if status == "" {
		status = model.ChatAnalyzerMessageFailed
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE chat_analyzer_messages
		SET status = ?,
		    attempts = attempts + 1,
		    analysis_json = ?,
		    applied_action_id = ?,
		    applied_target_key = ?,
		    batch_id = ?,
		    last_error = ?,
		    processed_at = ?
		WHERE id = ?
	`, string(status), strings.TrimSpace(analysisJSON), strings.TrimSpace(appliedActionID), strings.TrimSpace(appliedTargetKey), strings.TrimSpace(batchID), strings.TrimSpace(lastError), processedAt.UTC().Format(time.RFC3339), strings.TrimSpace(id))
	return err
}

func (s *SQLiteStore) ExpireStaleChatAnalyzerMessages(ctx context.Context, messageDateBefore, processedAt time.Time, analysisJSON string) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE chat_analyzer_messages
		SET status = ?,
		    attempts = attempts + 1,
		    analysis_json = ?,
		    last_error = '',
		    processed_at = ?
		WHERE status = ? AND message_date < ?
	`, string(model.ChatAnalyzerMessageIgnored), strings.TrimSpace(analysisJSON), processedAt.UTC().Format(time.RFC3339), string(model.ChatAnalyzerMessagePending), messageDateBefore.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	updated, err := res.RowsAffected()
	return int(updated), err
}

func (s *SQLiteStore) SaveChatAnalyzerBatch(ctx context.Context, batch model.ChatAnalyzerBatch) error {
	status := strings.TrimSpace(string(batch.Status))
	if status == "" {
		status = string(model.ChatAnalyzerBatchRunning)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_analyzer_batches(
			id, status, dry_run, started_at, finished_at, message_count, report_count, vote_count,
			ignored_count, would_apply_count, applied_count, error_count, model, selected_model, result_json, last_error
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			dry_run = excluded.dry_run,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			message_count = excluded.message_count,
			report_count = excluded.report_count,
			vote_count = excluded.vote_count,
			ignored_count = excluded.ignored_count,
			would_apply_count = excluded.would_apply_count,
			applied_count = excluded.applied_count,
			error_count = excluded.error_count,
			model = excluded.model,
			selected_model = excluded.selected_model,
			result_json = excluded.result_json,
			last_error = excluded.last_error
	`, strings.TrimSpace(batch.ID), status, boolToInt(batch.DryRun), batch.StartedAt.UTC().Format(time.RFC3339), formatOptionalTime(batch.FinishedAt), batch.MessageCount, batch.ReportCount, batch.VoteCount, batch.IgnoredCount, batch.WouldApply, batch.AppliedCount, batch.ErrorCount, strings.TrimSpace(batch.Model), strings.TrimSpace(batch.SelectedModel), batch.ResultJSON, batch.Error)
	return err
}

func (s *SQLiteStore) FinalizeChatAnalyzerBatch(ctx context.Context, batch model.ChatAnalyzerBatch, outcomes []ChatAnalyzerMessageOutcome) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	for _, outcome := range outcomes {
		status := outcome.Status
		if status == "" {
			status = model.ChatAnalyzerMessageFailed
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE chat_analyzer_messages
			SET status = ?,
			    attempts = attempts + 1,
			    analysis_json = ?,
			    applied_action_id = ?,
			    applied_target_key = ?,
			    batch_id = ?,
			    last_error = ?,
			    processed_at = ?
			WHERE id = ?
		`, string(status), strings.TrimSpace(outcome.AnalysisJSON), strings.TrimSpace(outcome.AppliedActionID), strings.TrimSpace(outcome.AppliedTargetKey), strings.TrimSpace(outcome.BatchID), strings.TrimSpace(outcome.LastError), outcome.ProcessedAt.UTC().Format(time.RFC3339), strings.TrimSpace(outcome.ID))
		if err != nil {
			return rollback(err)
		}
		updated, err := res.RowsAffected()
		if err != nil {
			return rollback(err)
		}
		if updated != 1 {
			return rollback(fmt.Errorf("chat analyzer message outcome target was not found"))
		}
	}
	status := strings.TrimSpace(string(batch.Status))
	if status == "" {
		status = string(model.ChatAnalyzerBatchRunning)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_analyzer_batches(
			id, status, dry_run, started_at, finished_at, message_count, report_count, vote_count,
			ignored_count, would_apply_count, applied_count, error_count, model, selected_model, result_json, last_error
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			dry_run = excluded.dry_run,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			message_count = excluded.message_count,
			report_count = excluded.report_count,
			vote_count = excluded.vote_count,
			ignored_count = excluded.ignored_count,
			would_apply_count = excluded.would_apply_count,
			applied_count = excluded.applied_count,
			error_count = excluded.error_count,
			model = excluded.model,
			selected_model = excluded.selected_model,
			result_json = excluded.result_json,
			last_error = excluded.last_error
	`, strings.TrimSpace(batch.ID), status, boolToInt(batch.DryRun), batch.StartedAt.UTC().Format(time.RFC3339), formatOptionalTime(batch.FinishedAt), batch.MessageCount, batch.ReportCount, batch.VoteCount, batch.IgnoredCount, batch.WouldApply, batch.AppliedCount, batch.ErrorCount, strings.TrimSpace(batch.Model), strings.TrimSpace(batch.SelectedModel), batch.ResultJSON, batch.Error); err != nil {
		return rollback(err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) FailStaleChatAnalyzerBatches(ctx context.Context, startedBefore, finishedAt time.Time, reason string) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE chat_analyzer_batches
		SET status = ?,
		    finished_at = ?,
		    error_count = CASE WHEN error_count > 0 THEN error_count ELSE message_count END,
		    last_error = ?
		WHERE status = ? AND started_at <= ?
	`, string(model.ChatAnalyzerBatchFailed), finishedAt.UTC().Format(time.RFC3339), strings.TrimSpace(reason), string(model.ChatAnalyzerBatchRunning), startedBefore.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	updated, err := res.RowsAffected()
	return int(updated), err
}

func (s *SQLiteStore) CountChatAnalyzerMessagesBySenderSince(ctx context.Context, chatID string, senderID int64, since time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM chat_analyzer_messages
		WHERE chat_id = ? AND sender_id = ? AND received_at >= ?
	`, strings.TrimSpace(chatID), senderID, since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) CountChatAnalyzerAppliedByTargetSince(ctx context.Context, targetKey string, since time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM chat_analyzer_messages
		WHERE applied_target_key = ? AND status = ? AND processed_at >= ?
	`, strings.TrimSpace(targetKey), string(model.ChatAnalyzerMessageApplied), since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

type chatAnalyzerRowScanner interface {
	Scan(dest ...any) error
}

func scanChatAnalyzerMessage(row chatAnalyzerRowScanner) (model.ChatAnalyzerMessage, error) {
	var (
		item           model.ChatAnalyzerMessage
		messageDateRaw string
		receivedAtRaw  string
		statusRaw      string
		processedAtRaw string
	)
	if err := row.Scan(
		&item.ID,
		&item.ChatID,
		&item.MessageID,
		&item.SenderID,
		&item.SenderStableID,
		&item.SenderNickname,
		&item.Text,
		&messageDateRaw,
		&receivedAtRaw,
		&item.ReplyToMessageID,
		&statusRaw,
		&item.Attempts,
		&item.AnalysisJSON,
		&item.AppliedActionID,
		&item.AppliedTargetKey,
		&item.BatchID,
		&item.LastError,
		&processedAtRaw,
	); err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	messageDate, err := time.Parse(time.RFC3339, messageDateRaw)
	if err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	receivedAt, err := time.Parse(time.RFC3339, receivedAtRaw)
	if err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	processedAt, err := parseOptionalTime(processedAtRaw)
	if err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	item.Status = model.ChatAnalyzerMessageStatus(statusRaw)
	item.MessageDate = messageDate
	item.ReceivedAt = receivedAt
	item.ProcessedAt = processedAt
	return item, nil
}

func formatOptionalTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

func parseOptionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
