package app

import "testing"

func TestPublicRiderCountBucketsSmallGroups(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		raw  int
		want int
	}{
		{raw: -1, want: 0},
		{raw: 0, want: 0},
		{raw: 1, want: 0},
		{raw: 2, want: 2},
		{raw: 4, want: 2},
		{raw: 5, want: 5},
		{raw: 9, want: 5},
		{raw: 10, want: 10},
		{raw: 25, want: 10},
	}

	for _, tc := range testCases {
		if got := PublicRiderCount(tc.raw); got != tc.want {
			t.Fatalf("PublicRiderCount(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestPublicReporterCountBucketsSmallGroups(t *testing.T) {
	cases := []struct {
		raw  int
		want int
	}{
		{raw: 0, want: 0},
		{raw: 1, want: 0},
		{raw: 2, want: 2},
		{raw: 4, want: 2},
		{raw: 5, want: 5},
		{raw: 9, want: 5},
		{raw: 10, want: 10},
		{raw: 20, want: 10},
	}
	for _, tc := range cases {
		if got := PublicReporterCount(tc.raw); got != tc.want {
			t.Fatalf("PublicReporterCount(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
