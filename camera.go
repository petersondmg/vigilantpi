package main

import (
	"context"
	"net/url"
	"os"
	"path"
	"strconv"
	"syscall"
	"time"

	"github.com/sparrc/go-ping"
)

const (
	minVideoDuration = time.Second * 50
)

// Camera ...
type Camera struct {
	Name     string   `yaml:"name"`
	URL      string   `yaml:"url"`
	Audio    bool     `yaml:"audio"`
	PreRec   []string `yaml:"pre_rec"`
	AfterRec []string `yaml:"after_rec"`
	healthy  bool
}

func (c *Camera) Unhealthy() {
	c.healthy = false
}

func (c *Camera) Healthy() {
	c.healthy = true
}

func (c *Camera) HealthCheck() func() {
	p := func() {
		u, err := url.Parse(c.URL)
		if err != nil {
			logger.Printf("error parsing camera (%s) url: %s", c.Name, err)
			led.BadCamera()
			return
		}

		pinger, err := ping.NewPinger(u.Hostname())
		if err != nil {
			logger.Printf("error trying to ping camera %s: %s", c.Name, err)
			led.BadCamera()
			return
		}
		pinger.SetPrivileged(true)

		pinger.Count = 3
		pinger.Timeout = time.Second * 15
		pinger.Run()

		stats := pinger.Statistics()
		if stats.PacketsRecv < pinger.Count {
			logger.Printf(
				"camera %s in not responding. ping stats - sent: %d, recv: %d,    loss: %v%%",
				c.Name,
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss,
			)
			led.BadCamera()
			return
		}
		c.Healthy()

		led.On()
	}

	t := time.NewTicker(time.Minute * 5)
	stop := make(chan struct{})

	go func() {
		for {
			select {
			case <-t.C:
				p()
			case <-stop:
				return
			}
		}
	}()

	return func() {
		t.Stop()
		stop <- struct{}{}
	}
}

func (c *Camera) RunPreRecTasks() {
	for _, taskName := range c.PreRec {
		task, ok := taskByName[taskName]
		if !ok {
			logger.Printf("invalid pre_rec task %s", taskName)
			continue
		}
		task.Run()
	}
}

func (c *Camera) RunAfterRecTasks() {
	for _, taskName := range c.PreRec {
		task, ok := taskByName[taskName]
		if !ok {
			logger.Printf("invalid pre_rec task %s", taskName)
			continue
		}
		task.Run()
	}
}

const (
	dayDirLayout = "rec_2006_01_02"
)

func record(ctx context.Context, c *Camera, stillProcessing chan<- struct{}) {
	start := time.Now()
	dayDir := start.Format(dayDirLayout)
	fileName := start.Format("15_04_05_") + c.Name + ".mp4"

	if !hddIsMounted() {
		logger.Println("can't record: hdd is not mounted")
		led.BadHD()
		tryMount()
		return
	}

	var err error

	recDir := path.Join(videosDir, dayDir)
	if err = os.MkdirAll(recDir, 0774); err != nil {
		logger.Printf("error creating recording directory %s: %s", recDir, err)
		led.BadHD()
		return
	}

	stopCheck := c.HealthCheck()
	defer stopCheck()

	c.RunPreRecTasks()

	if c.healthy {
		logger.Printf("recording %s...\n", c.Name)
	}

	args := []string{
		ffmpeg,
		"-nostdin",
		"-nostats",
		"-y",
		"-r",
		"10",
		"-i",
		c.URL,
		"-c:v",
		"copy",
		"-r",
		"10",
	}

	if !c.Audio {
		args = append(args, "-an")
	}

	args = append(
		args,
		//sets duration
		"-to",
		strconv.Itoa(int(duration.Seconds())),
		"-movflags",
		"+faststart",
		path.Join(videosDir, dayDir, fileName),
	)

	p, err := os.StartProcess(
		ffmpeg,
		args,
		&os.ProcAttr{
			Env: os.Environ(),

			Files: []*os.File{
				nil,
				nil, // os.Stdout,
				nil, // os.Stdout,
			},
		},
	)

	//logger.Println(strings.Join(args, " "))

	if err != nil {
		logger.Printf("error running ffmpeg for %s - %s", c.Name, err)
		led.BadCamera()
		return
	}

	timeout := time.NewTimer(duration + (time.Minute * 5))
	defer timeout.Stop()

	exited := make(chan struct{})

	var sigterm bool

	go func() {
		state, err := p.Wait()
		if err != nil {
			logger.Printf("error getting proccess state %s - %s", c.Name, err)
			return
		}
		if !sigterm && !state.Exited() {
			logger.Printf("p.Wait() returned but process hasn't exited for %s. killing process...", c.Name)
			p.Signal(syscall.SIGKILL)
			p.Kill()
		}
		exited <- struct{}{}
		exited <- struct{}{}
	}()

	go func() {
		select {
		case <-ctx.Done():
			logger.Println("sending SIGTERM to ffmpeg process of ", c.Name, "-", p.Signal(syscall.SIGTERM))
			sigterm = true
		case <-exited:
		}
	}()

	select {
	case <-time.After(duration + (time.Minute * 1)):
		logger.Printf("still processing %s...", c.Name)
		stillProcessing <- struct{}{}

	case <-timeout.C:
		logger.Printf("recording process of %s has timeout. killing process...", c.Name)
		p.Signal(syscall.SIGTERM)
		sigterm = true
		time.Sleep(time.Second * 1)
		p.Signal(syscall.SIGKILL)
		p.Kill()
		return

	case <-exited:
	}

	took := time.Now().Sub(start)

	if took < minVideoDuration {
		if c.healthy {
			logger.Printf("camera %s is unhealthy. recording took %s", c.Name, took)
		}
		led.BadCamera()
		c.Unhealthy()
	} else {
		c.Healthy()
	}

	if c.healthy {
		logger.Printf("recording %s took %s\n", c.Name, took)
	}

	c.RunAfterRecTasks()
}
