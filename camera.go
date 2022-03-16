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
	Name            string   `yaml:"name"`
	URL             string   `yaml:"url"`
	Audio           bool     `yaml:"audio"`
	VideoCodec      string   `yaml:"video_codec"`
	PreRec          []string `yaml:"pre_rec"`
	AfterRec        []string `yaml:"after_rec"`
	MotionDetection *struct {
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
		if stats.PacketsRecv < pinger.Count {
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
	fileName := start.Format("15_04_05_") + c.Name + ".mp4"

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
		codec,
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
			logger.Println("sending SIGTERM to ffmpeg process of", c.Name, " - error return: ", p.Signal(syscall.SIGTERM))
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
