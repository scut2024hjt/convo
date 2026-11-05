package redis

import (
	"fmt"
	"strconv"
	"time"

	"github.com/scut2024hjt/convo/observability"
)

// SampleOutbox runs only in a background ticker, never in the vote request
// path. XDEL removes published messages, so XLEN counts unforwarded entries;
// XPENDING is a subset, not another queue to add to XLEN.
func SampleOutbox() error {
	stream := getRedisKey(KeyVoteOutboxStream)
	length, err := client.XLen(stream).Result()
	if err != nil {
		observability.RecordOutboxSample(0, 0, 0, err)
		return err
	}
	var age float64
	if length > 0 {
		first, rangeErr := client.XRangeN(stream, "-", "+", 1).Result()
		if rangeErr != nil || len(first) == 0 {
			if rangeErr == nil {
				rangeErr = fmt.Errorf("nonempty outbox missing first entry")
			}
			observability.RecordOutboxSample(0, 0, 0, rangeErr)
			return rangeErr
		}
		value, ok := first[0].Values["occurred_at"]
		if !ok {
			err = fmt.Errorf("oldest outbox event lacks occurred_at")
		} else {
			occurred, parseErr := strconv.ParseInt(fmt.Sprint(value), 10, 64)
			if parseErr != nil {
				err = parseErr
			} else {
				age = time.Since(time.Unix(occurred, 0)).Seconds()
			}
		}
		if err != nil {
			observability.RecordOutboxSample(0, 0, 0, err)
			return err
		}
		if age < 0 {
			age = 0
		}
	}
	// The relay may not have created the group yet; do not hide that as zero.
	pending, err := client.XPending(stream, VoteOutboxGroup).Result()
	if err != nil {
		observability.RecordOutboxSample(0, 0, 0, err)
		return err
	}
	observability.RecordOutboxSample(float64(length), age, float64(pending.Count), nil)
	return nil
}
