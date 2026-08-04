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
	"NanoKVM-Server/service/stream/webrtc"
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
	// There used to be a DisableHdmiCapture() and a 10ms sleep in front of
	// this. It was a power cycle of the HDMI receiver, and it did nothing
	// useful in either direction. On alpha and beta boards kvmv_hdmi_control
	// declines the call outright, so the pair was dead code. On the PCIe board
	// it did toggle the receiver, 10ms apart, which is a number copied from
	// libkvm rather than one the receiver was measured against - and
	// EnableHdmiCapture powers the receiver on anyway, so the cycle added a
	// teardown nothing had asked for. `Settings > Reset HDMI` exists for a
	// deliberate cycle and waits a full second between the halves.
	//
	// The idle bookkeeping that DisableHdmiCapture also does is all zero-valued
	// in a process that has only just started, so dropping the call loses it
	// nothing.
	if !utils.IsHdmiDisabled() {
		vm.EnableHdmiCapture()
	}
	vm.SetHdmiViewerCount(0)

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
	// Stop the audio child before this process goes away. It does not follow us
	// out: arecord sees the closed pipe only when it writes, and while the host
	// plays nothing it blocks in the ALSA read forever. The orphan keeps the
	// capture card open, so the next server cannot record and audio stays dead
	// until somebody kills it by hand.
	//
	// This covers SIGTERM, which is what `S95nanokvm restart` and the in-place
	// updater send. S95nanokvm kills arecord as well, because a SIGKILL or a
	// crash reaches no code in this process.
	webrtc.StopAudioCapture()

	common.GetKvmVision().Close()
}
