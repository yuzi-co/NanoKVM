package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/auth"
)

func authRouter(r *gin.Engine) {
	service := auth.NewService()

	r.POST("/api/auth/login", service.Login) // login

	api := r.Group("/api").Use(middleware.CheckToken())

	api.GET("/auth/password", service.IsPasswordUpdated) // is password updated
	api.GET("/auth/account", service.GetAccount)         // get account
	api.POST("/auth/password", service.ChangePassword)   // change password
	api.POST("/auth/logout", service.Logout)             // logout

	// Issuing and revoking keys is session work: an api key must not be able
	// to mint further keys.
	keys := r.Group("/api").Use(middleware.CheckSession())

	keys.GET("/auth/api-keys", service.GetAPIKeys)          // list api keys
	keys.POST("/auth/api-keys", service.CreateAPIKey)       // create an api key
	keys.DELETE("/auth/api-keys/:id", service.DeleteAPIKey) // revoke an api key
}
