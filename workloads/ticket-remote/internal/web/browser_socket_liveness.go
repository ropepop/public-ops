package web

import (
	"context"
	"fmt"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/phone"
)

const (
	browserVideoLivenessIdle    = phone.DefaultLivenessIdle
	browserVideoLivenessTimeout = phone.DefaultLivenessTimeout
)

// browserVideoLivenessLoop verifies a quiet viewer without making its
// transport state part of global source health. Conn.Ping must run alongside
// the handler's Read so nhooyr can consume the matching Pong.
func browserVideoLivenessLoop(
	ctx context.Context,
	conn *websocket.Conn,
	activity <-chan struct{},
	idle time.Duration,
	timeout time.Duration,
) error {
	timer := time.NewTimer(idle)
	defer timer.Stop()
	reset := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idle)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-activity:
			reset()
		case <-timer.C:
			pingCtx, cancel := context.WithTimeout(ctx, timeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("browser video liveness: %w", err)
			}
			timer.Reset(idle)
		}
	}
}

func signalBrowserVideoActivity(activity chan<- struct{}) {
	select {
	case activity <- struct{}{}:
	default:
	}
}
