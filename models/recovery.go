package models

// PostRecoverySnapshot contains the MySQL fields required to rebuild the
// Redis post indexes. NetVote is the sum of the latest persisted directions.
type PostRecoverySnapshot struct {
	PostID      int64 `db:"post_id"`
	CommunityID int64 `db:"community_id"`
	CreateUnix  int64 `db:"create_unix"`
	NetVote     int64 `db:"net_vote"`
}

// VoteRecoverySnapshot contains the latest persisted state and version for a
// single user/post pair. Direction zero is retained as a version tombstone.
type VoteRecoverySnapshot struct {
	UserID    int64 `db:"user_id"`
	PostID    int64 `db:"post_id"`
	Direction int8  `db:"direction"`
	Version   int64 `db:"version"`
}
