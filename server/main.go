package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"NanoKVM-Server/common"
	"NanoKVM-Server/config"
	"NanoKVM-Server/logger"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/router"
	"NanoKVM-Server/service/vm"
	"NanoKVM-Server/service/vm/jiggler"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	cors "github.com/rs/cors/wrapper/gin"
)

func main() {
	initialize()
	defer dispose()

	run()
}

func initialize() {
	if err := config.EnsurePicoclawInternalToken(); err != nil {
		log.Fatalf("failed to initialize picoclaw internal token: %v", err)
	}

	logger.Init()

	// restore the memory limit the user configured, which is otherwise only
	// applied to the process that set it and lost on the next boot
	utils.InitGoMemLimit()

	// init screen parameters
	_ = common.GetScreen()

	// init HDMI
	//
	// There used to be a SetHDMI(false) and a 10ms sleep in front of this. It
	// was a power cycle of the HDMI receiver, and it did nothing useful in
	// either direction. On alpha and beta boards kvmv_hdmi_control declines the
	// call outright, so the pair was dead code. On the PCIe board it did toggle
	// the receiver, 10ms apart, which is a number copied from libkvm rather
	// than one the receiver was measured against - and EnableHdmiCapture powers
	// the receiver on anyway, so the cycle added a teardown nothing had asked
	// for. `Settings > Reset HDMI` exists for a deliberate cycle and waits a
	// full second between the halves.
	if !utils.IsHdmiDisabled() {
		// Starts the idle countdown too: nothing is watching yet.
		vm.EnableHdmiCapture()
	}

	// run mouse jiggler
	jiggler.GetJiggler().Run()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-sigChan
		log.Printf("\nReceived signal: %v\n", sig)

		dispose()
		os.Exit(0)
	}()
}

func run() {
	conf := config.GetInstance()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	if conf.Authentication == "disable" {
		r.Use(cors.AllowAll())
	}

	router.Init(r)

	httpAddr := utils.ListenAddr(conf.Host, strconv.Itoa(conf.Port.Http))
	loopbackHTTPAddr := utils.ListenAddr("127.0.0.1", strconv.Itoa(conf.Port.Http))
	needsLoopbackHTTP := utils.NeedsDedicatedLoopbackListener(conf.Host)

	if conf.Proto == "https" {
		httpsPortStr := strconv.Itoa(conf.Port.Https)

		go func() {
			server := utils.NewServer(utils.ListenAddr(conf.Host, httpsPortStr), r)
			if err := server.ListenAndServeTLS(conf.Cert.Crt, conf.Cert.Key); err != nil {
				panic("start https server failed")
			}
		}()

		if needsLoopbackHTTP {
			go func() {
				if err := middleware.ListenAndServeLoopbackHTTPRedirect(
					loopbackHTTPAddr,
					httpsPortStr,
					r,
					router.LoopbackHTTPAllowedPaths()...,
				); err != nil {
					panic("start loopback http server failed")
				}
			}()
		}

		if err := middleware.ListenAndServeLoopbackHTTPRedirect(
			httpAddr,
			httpsPortStr,
			r,
			router.LoopbackHTTPAllowedPaths()...,
		); err != nil {
			panic("start http server failed")
		}
	} else {
		if needsLoopbackHTTP {
			go func() {
				if err := utils.NewServer(loopbackHTTPAddr, r).ListenAndServe(); err != nil {
					panic("start loopback http server failed")
				}
			}()
		}

		if err := utils.NewServer(httpAddr, r).ListenAndServe(); err != nil {
			panic("start http server failed")
		}
	}
}

func dispose() {
	common.GetKvmVision().Close()
}
