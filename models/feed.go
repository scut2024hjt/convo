package models

import "time"

// FeedItem is a read model: IDs are strings so JavaScript never rounds them.
type FeedItem struct {
	ID         string        `json:"id"`
	AuthorID   string        `json:"author_id"`
	AuthorName string        `json:"author_name"`
	Title      string        `json:"title"`
	CreateTime time.Time     `json:"create_time"`
	Community  FeedCommunity `json:"community"`
}

type FeedCommunity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type FeedPage struct {
	Items      []FeedItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}
