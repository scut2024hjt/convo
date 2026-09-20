package mysql

import (
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/encrypt"
	"database/sql"

	"github.com/jmoiron/sqlx"
)

// CheckUserExit 把每一步数据库操作封装成函数
// 待 logic 层根据业务需求调用
// CheckUserExit 检查指定用户名的用户是否存在
func CheckUserExit(username string) (err error) {
	sqlStr := `select count(user_id) from user where username = ?`
	var count int
	if err := db.Get(&count, sqlStr, username); err != nil {
		return err
	}
	if count > 0 {
		return ErrorUserExist
	}
	return
}

// InsertUser 向数据库中插入一条新的用户记录
func InsertUser(user *models.User) (err error) {
	// 执行 SQL 语句入库
	sqlStr := "insert into user(user_id, username, password) values(?, ?, ?)"
	_, err = db.Exec(sqlStr, user.UserID, user.Username, user.Password)
	return
}

func Login(user *models.User) (err error) {
	opassword := user.Password // 用户登录的密码
	sqlStr := `select user_id, username, password from user where username = ?`
	err = db.Get(user, sqlStr, user.Username)
	if err == sql.ErrNoRows {
		return ErrorUserNotExist
	}
	if err != nil {
		// 查询数据库失败
		return err
	}
	// 判断密码是否正确
	password := encrypt.EncryptPassword(opassword)
	if password != user.Password {
		return ErrorInvalidPassword
	}
	return
}

func GetUserById(uid int64) (user *models.User, err error) {
	user = new(models.User)
	sqlStr := `select user_id, username from user where user_id = ?`
	err = db.Get(user, sqlStr, uid)
	return
}

// GetUsersByIDs 根据用户 ID 列表批量查询用户信息
func GetUsersByIDs(uids []int64) (users []*models.User, err error) {
	users = make([]*models.User, 0, len(uids))
	if len(uids) == 0 {
		return
	}

	// 去重后再查，避免重复 ID 放大 IN 列表长度。
	uniqueUIDs := make([]int64, 0, len(uids))
	seen := make(map[int64]struct{}, len(uids))
	for _, uid := range uids {
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		uniqueUIDs = append(uniqueUIDs, uid)
	}

	const batchSize = 500
	sqlStr := `select user_id, username from user where user_id in (?)`
	for start := 0; start < len(uniqueUIDs); start += batchSize {
		end := start + batchSize
		if end > len(uniqueUIDs) {
			end = len(uniqueUIDs)
		}

		query, args, err := sqlx.In(sqlStr, uniqueUIDs[start:end])
		if err != nil {
			return nil, err
		}
		query = db.Rebind(query)

		batchUsers := make([]*models.User, 0, end-start)
		if err = db.Select(&batchUsers, query, args...); err != nil {
			return nil, err
		}
		users = append(users, batchUsers...)
	}
	return
}
