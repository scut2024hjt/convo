package mysql

import (
	"github.com/scut2024hjt/convo/models"
	"database/sql"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

func GetCommunityList() (communityList []*models.Community, err error) {
	sqlStr := "select community_id, community_name from community"
	err = db.Select(&communityList, sqlStr)
	if err != nil {
		if err == sql.ErrNoRows {
			zap.L().Warn("there is no community in db")
			err = nil
		}
		zap.L().Error("query data failed", zap.Error(err))
	}
	return
}

// GetCommunityDetailByID 根据 ID 查询社区详情
func GetCommunityDetailByID(id int64) (communityDetail *models.CommunityDetail, err error) {
	communityDetail = new(models.CommunityDetail)
	sqlStr := `select community_id, community_name, introduction, create_time from community where community_id = ?`
	err = db.Get(communityDetail, sqlStr, id)
	if err != nil {
		if err == sql.ErrNoRows {
			zap.L().Warn("there is no communityDetail in db")
			err = ErrorInvalidID
		}
		zap.L().Error("query data failed", zap.Error(err))
	}
	return
}

// GetCommunityDetailsByIDs 根据社区 ID 列表批量查询社区详情
func GetCommunityDetailsByIDs(ids []int64) (communityDetails []*models.CommunityDetail, err error) {
	communityDetails = make([]*models.CommunityDetail, 0, len(ids))
	if len(ids) == 0 {
		return
	}
	sqlStr := `select community_id, community_name, introduction, create_time from community where community_id in (?)`
	query, args, err := sqlx.In(sqlStr, ids)
	if err != nil {
		return nil, err
	}
	query = db.Rebind(query)
	err = db.Select(&communityDetails, query, args...)
	return
}
