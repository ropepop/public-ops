package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"telegramtrainapp/internal/spacetime"
)

// ReadSpacetimeView uses the backing state's aggregate read. The bool identifies
// the backend, so a failed remote read never falls back to per-train requests.
func ReadSpacetimeView(ctx context.Context, st Store, userID int64, procedure string, args []any) (map[string]any, bool, error) {
	switch st := st.(type) {
	case *RoutedStore:
		return ReadSpacetimeView(ctx, st.state, userID, procedure, args)
	case *SpacetimeStore:
		opts := spacetime.TokenOptions{}
		if userID > 0 {
			opts.Subject = "telegram:" + strconv.FormatInt(userID, 10)
			opts.Roles = []string{"train_user"}
		}
		token, err := st.client.IssueToken(time.Now().UTC(), opts)
		if err != nil {
			return nil, true, err
		}
		payload, err := st.client.CallProcedureWithToken(ctx, procedure, args, token)
		if err != nil {
			return nil, true, err
		}
		view, ok := payload.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("invalid spacetime view %s", procedure)
		}
		return view, true, nil
	default:
		return nil, false, nil
	}
}
