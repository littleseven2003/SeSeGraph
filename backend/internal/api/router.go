package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthResponse 是健康检查接口的响应体。
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// NewRouter 构建对外路由。当前仅暴露健康检查，其余路径一律返回 404。
func NewRouter(version, commit string) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	router.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, HealthResponse{
			Status:  "ok",
			Version: version,
			Commit:  commit,
		})
	})

	return router
}
