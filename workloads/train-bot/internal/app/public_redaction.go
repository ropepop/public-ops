package app

import (
	"time"

	"telegramtrainapp/internal/domain"
)

type PublicTrainInstance struct {
	ID          string    `json:"id"`
	ServiceDate string    `json:"serviceDate"`
	FromStation string    `json:"fromStation"`
	ToStation   string    `json:"toStation"`
	DepartureAt time.Time `json:"departureAt"`
	ArrivalAt   time.Time `json:"arrivalAt"`
}

func PublicTrainInstanceFor(train domain.TrainInstance) PublicTrainInstance {
	return PublicTrainInstance{
		ID:          train.ID,
		ServiceDate: train.ServiceDate,
		FromStation: train.FromStation,
		ToStation:   train.ToStation,
		DepartureAt: train.DepartureAt,
		ArrivalAt:   train.ArrivalAt,
	}
}

func PublicRiderCount(raw int) int {
	return publicSmallGroupBucket(raw)
}

func PublicReporterCount(raw int) int {
	return publicSmallGroupBucket(raw)
}

func PublicTrainStatus(status domain.TrainStatus) domain.TrainStatus {
	status.UniqueReporters = PublicReporterCount(status.UniqueReporters)
	return status
}

func publicSmallGroupBucket(raw int) int {
	switch {
	case raw >= 10:
		return 10
	case raw >= 5:
		return 5
	case raw >= 2:
		return 2
	default:
		return 0
	}
}
