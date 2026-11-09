package models

const (
	VoteChangedEventType = "vote.changed.v1" // transport routing and legacy body type
	LikeChangedEventType = "like.changed.v1" // current body type
	// Lua numbers are IEEE-754 doubles. Never use an imprecise version.
	MaxLikeVersion int64 = 9007199254740991
)

type VoteEvent struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	UserID     string `json:"user_id"`
	PostID     string `json:"post_id"`
	Direction  int8   `json:"direction"`
	Version    int64  `json:"version"`
	OccurredAt int64  `json:"occurred_at"`
}

type VoteResult struct {
	Direction int8  `json:"direction"`
	Version   int64 `json:"version"`
	Changed   bool  `json:"changed"`
}

// LikeState is strictly per authenticated user and never placed in public caches.
type LikeState struct {
	PostID    string `json:"post_id"`
	Liked     bool   `json:"liked"`
	Version   string `json:"version"`
	Source    string `json:"source"`
	Freshness string `json:"freshness"`
	Code      int64  `json:"code,omitempty"`
}
