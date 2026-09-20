package logic

import (
	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/models"
)

func GetCommunityList() ([]*models.Community, error) {
	// 查数据库 查找到所有的 community 并返回
	return mysql.GetCommunityList()
}

func GetCommunityDetail(id int64) (*models.CommunityDetail, error) {
	return mysql.GetCommunityDetailByID(id)
}
