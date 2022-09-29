package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"vigilantpi/db"
)

var (
	version = "development"

	logPath = os.Getenv("LOG")

	logger *log.Logger

	configPath string
	videosDir  string
	mountedDir string
	mountDev   string
	mountLabel string

	duration time.Duration

	ffmpeg string

	started = time.Now()

	config *Config

	emptyFn = func() {}

	led = struct {
		BadHD      func()
		BadNetwork func()
		BadCamera  func()

		On  func()
		Off func()

		Confirm func()
	}{
		BadHD:      emptyFn,
		BadNetwork: emptyFn,
		BadCamera:  emptyFn,
		On:         emptyFn,
		Off:        emptyFn,
		Confirm:    emptyFn,
	}

	stop chan struct{}

	shouldReboot bool
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {

		case "version":
			fmt.Println(version)
			return

		case "mount-dir":
			loadConfig()
			fmt.Println(config.MountDir)
			return
		}
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
	mountLabel = safeShell(config.MountLabel)

	vigilantDB := os.Getenv("DB")
	if vigilantDB == "" {
		vigilantDB = "/home/pi/vigilantpi/db.json"
		log.Printf("No DB env. Default DB to %s", vigilantDB)
	}

	if err := db.Init(vigilantDB); err != nil {
		logger.Printf("error opening .json database: %s", err)
	}
	defer db.Close()

	logger.Println("started!")
	go telegramBot()

	telegramNotifyf("VigilantPI started at %s", started.Format("15:04:05 - 02/01/2006"))

	ctx, cancel := context.WithCancel(context.Background())

	finished := make(chan struct{})
	go func() {
		if p := db.Get("pause"); p != "" {
			db.Del("pause")
			pause, err := time.ParseDuration(p)
			if err == nil && pause > 0 {
				msg := fmt.Sprintf("System paused %s! Restart to resume.", pause)
				logger.Printf(msg)
				telegramNotifyf(msg)
				time.Sleep(pause)
				logger.Print("System resumed!")
				telegramNotifyf("System resumed!")
			}
		}
		run(ctx, config.Cameras)
		finished <- struct{}{}
	}()

	go crond(config.Cron)

	if config.HealthCheckURL != "" {
		go healthcheck()
	}

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
		forceReboot()
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

func healthcheck() {
	log.Printf("health check enabled")
	for range time.NewTicker(time.Minute * 5).C {
		healthy := true
		if !hddIsMounted() {
			healthy = false
		}

		for _, c := range cameraByName {
			if !c.healthy {
				healthy = false
			}
		}

		if healthy {
			req, err := http.NewRequest(http.MethodGet, config.HealthCheckURL, nil)
			if err != nil {
				log.Printf("error on health check url: %s: %s", config.HealthCheckURL, err)
				return
			}
			res, err := tasksClient.Do(req)
			if err != nil {
				log.Printf("error on health check request: %s", err)
				return
			}
			defer res.Body.Close()
		}
	}
}

func run(ctx context.Context, cameras []Camera) {
	if !hddIsMounted() {
		led.BadHD()
		for !hddIsMounted() {
			tryMount()
			logger.Println("hdd is not mounted. waiting..")
			time.Sleep(time.Second * 10)
		}
	}
	logger.Println("hdd is mounted")

	updateConfig()

	led.On()

	go oldFilesWatcher(config.DeleteAfterDays)

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
				go func() {
					atomic.AddInt32(&running, 1)

					stillProcessing := make(chan struct{})
					recordingFinished := make(chan struct{})

					released := false
					release := func() {
						if released {
							return
						}
						released = true
						rec <- c
					}

					go func() {
						record(ctx, c, stillProcessing)
						recordingFinished <- struct{}{}
						recordingFinished <- struct{}{}
					}()

					go func() {
						select {
						case <-stillProcessing:
							if c.healthy && !shouldExit {
								release()
							}

						case <-recordingFinished:
						}
					}()

					<-recordingFinished

					result := atomic.AddInt32(&running, -1)
					if shouldExit {
						if result == 0 {
							done <- struct{}{}
						}
						return
					}
					if c.healthy {
						release()
						return
					}
					time.Sleep(time.Second * 10)
					release()
				}()
			}
		}
	}()

	for _, camera := range cameras {
		camera := camera
		camera.Healthy()
		camera.SetupMotionDetection()
		cameraByName[camera.Name] = &camera
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

func forceReboot() {
	// force reboot on vigilantpid
	os.Exit(2)
}
