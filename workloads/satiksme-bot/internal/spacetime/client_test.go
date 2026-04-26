package spacetime

import (
	"testing"
)

func TestCanonicalProcedureName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "   ",
			want: "",
		},
		{
			name: "unprefixed",
			in:   "service_pending_report_dump_count",
			want: "satiksmebot_service_pending_report_dump_count",
		},
		{
			name: "prefixed",
			in:   "satiksmebot_service_pending_report_dump_count",
			want: "satiksmebot_service_pending_report_dump_count",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := canonicalProcedureName(tt.in); got != tt.want {
				t.Fatalf("canonicalProcedureName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
