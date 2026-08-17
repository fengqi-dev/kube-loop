package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

func serveHTTP(handler *Handler, writer http.ResponseWriter, request *http.Request) {
	router := echo.New()
	handler.RegisterRoutes(router.Group(""))
	router.ServeHTTP(writer, request)
}
