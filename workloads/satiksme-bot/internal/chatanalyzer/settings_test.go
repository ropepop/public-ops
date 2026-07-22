package chatanalyzer

import "testing"

func TestSettingsDefaultModelBatchLimitIsFive(t *testing.T) {
	settings := (Settings{}).withDefaults()
	if got, want := settings.BatchLimit, 5; got != want {
		t.Fatalf("model batch limit = %d, want %d", got, want)
	}
}
