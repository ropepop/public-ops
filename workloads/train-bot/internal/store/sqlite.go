package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"telegramtrainapp/internal/domain"

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
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		versions = append(versions, name)
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
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			version,
			time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("mark migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

func (s *SQLiteStore) UpsertTrainInstances(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM train_instances WHERE service_date = ?`, serviceDate); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO train_instances (
			id, service_date, from_station, to_station, departure_at, arrival_at, source_version
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range trains {
		if _, err := stmt.ExecContext(
			ctx,
			t.ID,
			serviceDate,
			t.FromStation,
			t.ToStation,
			t.DepartureAt.UTC().Format(time.RFC3339),
			t.ArrivalAt.UTC().Format(time.RFC3339),
			sourceVersion,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) UpsertTrainStops(ctx context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM train_stops
		WHERE train_instance_id IN (
			SELECT id FROM train_instances WHERE service_date = ?
		)
	`, serviceDate); err != nil {
		return err
	}

	for trainID, stops := range stopsByTrain {
		for _, stop := range stops {
			name := strings.TrimSpace(stop.StationName)
			if name == "" {
				continue
			}
			stationID := normalizeStationID(name)
			normalized := normalizeStationKey(name)
			var latitude any
			var longitude any
			if stop.Latitude != nil {
				latitude = *stop.Latitude
			}
			if stop.Longitude != nil {
				longitude = *stop.Longitude
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stations(id, name, normalized_key, latitude, longitude)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(normalized_key) DO UPDATE SET
					name = excluded.name,
					latitude = COALESCE(excluded.latitude, stations.latitude),
					longitude = COALESCE(excluded.longitude, stations.longitude)
			`, stationID, name, normalized, latitude, longitude); err != nil {
				return err
			}

			var arrival any
			var departure any
			if stop.ArrivalAt != nil {
				arrival = stop.ArrivalAt.UTC().Format(time.RFC3339)
			}
			if stop.DepartureAt != nil {
				departure = stop.DepartureAt.UTC().Format(time.RFC3339)
			}

			if _, err := tx.ExecContext(ctx, `
					INSERT INTO train_stops(train_instance_id, station_id, seq, arrival_at, departure_at)
					VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(train_instance_id, seq) DO UPDATE SET
						station_id = excluded.station_id,
						seq = excluded.seq,
						arrival_at = excluded.arrival_at,
						departure_at = excluded.departure_at
				`, trainID, stationID, stop.Seq, arrival, departure); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) ListTrainInstancesByDate(ctx context.Context, serviceDate string) ([]domain.TrainInstance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, service_date, from_station, to_station, departure_at, arrival_at, source_version
		FROM train_instances
		WHERE service_date = ?
		ORDER BY departure_at ASC
	`, serviceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrainRows(rows)
}

func (s *SQLiteStore) ListTrainInstancesByWindow(ctx context.Context, serviceDate string, start, end time.Time) ([]domain.TrainInstance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, service_date, from_station, to_station, departure_at, arrival_at, source_version
		FROM train_instances
		WHERE service_date = ?
		AND departure_at >= ? AND departure_at <= ?
		ORDER BY departure_at ASC
	`, serviceDate, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrainRows(rows)
}

func (s *SQLiteStore) ListStationWindowTrains(ctx context.Context, serviceDate string, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.service_date, t.from_station, t.to_station, t.departure_at, t.arrival_at, t.source_version,
		       ts.station_id, s.name,
		       COALESCE(ts.departure_at, ts.arrival_at, t.departure_at) AS pass_at
		FROM train_instances t
		INNER JOIN train_stops ts ON ts.train_instance_id = t.id
		INNER JOIN stations s ON s.id = ts.station_id
		WHERE t.service_date = ?
		  AND ts.station_id = ?
		  AND COALESCE(ts.departure_at, ts.arrival_at, t.departure_at) >= ?
		  AND COALESCE(ts.departure_at, ts.arrival_at, t.departure_at) <= ?
		ORDER BY pass_at ASC, t.departure_at ASC
	`, serviceDate, stationID, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.StationWindowTrain, 0)
	for rows.Next() {
		var (
			item            domain.StationWindowTrain
			departureAtText string
			arrivalAtText   string
			passAtText      string
		)
		if err := rows.Scan(
			&item.Train.ID,
			&item.Train.ServiceDate,
			&item.Train.FromStation,
			&item.Train.ToStation,
			&departureAtText,
			&arrivalAtText,
			&item.Train.SourceVersion,
			&item.StationID,
			&item.StationName,
			&passAtText,
		); err != nil {
			return nil, err
		}
		departureAt, err := time.Parse(time.RFC3339, departureAtText)
		if err != nil {
			return nil, err
		}
		arrivalAt, err := time.Parse(time.RFC3339, arrivalAtText)
		if err != nil {
			return nil, err
		}
		passAt, err := time.Parse(time.RFC3339, passAtText)
		if err != nil {
			return nil, err
		}
		item.Train.DepartureAt = departureAt
		item.Train.ArrivalAt = arrivalAt
		item.PassAt = passAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListRouteWindowTrains(ctx context.Context, serviceDate string, fromStationID string, toStationID string, start, end time.Time) ([]domain.RouteWindowTrain, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.service_date, t.from_station, t.to_station, t.departure_at, t.arrival_at, t.source_version,
		       s_from.id, s_from.name,
		       s_to.id, s_to.name,
		       COALESCE(ts_from.departure_at, ts_from.arrival_at, t.departure_at) AS from_pass_at,
		       COALESCE(ts_to.arrival_at, ts_to.departure_at, t.arrival_at) AS to_pass_at
		FROM train_instances t
		INNER JOIN train_stops ts_from ON ts_from.train_instance_id = t.id
		INNER JOIN train_stops ts_to ON ts_to.train_instance_id = t.id
		INNER JOIN stations s_from ON s_from.id = ts_from.station_id
		INNER JOIN stations s_to ON s_to.id = ts_to.station_id
		WHERE t.service_date = ?
		  AND ts_from.station_id = ?
		  AND ts_to.station_id = ?
		  AND ts_from.seq < ts_to.seq
		  AND COALESCE(ts_from.departure_at, ts_from.arrival_at, t.departure_at) >= ?
		  AND COALESCE(ts_from.departure_at, ts_from.arrival_at, t.departure_at) <= ?
		ORDER BY from_pass_at ASC, t.departure_at ASC
	`, serviceDate, fromStationID, toStationID, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.RouteWindowTrain, 0)
	for rows.Next() {
		var (
			item            domain.RouteWindowTrain
			departureAtText string
			arrivalAtText   string
			fromPassAtText  string
			toPassAtText    string
		)
		if err := rows.Scan(
			&item.Train.ID,
			&item.Train.ServiceDate,
			&item.Train.FromStation,
			&item.Train.ToStation,
			&departureAtText,
			&arrivalAtText,
			&item.Train.SourceVersion,
			&item.FromStationID,
			&item.FromStationName,
			&item.ToStationID,
			&item.ToStationName,
			&fromPassAtText,
			&toPassAtText,
		); err != nil {
			return nil, err
		}
		departureAt, err := time.Parse(time.RFC3339, departureAtText)
		if err != nil {
			return nil, err
		}
		arrivalAt, err := time.Parse(time.RFC3339, arrivalAtText)
		if err != nil {
			return nil, err
		}
		fromPassAt, err := time.Parse(time.RFC3339, fromPassAtText)
		if err != nil {
			return nil, err
		}
		toPassAt, err := time.Parse(time.RFC3339, toPassAtText)
		if err != nil {
			return nil, err
		}
		item.Train.DepartureAt = departureAt
		item.Train.ArrivalAt = arrivalAt
		item.FromPassAt = fromPassAt
		item.ToPassAt = toPassAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListStationsByDate(ctx context.Context, serviceDate string) ([]domain.Station, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.id, s.name, s.normalized_key, s.latitude, s.longitude
		FROM stations s
		INNER JOIN train_stops ts ON ts.station_id = s.id
		INNER JOIN train_instances t ON t.id = ts.train_instance_id
		WHERE t.service_date = ?
		ORDER BY s.name ASC
	`, serviceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Station, 0)
	for rows.Next() {
		st, err := scanStationRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListReachableDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.id, s.name, s.normalized_key, s.latitude, s.longitude
		FROM train_instances t
		INNER JOIN train_stops ts_from ON ts_from.train_instance_id = t.id
		INNER JOIN train_stops ts_to ON ts_to.train_instance_id = t.id
		INNER JOIN stations s ON s.id = ts_to.station_id
		WHERE t.service_date = ?
		  AND ts_from.station_id = ?
		  AND ts_to.seq > ts_from.seq
		ORDER BY s.name ASC
	`, serviceDate, fromStationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Station, 0)
	for rows.Next() {
		st, err := scanStationRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListTerminalDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.id, s.name, s.normalized_key, s.latitude, s.longitude
		FROM train_instances t
		INNER JOIN train_stops ts_from ON ts_from.train_instance_id = t.id
		INNER JOIN train_stops ts_last ON ts_last.train_instance_id = t.id
		INNER JOIN stations s ON s.id = ts_last.station_id
		WHERE t.service_date = ?
		  AND ts_from.station_id = ?
		  AND ts_last.seq = (
		    SELECT MAX(ts2.seq)
		    FROM train_stops ts2
		    WHERE ts2.train_instance_id = t.id
		      AND ts2.seq > ts_from.seq
		  )
		ORDER BY s.name ASC
	`, serviceDate, fromStationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Station, 0)
	for rows.Next() {
		st, err := scanStationRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetStationByID(ctx context.Context, stationID string) (*domain.Station, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, normalized_key, latitude, longitude
		FROM stations
		WHERE id = ?
		LIMIT 1
	`, stationID)
	st, err := scanStationRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &st, nil
}

func (s *SQLiteStore) ListTrainStops(ctx context.Context, trainID string) ([]domain.TrainStop, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts.train_instance_id, ts.station_id, s.name, ts.seq, ts.arrival_at, ts.departure_at, s.latitude, s.longitude
		FROM train_stops ts
		INNER JOIN stations s ON s.id = ts.station_id
		WHERE ts.train_instance_id = ?
		ORDER BY ts.seq ASC
	`, trainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.TrainStop, 0)
	for rows.Next() {
		var item domain.TrainStop
		var arrivalText sql.NullString
		var departureText sql.NullString
		var latitude sql.NullFloat64
		var longitude sql.NullFloat64
		if err := rows.Scan(
			&item.TrainInstanceID,
			&item.StationID,
			&item.StationName,
			&item.Seq,
			&arrivalText,
			&departureText,
			&latitude,
			&longitude,
		); err != nil {
			return nil, err
		}
		if arrivalText.Valid {
			arrivalAt, err := time.Parse(time.RFC3339, arrivalText.String)
			if err != nil {
				return nil, err
			}
			item.ArrivalAt = &arrivalAt
		}
		if departureText.Valid {
			departureAt, err := time.Parse(time.RFC3339, departureText.String)
			if err != nil {
				return nil, err
			}
			item.DepartureAt = &departureAt
		}
		if latitude.Valid {
			item.Latitude = &latitude.Float64
		}
		if longitude.Valid {
			item.Longitude = &longitude.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) TrainHasStops(ctx context.Context, trainID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM train_stops WHERE train_instance_id = ?
	`, trainID)
	var c int
	if err := row.Scan(&c); err != nil {
		return false, err
	}
	return c > 0, nil
}

func (s *SQLiteStore) GetTrainInstanceByID(ctx context.Context, id string) (*domain.TrainInstance, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, service_date, from_station, to_station, departure_at, arrival_at, source_version
		FROM train_instances WHERE id = ? LIMIT 1
	`, id)
	t, err := scanTrainRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *SQLiteStore) EnsureUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, alerts_enabled, global_station_sightings_enabled, alert_style, language, updated_at)
		VALUES (?, 1, 0, 'DETAILED', 'EN', ?)
		ON CONFLICT(user_id) DO NOTHING
	`, userID, now); err != nil {
		return domain.UserSettings{}, err
	}
	return s.GetUserSettings(ctx, userID)
}

func (s *SQLiteStore) GetUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT user_id, alerts_enabled, alert_style, language, updated_at
		FROM user_settings
		WHERE user_id = ?
	`, userID)
	var us domain.UserSettings
	var alerts int
	var style, lang, updated string
	if err := row.Scan(&us.UserID, &alerts, &style, &lang, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.EnsureUserSettings(ctx, userID)
		}
		return domain.UserSettings{}, err
	}
	us.AlertsEnabled = alerts == 1
	us.AlertStyle = parseAlertStyle(style)
	us.Language = parseLanguage(lang)
	t, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return domain.UserSettings{}, err
	}
	us.UpdatedAt = t
	return us, nil
}

func (s *SQLiteStore) HasUserSettings(ctx context.Context, userID int64) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM user_settings
		WHERE user_id = ?
	`, userID)
	var exists int
	if err := row.Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) SetAlertsEnabled(ctx context.Context, userID int64, enabled bool) error {
	if _, err := s.EnsureUserSettings(ctx, userID); err != nil {
		return err
	}
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_settings SET alerts_enabled = ?, updated_at = ? WHERE user_id = ?
	`, value, time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *SQLiteStore) SetAlertStyle(ctx context.Context, userID int64, style domain.AlertStyle) error {
	if _, err := s.EnsureUserSettings(ctx, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_settings SET alert_style = ?, updated_at = ? WHERE user_id = ?
	`, string(style), time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *SQLiteStore) ToggleAlertStyle(ctx context.Context, userID int64) (domain.AlertStyle, error) {
	settings, err := s.EnsureUserSettings(ctx, userID)
	if err != nil {
		return "", err
	}
	newStyle := domain.AlertStyleDetailed
	if settings.AlertStyle == domain.AlertStyleDetailed {
		newStyle = domain.AlertStyleDiscreet
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE user_settings SET alert_style = ?, updated_at = ? WHERE user_id = ?
	`, string(newStyle), time.Now().UTC().Format(time.RFC3339), userID)
	return newStyle, err
}

func (s *SQLiteStore) SetLanguage(ctx context.Context, userID int64, lang domain.Language) error {
	if _, err := s.EnsureUserSettings(ctx, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_settings SET language = ?, updated_at = ? WHERE user_id = ?
	`, string(lang), time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *SQLiteStore) ResetTestUser(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for _, statement := range []string{
		`DELETE FROM undo_checkouts WHERE user_id = ?`,
		`DELETE FROM checkins WHERE user_id = ?`,
		`DELETE FROM train_mutes WHERE user_id = ?`,
		`DELETE FROM subscriptions WHERE user_id = ?`,
		`DELETE FROM favorite_routes WHERE user_id = ?`,
		`DELETE FROM report_events WHERE user_id = ?`,
		`DELETE FROM station_sighting_events WHERE user_id = ?`,
		`DELETE FROM incident_votes WHERE user_id = ?`,
		`DELETE FROM incident_vote_events WHERE user_id = ?`,
		`DELETE FROM incident_comments WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
			return err
		}
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, alerts_enabled, global_station_sightings_enabled, alert_style, language, updated_at)
		VALUES (?, 1, 0, 'DETAILED', 'EN', ?)
		ON CONFLICT(user_id) DO UPDATE SET
			alerts_enabled = excluded.alerts_enabled,
			global_station_sightings_enabled = excluded.global_station_sightings_enabled,
			alert_style = excluded.alert_style,
			language = excluded.language,
			updated_at = excluded.updated_at
	`, userID, updatedAt); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *SQLiteStore) ConsumeTestLoginTicket(ctx context.Context, nonceHash string, userID int64, expiresAt time.Time) (bool, error) {
	cleanNonceHash := strings.TrimSpace(nonceHash)
	if cleanNonceHash == "" {
		return false, errors.New("test login ticket nonce hash is required")
	}
	if expiresAt.IsZero() {
		return false, errors.New("test login ticket expiry is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM test_login_tickets WHERE expires_at <= ?
	`, now.Format(time.RFC3339)); err != nil {
		return false, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO test_login_tickets (nonce_hash, user_id, expires_at, consumed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(nonce_hash) DO NOTHING
	`, cleanNonceHash, userID, expiresAt.UTC().Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	return rowsAffected > 0, nil
}

func (s *SQLiteStore) CheckInUser(ctx context.Context, userID int64, trainID string, checkedInAt, autoCheckoutAt time.Time) error {
	return s.CheckInUserAtStation(ctx, userID, trainID, nil, checkedInAt, autoCheckoutAt)
}

func (s *SQLiteStore) CheckInUserAtStation(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO checkins(user_id, train_instance_id, boarding_station_id, checked_in_at, auto_checkout_at, muted_until, is_active)
		VALUES (?, ?, ?, ?, ?, NULL, 1)
		ON CONFLICT(user_id) DO UPDATE SET
			train_instance_id = excluded.train_instance_id,
			boarding_station_id = excluded.boarding_station_id,
			checked_in_at = excluded.checked_in_at,
			auto_checkout_at = excluded.auto_checkout_at,
			muted_until = NULL,
			is_active = 1
	`, userID, trainID, boardingStationID, checkedInAt.UTC().Format(time.RFC3339), autoCheckoutAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) GetActiveCheckIn(ctx context.Context, userID int64, now time.Time) (*domain.CheckIn, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT user_id, train_instance_id, boarding_station_id, checked_in_at, auto_checkout_at, muted_until, is_active
		FROM checkins
		WHERE user_id = ? AND is_active = 1 AND auto_checkout_at >= ?
		LIMIT 1
	`, userID, now.UTC().Format(time.RFC3339))
	var c domain.CheckIn
	var checkedIn, autoCheckout string
	var boarding sql.NullString
	var muted sql.NullString
	var isActive int
	if err := row.Scan(&c.UserID, &c.TrainInstanceID, &boarding, &checkedIn, &autoCheckout, &muted, &isActive); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	checkedInAt, err := time.Parse(time.RFC3339, checkedIn)
	if err != nil {
		return nil, err
	}
	autoCheckoutAt, err := time.Parse(time.RFC3339, autoCheckout)
	if err != nil {
		return nil, err
	}
	c.CheckedInAt = checkedInAt
	c.AutoCheckoutAt = autoCheckoutAt
	c.IsActive = isActive == 1
	if boarding.Valid && boarding.String != "" {
		c.BoardingStationID = &boarding.String
	}
	if muted.Valid && muted.String != "" {
		t, err := time.Parse(time.RFC3339, muted.String)
		if err != nil {
			return nil, err
		}
		c.MutedUntil = &t
	}
	return &c, nil
}

func (s *SQLiteStore) CheckoutUser(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE checkins SET is_active = 0 WHERE user_id = ?`, userID)
	return err
}

func (s *SQLiteStore) UndoCheckoutUser(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO checkins(user_id, train_instance_id, boarding_station_id, checked_in_at, auto_checkout_at, muted_until, is_active)
		VALUES (?, ?, ?, ?, ?, NULL, 1)
		ON CONFLICT(user_id) DO UPDATE SET
			train_instance_id = excluded.train_instance_id,
			boarding_station_id = excluded.boarding_station_id,
			checked_in_at = excluded.checked_in_at,
			auto_checkout_at = excluded.auto_checkout_at,
			is_active = 1
	`, userID, trainID, boardingStationID, checkedInAt.UTC().Format(time.RFC3339), autoCheckoutAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) SetTrainMute(ctx context.Context, userID int64, trainID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO train_mutes(user_id, train_instance_id, muted_until, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, train_instance_id) DO UPDATE SET
			muted_until = excluded.muted_until
	`, userID, trainID, until.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) IsTrainMuted(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM train_mutes
		WHERE user_id = ? AND train_instance_id = ? AND muted_until >= ?
	`, userID, trainID, now.UTC().Format(time.RFC3339))
	var c int
	if err := row.Scan(&c); err != nil {
		return false, err
	}
	return c > 0, nil
}

func (s *SQLiteStore) CountActiveCheckins(ctx context.Context, trainID string, now time.Time) (int, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM checkins
		WHERE train_instance_id = ? AND is_active = 1 AND auto_checkout_at >= ?
	`, trainID, now.UTC().Format(time.RFC3339))
	var c int
	if err := row.Scan(&c); err != nil {
		return 0, err
	}
	return c, nil
}

func (s *SQLiteStore) ListActiveCheckinUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id FROM checkins
		WHERE train_instance_id = ? AND is_active = 1 AND auto_checkout_at >= ?
	`, trainID, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		users = append(users, userID)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpsertSubscription(ctx context.Context, userID int64, trainID string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions(user_id, train_instance_id, expires_at, is_active)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(user_id, train_instance_id) DO UPDATE SET
			expires_at = excluded.expires_at,
			is_active = 1
	`, userID, trainID, expiresAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) DeactivateSubscription(ctx context.Context, userID int64, trainID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET is_active = 0
		WHERE user_id = ? AND train_instance_id = ?
	`, userID, trainID)
	return err
}

func (s *SQLiteStore) HasActiveSubscription(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM subscriptions
		WHERE user_id = ? AND train_instance_id = ? AND is_active = 1 AND expires_at >= ?
	`, userID, trainID, now.UTC().Format(time.RFC3339))
	var c int
	if err := row.Scan(&c); err != nil {
		return false, err
	}
	return c > 0, nil
}

func (s *SQLiteStore) ListActiveSubscriptionUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id FROM subscriptions
		WHERE train_instance_id = ? AND is_active = 1 AND expires_at >= ?
	`, trainID, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		users = append(users, userID)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpsertFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO favorite_routes(user_id, from_station_id, to_station_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, from_station_id, to_station_id) DO UPDATE SET
			created_at = excluded.created_at
	`, userID, fromStationID, toStationID, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) DeleteFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM favorite_routes
		WHERE user_id = ? AND from_station_id = ? AND to_station_id = ?
	`, userID, fromStationID, toStationID)
	return err
}

func (s *SQLiteStore) ListFavoriteRoutes(ctx context.Context, userID int64) ([]domain.FavoriteRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fr.user_id,
		       fr.from_station_id, s_from.name,
		       fr.to_station_id, s_to.name,
		       fr.created_at
		FROM favorite_routes fr
		LEFT JOIN stations s_from ON s_from.id = fr.from_station_id
		LEFT JOIN stations s_to ON s_to.id = fr.to_station_id
		WHERE fr.user_id = ?
		ORDER BY fr.created_at DESC, fr.from_station_id ASC, fr.to_station_id ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.FavoriteRoute, 0)
	for rows.Next() {
		var (
			item      domain.FavoriteRoute
			createdAt string
			fromName  sql.NullString
			toName    sql.NullString
		)
		if err := rows.Scan(&item.UserID, &item.FromStationID, &fromName, &item.ToStationID, &toName, &createdAt); err != nil {
			return nil, err
		}
		if fromName.Valid {
			item.FromStationName = fromName.String
		}
		if toName.Valid {
			item.ToStationName = toName.String
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = t
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListAllFavoriteRoutes(ctx context.Context) ([]domain.FavoriteRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fr.user_id,
		       fr.from_station_id, s_from.name,
		       fr.to_station_id, s_to.name,
		       fr.created_at
		FROM favorite_routes fr
		LEFT JOIN stations s_from ON s_from.id = fr.from_station_id
		LEFT JOIN stations s_to ON s_to.id = fr.to_station_id
		ORDER BY fr.created_at DESC, fr.user_id ASC, fr.from_station_id ASC, fr.to_station_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.FavoriteRoute, 0)
	for rows.Next() {
		var (
			item      domain.FavoriteRoute
			createdAt string
			fromName  sql.NullString
			toName    sql.NullString
		)
		if err := rows.Scan(&item.UserID, &item.FromStationID, &fromName, &item.ToStationID, &toName, &createdAt); err != nil {
			return nil, err
		}
		if fromName.Valid {
			item.FromStationName = fromName.String
		}
		if toName.Valid {
			item.ToStationName = toName.String
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = t
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) InsertReportEvent(ctx context.Context, e domain.ReportEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO report_events(id, train_instance_id, user_id, signal, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, e.ID, e.TrainInstanceID, e.UserID, string(e.Signal), e.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) GetLastReportByUserTrain(ctx context.Context, userID int64, trainID string) (*domain.ReportEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, train_instance_id, user_id, signal, created_at
		FROM report_events
		WHERE user_id = ? AND train_instance_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, userID, trainID)
	var e domain.ReportEvent
	var signal, created string
	if err := row.Scan(&e.ID, &e.TrainInstanceID, &e.UserID, &signal, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = createdAt
	e.Signal = domain.SignalType(signal)
	return &e, nil
}

func (s *SQLiteStore) ListReportsSince(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.ReportEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, train_instance_id, user_id, signal, created_at
		FROM report_events
		WHERE train_instance_id = ? AND created_at >= ?
		ORDER BY created_at DESC
		LIMIT ?
	`, trainID, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReportRows(rows)
}

func (s *SQLiteStore) ListRecentReports(ctx context.Context, trainID string, limit int) ([]domain.ReportEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, train_instance_id, user_id, signal, created_at
		FROM report_events
		WHERE train_instance_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, trainID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReportRows(rows)
}

func (s *SQLiteStore) ListRecentReportEvents(ctx context.Context, since time.Time, limit int) ([]domain.ReportEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, train_instance_id, user_id, signal, created_at
		FROM report_events
		WHERE created_at >= ?
		ORDER BY created_at DESC
		LIMIT ?
	`, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReportRows(rows)
}

func (s *SQLiteStore) InsertStationSighting(ctx context.Context, e domain.StationSighting) error {
	var destinationStationID any
	var matchedTrainID any
	if e.DestinationStationID != nil {
		destinationStationID = strings.TrimSpace(*e.DestinationStationID)
	}
	if e.MatchedTrainInstanceID != nil {
		matchedTrainID = strings.TrimSpace(*e.MatchedTrainInstanceID)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO station_sighting_events(id, station_id, destination_station_id, matched_train_instance_id, user_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.ID, e.StationID, destinationStationID, matchedTrainID, e.UserID, e.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) GetLastStationSightingByUserScope(ctx context.Context, userID int64, stationID string, destinationStationID *string) (*domain.StationSighting, error) {
	var (
		row  *sql.Row
		dest any
	)
	if destinationStationID != nil && strings.TrimSpace(*destinationStationID) != "" {
		dest = strings.TrimSpace(*destinationStationID)
		row = s.db.QueryRowContext(ctx, `
			SELECT sse.id, sse.station_id, ss.name, sse.destination_station_id, ds.name, sse.matched_train_instance_id, sse.user_id, sse.created_at
			FROM station_sighting_events sse
			INNER JOIN stations ss ON ss.id = sse.station_id
			LEFT JOIN stations ds ON ds.id = sse.destination_station_id
			WHERE sse.user_id = ? AND sse.station_id = ? AND sse.destination_station_id = ?
			ORDER BY sse.created_at DESC
			LIMIT 1
		`, userID, stationID, dest)
	} else {
		row = s.db.QueryRowContext(ctx, `
			SELECT sse.id, sse.station_id, ss.name, sse.destination_station_id, ds.name, sse.matched_train_instance_id, sse.user_id, sse.created_at
			FROM station_sighting_events sse
			INNER JOIN stations ss ON ss.id = sse.station_id
			LEFT JOIN stations ds ON ds.id = sse.destination_station_id
			WHERE sse.user_id = ? AND sse.station_id = ? AND sse.destination_station_id IS NULL
			ORDER BY sse.created_at DESC
			LIMIT 1
		`, userID, stationID)
	}
	item, err := scanStationSightingRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *SQLiteStore) ListRecentStationSightingsByStation(ctx context.Context, stationID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sse.id, sse.station_id, ss.name, sse.destination_station_id, ds.name, sse.matched_train_instance_id, sse.user_id, sse.created_at
		FROM station_sighting_events sse
		INNER JOIN stations ss ON ss.id = sse.station_id
		LEFT JOIN stations ds ON ds.id = sse.destination_station_id
		WHERE sse.station_id = ? AND sse.created_at >= ?
		ORDER BY sse.created_at DESC
		LIMIT ?
	`, stationID, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStationSightingRows(rows)
}

func (s *SQLiteStore) ListRecentStationSightings(ctx context.Context, since time.Time, limit int) ([]domain.StationSighting, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sse.id, sse.station_id, ss.name, sse.destination_station_id, ds.name, sse.matched_train_instance_id, sse.user_id, sse.created_at
		FROM station_sighting_events sse
		INNER JOIN stations ss ON ss.id = sse.station_id
		LEFT JOIN stations ds ON ds.id = sse.destination_station_id
		WHERE sse.created_at >= ?
		ORDER BY sse.created_at DESC
		LIMIT ?
	`, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStationSightingRows(rows)
}

func (s *SQLiteStore) ListRecentStationSightingsByTrain(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sse.id, sse.station_id, ss.name, sse.destination_station_id, ds.name, sse.matched_train_instance_id, sse.user_id, sse.created_at
		FROM station_sighting_events sse
		INNER JOIN stations ss ON ss.id = sse.station_id
		LEFT JOIN stations ds ON ds.id = sse.destination_station_id
		WHERE sse.matched_train_instance_id = ? AND sse.created_at >= ?
		ORDER BY sse.created_at DESC
		LIMIT ?
	`, trainID, since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStationSightingRows(rows)
}

func (s *SQLiteStore) UpsertIncidentVote(ctx context.Context, vote domain.IncidentVote) error {
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

func (s *SQLiteStore) InsertIncidentVoteEvent(ctx context.Context, vote domain.IncidentVoteEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_vote_events(id, incident_id, user_id, nickname, vote_value, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, vote.ID, vote.IncidentID, vote.UserID, strings.TrimSpace(vote.Nickname), string(vote.Value), vote.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) ListIncidentVotes(ctx context.Context, incidentID string) ([]domain.IncidentVote, error) {
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
	out := make([]domain.IncidentVote, 0)
	for rows.Next() {
		var (
			item         domain.IncidentVote
			valueRaw     string
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(&item.IncidentID, &item.UserID, &item.Nickname, &valueRaw, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, err
		}
		item.Value = domain.IncidentVoteValue(valueRaw)
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

func (s *SQLiteStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]domain.IncidentVoteEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_id, user_id, nickname, vote_value, created_at
		FROM incident_vote_events
		WHERE incident_id = ? AND created_at >= ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, strings.TrimSpace(incidentID), since.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.IncidentVoteEvent, 0)
	for rows.Next() {
		var (
			item         domain.IncidentVoteEvent
			valueRaw     string
			createdAtRaw string
		)
		if err := rows.Scan(&item.ID, &item.IncidentID, &item.UserID, &item.Nickname, &valueRaw, &createdAtRaw); err != nil {
			return nil, err
		}
		item.Value = domain.IncidentVoteValue(valueRaw)
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) InsertIncidentComment(ctx context.Context, comment domain.IncidentComment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_comments(id, incident_id, user_id, nickname, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, comment.ID, comment.IncidentID, comment.UserID, strings.TrimSpace(comment.Nickname), strings.TrimSpace(comment.Body), comment.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]domain.IncidentComment, error) {
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
	out := make([]domain.IncidentComment, 0)
	for rows.Next() {
		var (
			item         domain.IncidentComment
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

func (s *SQLiteStore) CleanupExpired(ctx context.Context, now time.Time, retention time.Duration, loc *time.Location) (CleanupResult, error) {
	if loc == nil {
		loc = time.UTC
	}
	cutoff := now.Add(-retention)
	oldestKeptServiceDate := now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	_, _ = s.db.ExecContext(ctx, `UPDATE checkins SET is_active = 0 WHERE is_active = 1 AND auto_checkout_at < ?`, now.UTC().Format(time.RFC3339))
	_, _ = s.db.ExecContext(ctx, `UPDATE subscriptions SET is_active = 0 WHERE is_active = 1 AND expires_at < ?`, now.UTC().Format(time.RFC3339))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM train_mutes WHERE muted_until < ?`, now.UTC().Format(time.RFC3339))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM test_login_tickets WHERE expires_at <= ?`, now.UTC().Format(time.RFC3339))

	resCheckins, err := s.db.ExecContext(ctx, `DELETE FROM checkins WHERE auto_checkout_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return CleanupResult{}, err
	}
	resSubs, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE expires_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return CleanupResult{}, err
	}
	resReports, err := s.db.ExecContext(ctx, `DELETE FROM report_events WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return CleanupResult{}, err
	}
	resStationSightings, err := s.db.ExecContext(ctx, `DELETE FROM station_sighting_events WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return CleanupResult{}, err
	}
	resTrains, err := s.db.ExecContext(ctx, `DELETE FROM train_instances WHERE service_date < ?`, oldestKeptServiceDate)
	if err != nil {
		return CleanupResult{}, err
	}
	resTrainStops, err := s.db.ExecContext(ctx, `
		DELETE FROM train_stops
		WHERE NOT EXISTS (
			SELECT 1 FROM train_instances t
			WHERE t.id = train_stops.train_instance_id
		)
	`)
	if err != nil {
		return CleanupResult{}, err
	}
	checkinsDeleted, _ := resCheckins.RowsAffected()
	subDeleted, _ := resSubs.RowsAffected()
	reportsDeleted, _ := resReports.RowsAffected()
	stationSightingsDeleted, _ := resStationSightings.RowsAffected()
	trainStopsDeleted, _ := resTrainStops.RowsAffected()
	trainsDeleted, _ := resTrains.RowsAffected()

	return CleanupResult{
		CheckinsDeleted:         checkinsDeleted,
		SubscriptionsDeleted:    subDeleted,
		ReportsDeleted:          reportsDeleted,
		StationSightingsDeleted: stationSightingsDeleted,
		TrainStopsDeleted:       trainStopsDeleted,
		TrainsDeleted:           trainsDeleted,
	}, nil
}

func (s *SQLiteStore) DeleteTrainDataByServiceDate(ctx context.Context, serviceDate string) (CleanupResult, error) {
	serviceDate = strings.TrimSpace(serviceDate)
	if serviceDate == "" {
		return CleanupResult{}, nil
	}
	resTrains, err := s.db.ExecContext(ctx, `DELETE FROM train_instances WHERE service_date = ?`, serviceDate)
	if err != nil {
		return CleanupResult{}, err
	}
	resTrainStops, err := s.db.ExecContext(ctx, `
		DELETE FROM train_stops
		WHERE NOT EXISTS (
			SELECT 1 FROM train_instances t
			WHERE t.id = train_stops.train_instance_id
		)
	`)
	if err != nil {
		return CleanupResult{}, err
	}
	trainsDeleted, _ := resTrains.RowsAffected()
	trainStopsDeleted, _ := resTrainStops.RowsAffected()
	return CleanupResult{
		TrainStopsDeleted: trainStopsDeleted,
		TrainsDeleted:     trainsDeleted,
	}, nil
}

func (s *SQLiteStore) UpsertDailyMetric(ctx context.Context, metricDate string, key string, value int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO daily_metrics(metric_date, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT(metric_date, key) DO UPDATE SET value = excluded.value
	`, metricDate, key, value)
	return err
}

func scanTrainRows(rows *sql.Rows) ([]domain.TrainInstance, error) {
	out := make([]domain.TrainInstance, 0)
	for rows.Next() {
		var (
			t               domain.TrainInstance
			departureAtText string
			arrivalAtText   string
		)
		if err := rows.Scan(
			&t.ID,
			&t.ServiceDate,
			&t.FromStation,
			&t.ToStation,
			&departureAtText,
			&arrivalAtText,
			&t.SourceVersion,
		); err != nil {
			return nil, err
		}
		departureAt, err := time.Parse(time.RFC3339, departureAtText)
		if err != nil {
			return nil, err
		}
		arrivalAt, err := time.Parse(time.RFC3339, arrivalAtText)
		if err != nil {
			return nil, err
		}
		t.DepartureAt = departureAt
		t.ArrivalAt = arrivalAt
		out = append(out, t)
	}
	return out, rows.Err()
}

type trainRowScanner interface {
	Scan(dest ...any) error
}

func scanTrainRow(row trainRowScanner) (domain.TrainInstance, error) {
	var (
		t               domain.TrainInstance
		departureAtText string
		arrivalAtText   string
	)
	if err := row.Scan(
		&t.ID,
		&t.ServiceDate,
		&t.FromStation,
		&t.ToStation,
		&departureAtText,
		&arrivalAtText,
		&t.SourceVersion,
	); err != nil {
		return domain.TrainInstance{}, err
	}
	departureAt, err := time.Parse(time.RFC3339, departureAtText)
	if err != nil {
		return domain.TrainInstance{}, err
	}
	arrivalAt, err := time.Parse(time.RFC3339, arrivalAtText)
	if err != nil {
		return domain.TrainInstance{}, err
	}
	t.DepartureAt = departureAt
	t.ArrivalAt = arrivalAt
	return t, nil
}

func scanStationRow(row trainRowScanner) (domain.Station, error) {
	var (
		item      domain.Station
		latitude  sql.NullFloat64
		longitude sql.NullFloat64
	)
	if err := row.Scan(&item.ID, &item.Name, &item.NormalizedKey, &latitude, &longitude); err != nil {
		return domain.Station{}, err
	}
	if latitude.Valid {
		item.Latitude = &latitude.Float64
	}
	if longitude.Valid {
		item.Longitude = &longitude.Float64
	}
	return item, nil
}

func scanReportRows(rows *sql.Rows) ([]domain.ReportEvent, error) {
	out := make([]domain.ReportEvent, 0)
	for rows.Next() {
		var e domain.ReportEvent
		var signal, created string
		if err := rows.Scan(&e.ID, &e.TrainInstanceID, &e.UserID, &signal, &created); err != nil {
			return nil, err
		}
		e.Signal = domain.SignalType(signal)
		createdAt, err := time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, err
		}
		e.CreatedAt = createdAt
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanStationSightingRows(rows *sql.Rows) ([]domain.StationSighting, error) {
	out := make([]domain.StationSighting, 0)
	for rows.Next() {
		item, err := scanStationSightingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanStationSightingRow(row trainRowScanner) (domain.StationSighting, error) {
	var (
		item                   domain.StationSighting
		destinationStationID   sql.NullString
		destinationStationName sql.NullString
		matchedTrainID         sql.NullString
		createdAt              string
	)
	if err := row.Scan(
		&item.ID,
		&item.StationID,
		&item.StationName,
		&destinationStationID,
		&destinationStationName,
		&matchedTrainID,
		&item.UserID,
		&createdAt,
	); err != nil {
		return domain.StationSighting{}, err
	}
	if destinationStationID.Valid {
		item.DestinationStationID = &destinationStationID.String
	}
	if destinationStationName.Valid {
		item.DestinationStationName = destinationStationName.String
	}
	if matchedTrainID.Valid {
		item.MatchedTrainInstanceID = &matchedTrainID.String
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return domain.StationSighting{}, err
	}
	item.CreatedAt = parsedCreatedAt
	return item, nil
}

func parseAlertStyle(v string) domain.AlertStyle {
	if v == string(domain.AlertStyleDiscreet) {
		return domain.AlertStyleDiscreet
	}
	return domain.AlertStyleDetailed
}

func parseLanguage(v string) domain.Language {
	if v == string(domain.LanguageLV) {
		return domain.LanguageLV
	}
	return domain.LanguageEN
}

func normalizeStationKey(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func normalizeStationID(name string) string {
	key := normalizeStationKey(name)
	return strings.ReplaceAll(key, " ", "_")
}
