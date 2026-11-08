package worker

import (
	"testing"
	"time"
)

func TestT01TaskBackoffIsBounded(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {6, 30 * time.Second}, {100, 30 * time.Second},
	}
	for _, c := range cases {
		if got := taskBackoff(c.attempt, 30*time.Second); got != c.want {
			t.Errorf("attempt %d: got %v, want %v", c.attempt, got, c.want)
		}
	}
}
