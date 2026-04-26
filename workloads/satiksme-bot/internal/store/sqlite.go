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
			(SELECT COUNT(*) FROM vehicle_sightings WHERE user_id = ? AND is_hidden = 0 AND created_at >= ?)
	`, userID, since.UTC().Format(time.RFC3339), userID, since.UTC().Format(time.RFC3339)).Scan(&count); err != nil {
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
	if _, err := s.db.ExecContext(ctx, `DELETE FROM incident_vote_events WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM report_dedupe_claims WHERE last_report_at < ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return result, err
	}
	result.StopSightingsDeleted, _ = stopRes.RowsAffected()
	result.VehicleSightingsDeleted, _ = vehicleRes.RowsAffected()
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
