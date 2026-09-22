package controller

type ResCode int64

const (
	CodeSuccess ResCode = 1000 + iota
	CodeInvalidParam
	CodeUserExist
	CodeUserNotExist
	CodeInvalidPassword
	CodeServerBusy

	CodeNeedLogin
	CodeValidToken
	CodeVoteExpired
	CodePostNotFound
	CodeForbidden
	CodeAuthUnavailable
	CodeTooManyRequests
)

var codeMsgMap = map[ResCode]string{
	CodeSuccess:         "success",
	CodeInvalidParam:    "请求参数错误",
	CodeUserExist:       "用户名存在",
	CodeUserNotExist:    "用户名不存在",
	CodeInvalidPassword: "用户名或密码错误",
	CodeServerBusy:      "服务繁忙",
	CodeNeedLogin:       "需要登录",
	CodeValidToken:      "无效的 token",
	CodeVoteExpired:     "投票时间已过",
	CodePostNotFound:    "帖子不存在",
	CodeForbidden:       "无权执行该操作",
	CodeAuthUnavailable: "认证服务暂不可用",
	CodeTooManyRequests: "请求过于频繁",
}

func (rc ResCode) Msg() string {
	msg, ok := codeMsgMap[rc]
	if !ok {
		msg = codeMsgMap[CodeServerBusy]
	}
	return msg
}
