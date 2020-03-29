package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	logPath          = "/home/alarm/vigilantpi.log"
	minVideoDuration = time.Minute * 1
)

var (
	version = "development"

	logger     *log.Logger
	videosDir  string
	duration   time.Duration
	configPath string
	ffmpeg     string
	led        struct {
		BadHD      func()
		BadNetwork func()
		BadCamera  func()

		On  func()
		Off func()

		Confirm func()
	}
	mountedDir string
	mountDev   string

	started = time.Now()

	config *Config

	stop chan struct{}

	shouldReboot bool
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}

	kill := make(chan os.Signal, 1)
	signal.Notify(kill, os.Interrupt, syscall.SIGTERM)
	stop = make(chan struct{})

	go func() {
		<-kill
		stop <- struct{}{}
	}()

	logger = log.New(os.Stdout, "", log.LstdFlags)

	logger.Printf("VigilantPI version: %s", version)

	loadConfig()

	config.Tasks.Init()

	go httpServer(config.Admin.Addr, config.Admin.User, config.Admin.Pass)

	//go mdnsServer()

	if videosDir = config.VideosDir; videosDir == "" {
		logger.Println("no videos_dir defined, using default value")
		videosDir = "./cameras"
	}

	if ffmpeg = config.FFMPEG; ffmpeg == "" {
		logger.Println("ffmpeg path undifined, using default value")
		ffmpeg = "/usr/local/bin/ffmpeg"
	}

	if duration = config.Duration; duration == 0 {
		logger.Println("no duration defined, using default value")
		duration = time.Hour * 1
	}

	logger.Printf("videos duration: %s", duration)

	if config.RaspberryPI.LEDPin > 0 {
		unmapGPIO := setupLED(config.RaspberryPI.LEDPin)
		defer unmapGPIO()
	}

	led.BadHD()

	mountedDir = safeShell(config.MountDir)
	mountDev = safeShell(config.MountDev)

	logger.Println("started!")

	ctx, cancel := context.WithCancel(context.Background())

	finished := make(chan struct{})
	go func() {
		run(ctx, config.Cameras)
		finished <- struct{}{}
	}()

	go crond(config.Cron)

	<-stop
	cancel()

	logger.Println("waiting recordings to finish")
	select {
	case <-finished:
	case <-time.NewTimer(time.Minute * 1).C:
		logger.Println("waiting timeout, exiting")
	}

	go func() {
		time.Sleep(time.Second * 1)
		// force reboot on vigilantpid
		os.Exit(2)
	}()

	if shouldReboot {
		logger.Println("executing rebooting cmd...")
		_, err := exec.Command("shutdown", "-r", "now").Output()
		logger.Println("executed cmd...")
		if err != nil {
			logger.Printf("error rebooting: %s", err)
		}
	}
}

func errIsNil(err error) {
	if err != nil {
		logger.Fatal(err)
	}
}

func run(ctx context.Context, cameras []Camera) {
	if !hddIsMounted() {
		led.BadHD()
		tryMount()
		for !hddIsMounted() {
			logger.Println("hdd is not mounted. waiting..")
			time.Sleep(time.Second * 10)
		}
	}
	logger.Println("hdd is mounted")

	updateConfig()

	led.On()

	go oldFilesWatcher()

	done := make(chan struct{})
	var running int32
	var shouldExit bool

	rec := make(chan *Camera)

	go func() {
		for {
			select {
			case <-ctx.Done():
				shouldExit = true
				return

			case c := <-rec:
				c.healthy = true
				go func() {
					atomic.AddInt32(&running, 1)
					record(ctx, c)
					result := atomic.AddInt32(&running, -1)
					if shouldExit {
						if result == 0 {
							done <- struct{}{}
						}
						return
					}
					if c.healthy {
						rec <- c
						return
					}
					time.Sleep(time.Minute * 5)
					rec <- c
				}()
			}
		}
	}()

	for _, camera := range cameras {
		camera := camera
		rec <- &camera
	}

	<-done
}

func clearLogs() {
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0755)
	if err != nil {
		logger.Printf("error clearing log: %s", err)
		return
	}
	if err = logFile.Close(); err != nil {
		logger.Printf("error closing log: %s", err)
	}
}

func restart() {
	logger.Println("restarting...")
	stop <- struct{}{}
}

func reboot() {
	logger.Println("rebooting...")
	shouldReboot = true
	stop <- struct{}{}
}
