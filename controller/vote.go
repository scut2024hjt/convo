package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/logic"
	"github.com/scut2024hjt/convo/models"
	"go.uber.org/zap"
)

// PostVoteHandler 投票
// @Summary 投票接口
// @Description 投票接口
// @Tags 投票相关接口
// @Accept application/json
// @Produce application/json
// @Param Authorization header string true "Bearer 用户令牌"
// @Param object body models.ParamsVoteData true "投票参数"
// @Security ApiKeyAuth
// @Success 200 {object} ResponseData
// @Router /vote [post]
func PostVoteHandler(c *gin.Context) {
	// 参数校验
	p := new(models.ParamsVoteData)
	if err := c.ShouldBindJSON(p); err != nil {
		errs, ok := err.(validator.ValidationErrors) // 类型断言
		if !ok {
			ResponseError(c, CodeInvalidParam)
			return
		}
		errData := removeTopStruct(errs.Translate(trans)) // 翻译并去除掉错误提示中的结构体标识
		ResponseErrorWithMsg(c, CodeInvalidParam, errData)
		return
	}
	// 获取当前请求的用户 id
	userID, err := getCurrentUserID(c)
	if err != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	// 具体投票的业务逻辑
	result, err := logic.VoteForPost(userID, p)
	if err != nil {
		zap.L().Error("logic.VoteForPost() failed", zap.Error(err))
		if errors.Is(err, redisdao.ErrVoteTimeExpired) {
			ResponseError(c, CodeVoteExpired)
			return
		}
		if errors.Is(err, redisdao.ErrPostNotInitialized) {
			ResponseError(c, CodePostNotFound)
			return
		}
		if errors.Is(err, redisdao.ErrInvalidDirection) {
			ResponseError(c, CodeInvalidParam)
			return
		}
		ResponseError(c, CodeServerBusy)
		return
	}
	ResponseSuccess(c, result)
}
