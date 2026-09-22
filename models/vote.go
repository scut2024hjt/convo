package models

const VoteChangedEventType = "vote.changed.v1"

type VoteEvent struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	UserID     string `json:"user_id"`
	PostID     string `json:"post_id"`
	Direction  int8  `json:"direction"`
	Version    int64 `json:"version"`
	OccurredAt int64 `json:"occurred_at"`
}

type VoteResult struct {
	Direction int8  `json:"direction"`
	Version   int64 `json:"version"`
	Changed   bool  `json:"changed"`
}
