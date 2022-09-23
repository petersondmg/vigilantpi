package main

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"syscall"
	"time"

	ping "github.com/sparrc/go-ping"

	"github.com/corona10/goimagehash"
)

const (
	minVideoDuration = time.Second * 50
)

var (
	cameraByName map[string]*Camera
	hashFnByName = map[string]func(image.Image) (*goimagehash.ImageHash, error){
		"perception": goimagehash.PerceptionHash,
		"average":    goimagehash.AverageHash,
		"difference": goimagehash.DifferenceHash,
	}
)

func init() {
	cameraByName = make(map[string]*Camera)
}

// Camera ...
type Camera struct {
	Name                      string   `yaml:"name"`
	URL                       string   `yaml:"url"`
	Audio                     bool     `yaml:"audio"`
	VideoCodec                string   `yaml:"video_codec"`
	AudioCodec                string   `yaml:"audio_codec"`
	Extension                 string   `yaml:"extension"`
	RTSPTransport             string   `yaml:"rtsp_transport"`
	InRate                    float64  `yaml:"in_rate"`
	OutRate                   float64  `yaml:"out_rate"`
	PreRec                    []string `yaml:"pre_rec"`
	AfterRec                  []string `yaml:"after_rec"`
	DisableParallelTransition bool     `yaml:"disable_parallel_transition"`
	MotionDetection           *struct {
		SnapshotInterval time.Duration `yaml:"snapshot_interval"`
		MinDistance      int           `yaml:"min_distance"`
		MaxDistance      int           `yaml:"max_distance"`
		Alg              string        `yaml:"alg"`
		TimeRange        struct {
			Start time.Duration `yaml:"start"`
			End   time.Duration `yaml:"end"`
		} `yaml:"time_range"`
	} `yaml:"motion_detection"`
	healthy bool
}

func (c *Camera) SetupMotionDetection() {
	if c.MotionDetection == nil {
		return
	}
	md := c.MotionDetection
	if md.SnapshotInterval < time.Minute {
		md.SnapshotInterval = time.Minute
	}

	hasher, ok := hashFnByName[md.Alg]
	if !ok {
		hasher = goimagehash.DifferenceHash
		md.Alg = "difference"
	}

	logger.Printf("md: set for %s - %v", c.Name, md)

	go func() {
		emptyFn := func() error { return nil }
		var (
			path, lastPath string
			err            error

			rm     = emptyFn
			lastRm = emptyFn
		)
		fire := func(t time.Time) {
			// only run in time window if set to
			if md.TimeRange.Start != 0 && md.TimeRange.End != 0 {
				midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
				if t.Before(midnight.Add(md.TimeRange.Start)) ||
					t.After(midnight.Add(md.TimeRange.End)) {
					return
				}
			}
			path, rm, err = c.Snapshot()
			if err != nil {
				logger.Printf("md: error taking snapshot on %s: %s", c.Name, err)
				return
			}
			if lastPath != "" {
				func() {
					lastFile, err := os.Open(lastPath)
					if err != nil {
						logger.Printf("md: error opening last file of %s: %s", c.Name, err)
						return
					}
					defer lastFile.Close()
					lastImg, err := jpeg.Decode(lastFile)
					if err != nil {
						logger.Printf("md: error decoding last image of %s: %s", c.Name, err)
						return
					}

					currFile, err := os.Open(path)
					if err != nil {
						logger.Printf("md: error opening current file of %s: %s", c.Name, err)
						return
					}
					defer currFile.Close()
					currImg, err := jpeg.Decode(currFile)
					if err != nil {
						logger.Printf("md: error decoding current image of %s: %s", c.Name, err)
						return
					}

					hash1, _ := hasher(lastImg)
					hash2, _ := hasher(currImg)

					distance, err := hash1.Distance(hash2)
					if err != nil {
						logger.Printf("md: error checking distance of %s: %s", c.Name, err)
						return
					}

					if distance < md.MinDistance || distance > md.MaxDistance {
						if distance > md.MaxDistance {
							logger.Printf("md: ignored! distance: %d on camera %s", distance, c.Name)
						}
						return
					}

					logger.Printf(
						"md: difference detected!! v: %d - last: %s, current: %s",
						distance,
						lastPath,
						path,
					)

					// wont delete
					lastRm = emptyFn
					rm = emptyFn

					telegramNotify(TelegramNotification{
						Text:   fmt.Sprintf("Motion detection on camera %s. (distance: %d)", c.Name, distance),
						Images: []string{lastPath, path},
					})
				}()
			}

			if err := lastRm(); err != nil {
				logger.Printf("md: error removing last snapshot on %s: %s", c.Name, err)
			}
			lastPath = path
			lastRm = rm
		}
		for now := range time.Tick(md.SnapshotInterval) {
			fire(now)
		}
	}()
}

func (c *Camera) Snapshot() (fpath string, rm func() error, err error) {
	dir := path.Join(videosDir, "snapshots")
	if err := os.MkdirAll(dir, 0774); err != nil {
		return "", nil, err
	}

	fpath = fmt.Sprintf("%s/%s_%s.jpg", dir, c.Name, time.Now().Format("2006_01_02_15_04_05"))
	const cmd = `ffmpeg -y -i '%s' -ss 00:00:01.500 -f image2 -vframes 1 '%s'`
	out, err := exec.Command("bash", "-c", fmt.Sprintf(cmd, c.URL, fpath)).Output()
	if err != nil {
		return "", nil, fmt.Errorf("err: %s - out: %s", err, out)
	}
	rm = func() error {
		return os.Remove(fpath)
	}
	return
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
		if stats.PacketsRecv == 0 {
			logger.Printf(
				"camera %s in not responding. ping stats - sent: %d, recv: %d,    loss: %v%%",
				c.Name,
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss,
			)
			led.BadCamera()
			return
		}
		//c.Healthy()

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

	if c.Extension == "" {
		c.Extension = "mp4"
	}
	fileName := start.Format("15_04_05_") + c.Name + "." + c.Extension

	if !hddIsMounted() {
		logger.Println("can't record: hdd is not mounted")
		telegramNotifyf("error: HD is not working")
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

	codec := c.VideoCodec
	if codec == "" {
		codec = "copy"
	}

	if c.InRate <= 0 {
		c.InRate = 10
	}

	if c.OutRate <= 0 {
		c.OutRate = 10
	}

	args := []string{
		ffmpeg,
		"-nostdin",
		"-nostats",
		"-y",
		"-r",
		fmt.Sprintf("%.1f", c.InRate),
	}

	if c.RTSPTransport != "" {
		args = append(args, "-rtsp_transport", c.RTSPTransport)
	}

	args = append(
		args,
		"-i",
		c.URL,
		"-c:v",
		codec,
		"-r",
		fmt.Sprintf("%.1f", c.OutRate),
	)

	if !c.Audio {
		args = append(args, "-an")
	} else {
		audioCodec := c.AudioCodec
		if audioCodec == "" {
			audioCodec = "copy"
		}

		args = append(args, "-c:a", audioCodec)
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

	signals := make(chan syscall.Signal, 1)
	defer func() {
		close(signals)
	}()

	finished := make(chan struct{}, 1)

	go func() {
		err := execProcess(ffmpeg, args, signals)
		if err != nil {
			logger.Printf("error running ffmpeg for %s - %s", c.Name, err)
			led.BadCamera()
			return
		}
		finished <- struct{}{}
	}()

	shouldInterrupt := make(chan struct{}, 1)
	if !c.DisableParallelTransition {
		go func() {
			<-time.After(config.Duration)
			shouldInterrupt <- struct{}{}
		}()
	}

	select {
	case <-ctx.Done():
		signals <- syscall.SIGTERM
		logger.Printf("SIGTERM sent to %s", c.Name)

		select {
		case <-finished:
		case <-time.After(config.TerminationTimeout):
			signals <- syscall.SIGKILL
			logger.Printf("SIGKILL sent to %s", c.Name)
		}

	// only executes if parallel transition is enabled
	case <-shouldInterrupt:
		signals <- syscall.SIGINT
		logger.Printf("SIGINT sent to %s", c.Name)

	case <-finished:
		logger.Printf("recording %s finished", c.Name)
	}

	took := time.Since(start)

	if took < minVideoDuration {
		if c.healthy {
			logger.Printf("camera %s is unhealthy. recording took %s", c.Name, took)
			telegramNotifyf("error: camera %s is not recording", c.Name)
		}
		led.BadCamera()
		c.Unhealthy()
	} else {
		if !c.healthy {
			telegramNotifyf("camera %s is now recording", c.Name)
		}
		c.Healthy()
	}

	if c.healthy {
		logger.Printf("recording %s took %s\n", c.Name, took)
	}

	c.RunAfterRecTasks()
}
func execProcess(ffmpeg string, args []string, signal chan syscall.Signal) error {
	logger.Println("running")

	var stdOut, stdErr *os.File

	if config.Debug {
		stdOut = os.Stdout
		stdErr = os.Stderr
	} else {
		devNull := os.NewFile(0, os.DevNull)
		stdOut = devNull
		stdErr = devNull
	}

	p, err := os.StartProcess(
		ffmpeg,
		args,
		&os.ProcAttr{
			Env: os.Environ(),
			Files: []*os.File{
				nil, /* stdin */
				stdOut,
				stdErr,
			},
		},
	)
	if err != nil {
		logger.Print(err)
		return err
	}

	go func() {
		for s := range signal {
			logger.Printf("received signal: %s", s)
			if err := p.Signal(s); err != nil {
				logger.Printf("error sending signal: %s", err)
			}
		}
	}()

	state, err := p.Wait()
	if err != nil {
		logger.Printf("wait err: %s", err)
		return err
	}
	logger.Println("state:", state)
	logger.Println("finished")
	return err
}
