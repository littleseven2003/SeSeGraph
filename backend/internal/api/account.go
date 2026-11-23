package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
)

// maxAccountBodyBytes 是账号凭据修改请求体的上限。
const maxAccountBodyBytes = 4096

// credentialUpdateRequest 是修改管理员凭据的请求体。
// 用户名或新密码留空表示不修改该项，当前密码始终必填。
type credentialUpdateRequest struct {
	CurrentPassword string `json:"current_password"`
	Username        string `json:"username"`
	NewPassword     string `json:"new_password"`
}

// updateCredentialsHandler 校验当前密码后修改管理员账号名或密码。
// 成功后会话版本递增并清除当前 Cookie，请求与响应都不回显密码。
func updateCredentialsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Auth == nil || deps.Sessions == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "账号服务不可用"})
			return
		}

		var req credentialUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求体过大"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}

		creds, err := deps.Auth.UpdateCredentials(c.Request.Context(), auth.CredentialUpdate{
			CurrentPassword: req.CurrentPassword,
			Username:        req.Username,
			NewPassword:     req.NewPassword,
		})
		var fieldErr *auth.FieldError
		switch {
		case err == nil:
		case errors.As(err, &fieldErr):
			c.JSON(http.StatusBadRequest, gin.H{"error": fieldErr.Error(), "fields": []string{fieldErr.Field}})
			return
		case errors.Is(err, auth.ErrNotInitialized):
			c.JSON(http.StatusConflict, gin.H{"error": "管理员尚未初始化"})
			return
		case errors.Is(err, auth.ErrConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "账号已被其他请求修改，请刷新后重试"})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "修改账号失败"})
			return
		}

		deps.Sessions.ClearCookie(c.Writer)
		c.JSON(http.StatusOK, gin.H{"username": creds.Username, "reauthenticate": true})
	}
}
