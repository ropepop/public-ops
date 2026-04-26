package ride

import (
	"context"
	"sync"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/store"
)

type UndoRecord struct {
	TrainID           string
	BoardingStationID *string
	CheckedInAt       time.Time
	AutoCheckoutAt    time.Time
	ExpiresAt         time.Time
}

type Service struct {
	store store.Store

	mu          sync.Mutex
	undoRecords map[int64]UndoRecord
}

func NewService(st store.Store) *Service {
	return &Service{
		store:       st,
		undoRecords: map[int64]UndoRecord{},
	}
}

func (s *Service) CheckIn(ctx context.Context, userID int64, trainID string, now, arrival time.Time) error {
	return s.CheckInUntil(ctx, userID, trainID, now, arrival.Add(10*time.Minute))
}

func (s *Service) CheckInAtStation(ctx context.Context, userID int64, trainID string, boardingStationID *string, now, arrival time.Time) error {
	return s.CheckInUntilAtStation(ctx, userID, trainID, boardingStationID, now, arrival.Add(10*time.Minute))
}

func (s *Service) CheckInUntil(ctx context.Context, userID int64, trainID string, now, autoCheckout time.Time) error {
	return s.CheckInUntilAtStation(ctx, userID, trainID, nil, now, autoCheckout)
}

func (s *Service) CheckInUntilAtStation(ctx context.Context, userID int64, trainID string, boardingStationID *string, now, autoCheckout time.Time) error {
	return s.store.CheckInUserAtStation(ctx, userID, trainID, boardingStationID, now, autoCheckout)
}

func (s *Service) ActiveCheckIn(ctx context.Context, userID int64, now time.Time) (*domain.CheckIn, error) {
	return s.store.GetActiveCheckIn(ctx, userID, now)
}

func (s *Service) Checkout(ctx context.Context, userID int64, now time.Time) error {
	active, err := s.store.GetActiveCheckIn(ctx, userID, now)
	if err != nil {
		return err
	}
	if active != nil {
		s.mu.Lock()
		s.undoRecords[userID] = UndoRecord{
			TrainID:           active.TrainInstanceID,
			BoardingStationID: active.BoardingStationID,
			CheckedInAt:       active.CheckedInAt,
			AutoCheckoutAt:    active.AutoCheckoutAt,
			ExpiresAt:         now.Add(10 * time.Second),
		}
		s.mu.Unlock()
	}
	return s.store.CheckoutUser(ctx, userID)
}

func (s *Service) UndoCheckout(ctx context.Context, userID int64, now time.Time) (bool, error) {
	s.mu.Lock()
	record, ok := s.undoRecords[userID]
	if !ok {
		s.mu.Unlock()
		return false, nil
	}
	if now.After(record.ExpiresAt) {
		delete(s.undoRecords, userID)
		s.mu.Unlock()
		return false, nil
	}
	delete(s.undoRecords, userID)
	s.mu.Unlock()
	if err := s.store.UndoCheckoutUser(ctx, userID, record.TrainID, record.BoardingStationID, record.CheckedInAt, record.AutoCheckoutAt); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) MuteForTrain(ctx context.Context, userID int64, trainID string, now time.Time, d time.Duration) error {
	return s.store.SetTrainMute(ctx, userID, trainID, now.Add(d))
}

func (s *Service) CleanupUndo(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for userID, rec := range s.undoRecords {
		if now.After(rec.ExpiresAt) {
			delete(s.undoRecords, userID)
		}
	}
}
