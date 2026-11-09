package likepolicy

import (
	"errors"
	"github.com/scut2024hjt/convo/models"
	"testing"
)

func TestLikePolicyNewAndLegacy(t *testing.T) {
	tests := []struct {
		name, kind     string
		direction      int8
		version        int64
		exists         bool
		current        int8
		currentVersion int64
		want           Status
		wantErr        error
	}{
		{"first_like", models.LikeChangedEventType, 1, 1, false, 0, 0, Applied, nil},
		{"stale_like", models.LikeChangedEventType, 1, 2, true, 0, 3, Stale, nil},
		{"later_unlike", models.LikeChangedEventType, 0, 3, true, 1, 2, Applied, nil},
		{"same", models.LikeChangedEventType, 0, 3, true, 0, 3, SameVersion, nil},
		{"conflict", models.LikeChangedEventType, 1, 3, true, 0, 3, "", ErrConflict},
		{"invalid_negative_new", models.LikeChangedEventType, -1, 2, true, 1, 1, "", nil},
		{"legacy_rejected_no_row", models.VoteChangedEventType, -1, 1, false, 0, 0, "", ErrLegacyAdvance},
		{"legacy_rejected_advance", models.VoteChangedEventType, 1, 4, true, 0, 3, "", ErrLegacyAdvance},
		{"legacy_stale", models.VoteChangedEventType, -1, 1, true, 1, 3, Stale, nil},
		{"legacy_same_normalized", models.VoteChangedEventType, -1, 3, true, 0, 3, SameVersion, nil},
		{"legacy_same_conflict", models.VoteChangedEventType, -1, 3, true, 1, 3, "", ErrConflict},
		{"legacy_current_bad", models.VoteChangedEventType, -1, 2, true, -1, 2, "", nil},
		{"too_big", models.LikeChangedEventType, 1, models.MaxLikeVersion + 1, false, 0, 0, "", nil},
		{"max_new", models.LikeChangedEventType, 1, models.MaxLikeVersion, false, 0, 0, Applied, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Classify(tc.kind, tc.direction, tc.version, tc.exists, tc.current, tc.currentVersion)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error %v want %v", err, tc.wantErr)
				}
			} else if tc.want == "" {
				if err == nil {
					t.Fatal("expected rejection")
				}
			} else if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != tc.want {
				t.Fatalf("status %q want %q", got, tc.want)
			}
		})
	}
}

func TestLegacyMigrationVersionsAndIdempotency(t *testing.T) {
	dir, v, changed, err := ConvertLegacyState(-1, 7)
	if err != nil || dir != 0 || v != 8 || !changed {
		t.Fatalf("-1:v7 => %d:v%d changed=%v err=%v", dir, v, changed, err)
	}
	dir, v, changed, err = ConvertLegacyState(dir, v)
	if err != nil || dir != 0 || v != 8 || changed {
		t.Fatalf("repeat migration => %d:v%d changed=%v err=%v", dir, v, changed, err)
	}
	for _, d := range []int8{0, 1} {
		nd, nv, ch, e := ConvertLegacyState(d, 3)
		if e != nil || nd != d || nv != 3 || ch {
			t.Fatalf("unchanged %d: %d %d %v %v", d, nd, nv, ch, e)
		}
	}
	for _, tc := range []struct {
		direction int8
		version   uint64
	}{{-1, uint64(models.MaxLikeVersion)}, {-1, 0}, {2, 1}, {-2, 1}, {1, uint64(models.MaxLikeVersion) + 1}} {
		if _, _, _, e := ConvertLegacyState(tc.direction, tc.version); e == nil {
			t.Fatalf("accepted invalid state %+v", tc)
		}
	}
}
