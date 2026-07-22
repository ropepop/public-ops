package main

import (
	"strings"
	"testing"
)

func TestValidateOnceSafety(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		productionStore           bool
		continuousAnalyzerEnabled bool
		dryRun                    bool
		acknowledgeDryRun         bool
		wantError                 string
	}{
		{
			name:            "isolated sqlite dry run remains allowed",
			productionStore: false,
			dryRun:          true,
		},
		{
			name:            "production live one-off remains allowed when continuous analyzer is stopped",
			productionStore: true,
			dryRun:          false,
		},
		{
			name:            "production dry run is refused without explicit acknowledgement",
			productionStore: true,
			dryRun:          true,
			wantError:       "-acknowledge-production-dry-run-consumes-messages",
		},
		{
			name:              "production dry run is allowed after explicit acknowledgement",
			productionStore:   true,
			dryRun:            true,
			acknowledgeDryRun: true,
		},
		{
			name:                      "continuous analyzer blocks production live one-off",
			productionStore:           true,
			continuousAnalyzerEnabled: true,
			dryRun:                    false,
			wantError:                 "SATIKSME_CHAT_ANALYZER_ENABLED=true",
		},
		{
			name:                      "continuous analyzer blocks acknowledged production dry run",
			productionStore:           true,
			continuousAnalyzerEnabled: true,
			dryRun:                    true,
			acknowledgeDryRun:         true,
			wantError:                 "SATIKSME_CHAT_ANALYZER_ENABLED=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateOnceSafety(
				tt.productionStore,
				tt.continuousAnalyzerEnabled,
				tt.dryRun,
				tt.acknowledgeDryRun,
			)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("validateOnceSafety() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateOnceSafety() error = nil, want error containing %q", tt.wantError)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("validateOnceSafety() error = %q, want substring %q", err, tt.wantError)
			}
		})
	}
}
