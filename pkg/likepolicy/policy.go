// Package likepolicy classifies durable current and historical like events.
// It is deliberately independent from SQL and RabbitMQ so version fencing can
// be tested without network services.
package likepolicy

import (
	"errors"
	"fmt"
	"github.com/scut2024hjt/convo/models"
)

type Status string

const (
	Applied     Status = "persisted"
	SameVersion Status = "same_version"
	Stale       Status = "stale"
)

var ErrConflict = errors.New("like event conflicts with persisted version")
var ErrLegacyAdvance = errors.New("legacy vote cannot advance like version after migration")

func Classify(eventType string, direction int8, version int64, exists bool, currentDirection int8, currentVersion int64) (Status, error) {
	if version <= 0 || version > models.MaxLikeVersion {
		return "", fmt.Errorf("invalid like version %d", version)
	}
	if exists && (currentDirection != 0 && currentDirection != 1 || currentVersion <= 0 || currentVersion > models.MaxLikeVersion) {
		return "", errors.New("invalid stored like state")
	}
	switch eventType {
	case models.LikeChangedEventType:
		if direction != 0 && direction != 1 {
			return "", fmt.Errorf("invalid like direction %d", direction)
		}
	case models.VoteChangedEventType:
		if direction != -1 && direction != 0 && direction != 1 {
			return "", fmt.Errorf("invalid legacy vote direction %d", direction)
		}
		if direction == -1 {
			direction = 0
		}
		if !exists || version > currentVersion {
			return "", ErrLegacyAdvance
		}
	default:
		return "", fmt.Errorf("unknown like event type %q", eventType)
	}
	if !exists {
		return Applied, nil
	}
	if version < currentVersion {
		return Stale, nil
	}
	if version == currentVersion {
		if direction == currentDirection {
			return SameVersion, nil
		}
		return "", ErrConflict
	}
	return Applied, nil
}

// ConvertLegacyState is the pure component of a bounded MySQL maintenance
// batch. It preserves unchanged rows and increments each old negative row
// exactly once; persistence must condition the UPDATE on direction=-1.
func ConvertLegacyState(direction int8, version uint64) (nextDirection int8, nextVersion uint64, changed bool, err error) {
	if version == 0 || version > uint64(models.MaxLikeVersion) || direction < -1 || direction > 1 {
		return 0, 0, false, errors.New("invalid persisted vote state")
	}
	if direction != -1 {
		return direction, version, false, nil
	}
	if version == uint64(models.MaxLikeVersion) {
		return 0, 0, false, errors.New("like version increment overflows safe limit")
	}
	return 0, version + 1, true, nil
}
