package router

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/picoclaw"

	"github.com/gin-gonic/contrib/static"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func Init(r *gin.Engine) {
	web(r)
	server(r)
	log.Debugf("router init done")
}

func web(r *gin.Engine) {
	execPath, err := os.Executable()
	if err != nil {
		panic("invalid executable path")
	}

	execDir := filepath.Dir(execPath)
	webPath := fmt.Sprintf("%s/web", execDir)

	r.Use(staticHandler(webPath))
}

// apiPrefix covers every route this server registers.
const apiPrefix = "/api/"

// staticHandler serves the built web UI. The static middleware runs before
// routing and stats the filesystem for every request, so an API call or a
// websocket upgrade would pay a syscall against the SD card, and a file that
// happened to sit at the same path would shadow the real handler.
func staticHandler(webPath string) gin.HandlerFunc {
	serve := static.Serve("/", static.LocalFile(webPath, true))

	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, apiPrefix) {
			return
		}

		// Shared caches must not hand a compressed body to a client that
		// cannot read one, whichever branch answers below.
		c.Header("Vary", "Accept-Encoding")

		if servePrecompressed(c, webPath) {
			return
		}

		serve(c)
	}
}

func server(r *gin.Engine) {
	control := controlmode.GetManager()
	picoclawService := picoclaw.NewService(control)

	authRouter(r)
	applicationRouter(r)
	vmRouter(r)
	streamRouter(r)
	storageRouter(r)
	networkRouter(r)
	hidRouter(r)
	controlRouter(r, control, picoclawService)
	mcpRouter(r, control, picoclawService)
	picoclawRouter(r, picoclawService)
	wsRouter(r)
	downloadRouter(r)
	extensionsRouter(r)
}

func LoopbackHTTPAllowedPaths() []string {
	paths := PicoclawLoopbackHTTPAllowedPaths()
	paths = append(paths, HIDLoopbackHTTPAllowedPaths()...)
	return paths
}
