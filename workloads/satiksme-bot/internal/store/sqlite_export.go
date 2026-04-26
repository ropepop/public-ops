package store

import (
	"context"
	"database/sql"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/spacetime"
)

func ExportSQLiteStateSnapshot(ctx context.Context, path string, cutoff time.Time) (spacetime.StateSnapshot, error) {
	st, err := NewSQLiteStore(path)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return spacetime.StateSnapshot{}, err
	}

	stopSightings, err := st.ListStopSightingsSince(ctx, cutoff, "", 0)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}
	vehicleSightings, err := st.ListVehicleSightingsSince(ctx, cutoff, "", 0)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}
	incidentVotes, err := exportSQLiteIncidentVotes(ctx, st.db, cutoff)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}
	incidentVoteEvents, err := st.ListIncidentVoteEvents(ctx, "", cutoff, 0)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}
	incidentComments, err := exportSQLiteIncidentComments(ctx, st.db, cutoff)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}
	reportDumpItems, err := exportSQLiteReportDumpQueue(ctx, st.db)
	if err != nil {
		return spacetime.StateSnapshot{}, err
	}

	return spacetime.StateSnapshot{
		StopSightings:      stopSightings,
		VehicleSightings:   vehicleSightings,
		IncidentVotes:      incidentVotes,
		IncidentVoteEvents: incidentVoteEvents,
		IncidentComments:   incidentComments,
		ReportDumpItems:    reportDumpItems,
	}, nil
}

func exportSQLiteIncidentVotes(ctx context.Context, db *sql.DB, cutoff time.Time) ([]model.IncidentVote, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT incident_id, user_id, nickname, vote_value, created_at, updated_at
		FROM incident_votes
		WHERE updated_at >= ?
		ORDER BY updated_at DESC, user_id ASC
	`, cutoff.UTC().Format(time.RFC3339))
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
		createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
		if err != nil {
			return nil, err
		}
		updatedAt, err := time.Parse(time.RFC3339, updatedAtRaw)
		if err != nil {
			return nil, err
		}
		item.Value = model.IncidentVoteValue(valueRaw)
		item.CreatedAt = createdAt
		item.UpdatedAt = updatedAt
		out = append(out, item)
	}
	return out, rows.Err()
}

func exportSQLiteIncidentComments(ctx context.Context, db *sql.DB, cutoff time.Time) ([]model.IncidentComment, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, incident_id, user_id, nickname, body, created_at
		FROM incident_comments
		WHERE created_at >= ?
		ORDER BY created_at DESC, id DESC
	`, cutoff.UTC().Format(time.RFC3339))
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

func exportSQLiteReportDumpQueue(ctx context.Context, db *sql.DB) ([]spacetime.ReportDumpItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, payload, attempts, created_at, next_attempt_at, last_attempt_at, last_error
		FROM report_dump_queue
		ORDER BY next_attempt_at ASC, created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]spacetime.ReportDumpItem, 0)
	for rows.Next() {
		var item spacetime.ReportDumpItem
		if err := rows.Scan(&item.ID, &item.Payload, &item.Attempts, &item.CreatedAt, &item.NextAttemptAt, &item.LastAttemptAt, &item.LastError); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
