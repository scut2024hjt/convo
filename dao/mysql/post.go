package mysql

import (
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/models"
)

// CreatePost 创建帖子
func CreatePost(p *models.Post) (err error) {
	sqlStr := `insert into post (post_id, title, content, author_id, community_id) values (?, ?, ?, ?, ?)`
	_, err = db.Exec(sqlStr, p.ID, p.Title, p.Content, p.AuthorID, p.CommunityID)
	return
}

// GetPostById 根据 id 查询单个帖子详情数据
func GetPostById(pid int64) (post *models.Post, err error) {
	post = new(models.Post)
	sqlStr := `select post_id, title, content, author_id, community_id, status, create_time, update_time from post where post_id = ?`
	err = db.Get(post, sqlStr, pid)
	return
}

func GetPostDetailByID(postID int64) (*models.ApiPostDetail, error) {
	var row struct {
		PostID                int64     `db:"post_id"`
		AuthorID              int64     `db:"author_id"`
		CommunityID           int64     `db:"community_id"`
		Status                int32     `db:"status"`
		Title                 string    `db:"title"`
		Content               string    `db:"content"`
		PostCreateTime        time.Time `db:"post_create_time"`
		PostUpdateTime        time.Time `db:"post_update_time"`
		AuthorName            string    `db:"author_name"`
		CommunityName         string    `db:"community_name"`
		CommunityIntroduction string    `db:"community_introduction"`
		CommunityCreateTime   time.Time `db:"community_create_time"`
	}
	err := db.Get(&row, `
		SELECT p.post_id, p.author_id, p.community_id, p.status, p.title, p.content,
		       p.create_time AS post_create_time, p.update_time AS post_update_time,
		       u.username AS author_name,
		       c.community_name, c.introduction AS community_introduction,
		       c.create_time AS community_create_time
		FROM post p
		JOIN user u ON u.user_id = p.author_id
		JOIN community c ON c.community_id = p.community_id
		WHERE p.post_id = ? AND p.status = 1`, postID)
	if err != nil {
		return nil, err
	}
	return &models.ApiPostDetail{
		AuthorName: row.AuthorName,
		Post: &models.Post{
			ID: row.PostID, AuthorID: row.AuthorID, CommunityID: row.CommunityID,
			Status: row.Status, Title: row.Title, Content: row.Content,
			CreateTime: row.PostCreateTime, UpdateTime: row.PostUpdateTime,
		},
		CommunityDetail: &models.CommunityDetail{
			ID: row.CommunityID, Name: row.CommunityName,
			Introduction: row.CommunityIntroduction, CreateTime: row.CommunityCreateTime,
		},
	}, nil
}

func UpdatePost(postID, authorID int64, title, content string) (bool, error) {
	result, err := db.Exec(
		`UPDATE post SET title = ?, content = ? WHERE post_id = ? AND author_id = ? AND status = 1`,
		title, content, postID, authorID,
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	return rowsAffected > 0, err
}

func GetPostAuthorID(postID int64) (int64, error) {
	var authorID int64
	err := db.Get(&authorID, `SELECT author_id FROM post WHERE post_id = ? AND status = 1`, postID)
	return authorID, err
}

// GetPostList 查询帖子列表函数
func GetPostList(page, size int64) (posts []*models.Post, err error) {
	posts = make([]*models.Post, 0, size)
	sqlStr := `select post_id, title, content, author_id, community_id, status,
       create_time, update_time from post order by create_time desc limit ?,?`
	err = db.Select(&posts, sqlStr, (page-1)*size, size)
	return
}

// GetPostListByIds 根据给定的 id 列表查询帖子数据
func GetPostListByIds(ids []string) (postList []*models.Post, err error) {
	sqlStr := `select post_id, title, content, author_id, community_id, status, create_time, update_time from post where post_id in (?)
				order by FIND_IN_SET(post_id, ?)`
	query, args, err := sqlx.In(sqlStr, ids, strings.Join(ids, ","))
	if err != nil {
		return nil, err
	}
	query = db.Rebind(query)
	err = db.Select(&postList, query, args...)
	return
}
