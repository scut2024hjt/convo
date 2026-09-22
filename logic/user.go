package logic

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/encrypt"
	"github.com/scut2024hjt/convo/pkg/jwt"
	"github.com/scut2024hjt/convo/pkg/snowflake"
)

// SignUp 存放业务逻辑代码
func SignUp(p *models.ParamsSignUp) (err error) {
	// 1. 判断用户存不存在
	if err := mysql.CheckUserExit(p.Username); err != nil {
		// 数据库查询出错
		return err
	}
	// 2. 生成 UID
	userID := snowflake.GenID()
	// 构造一个 User 实例
	user := &models.User{
		UserID:   userID,
		Username: p.Username,
		Password: p.Password,
	}
	// 3. 密码加密
	user.Password, err = encrypt.EncryptPassword(user.Password)
	if err != nil {
		return err
	}
	// 4. 保存进数据库
	err = mysql.InsertUser(user)
	// redis.xxx ...
	return
}

// Login 存放业务逻辑代码
func Login(p *models.ParamsLogin) (user *models.User, err error) {
	user = &models.User{
		Username: p.Username,
		Password: p.Password,
	}
	// 传递的是指针，就能拿到 user.UserID
	if err := mysql.Login(user); err != nil {
		return nil, err
	}
	sessionBytes := make([]byte, 32)
	if _, err = rand.Read(sessionBytes); err != nil {
		return nil, err
	}
	sessionID := base64.RawURLEncoding.EncodeToString(sessionBytes)
	token, expiresAt, err := jwt.GenToken(user.UserID, user.Username, sessionID)
	if err != nil {
		return nil, err
	}
	if err = redisdao.SetSession(user.UserID, sessionID, time.Until(expiresAt)); err != nil {
		return nil, err
	}
	user.Token = token
	return
}

func Logout(userID int64, sessionID string) error {
	return redisdao.DeleteSession(userID, sessionID)
}
