package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/stianeikeland/go-rpio/v4"
)

// Config yaml ...
type Config struct {
	FFMPEG      string        `yaml:"ffmpeg"`
	MountDir    string        `yaml:"mount_dir"`
	MountDev    string        `yaml:"mount_dev"`
	User        string        `yaml:"user"`
	Pass        string        `yaml:"pass"`
	VideosDir   string        `yaml:"videos_dir"`
	LokiURL     string        `yaml:"loki_url"`
	Duration    time.Duration `yaml:"duration"`
	Cameras     []Camera      `yaml:"cameras"`
	DailyBackup struct {
		ScpURL    string `yaml:"scp_url"`
		PublicKey string `yaml:"public_key"`
	} `yaml:"daily_backup"`
	RaspberryPI struct {
		LEDPin int `yaml:"led_pin"`
	} `yaml:"raspberry_pi"`
}

// Camera ...
type Camera struct {
	Name       string `yaml:"name"`
	URL        string `yaml:"url"`
	PreRecURLs []struct {
		URL       string `yaml:"url"`
		Method    string `yaml:"method"`
		BasicUser string `yaml:"basic_user"`
		BasicPass string `yaml:"basic_pass"`
		Headers   []struct {
			Name  string `yaml:"name"`
			Value string `yaml:"value"`
		} `yaml:"headers"`
		Expect string `yaml:"expect"`
	} `yaml:"pre_rec_urls"`
}

func (c *Camera) RunPreRecURLs() {
	for _, u := range c.PreRecURLs {
		u := u
		req, err := http.NewRequest(strings.ToUpper(u.Method), u.URL, nil)
		if err != nil {
			logger.Printf("error on pre rec %s", u.URL, err)
			continue
		}
		for _, h := range u.Headers {
			req.Header.Set(h.Name, h.Value)
		}

		if u.BasicUser != "" || u.BasicPass != "" {
			req.SetBasicAuth(u.BasicUser, u.BasicPass)
		}

		go func() {
			res, err := preRecClient.Do(req)
			if err != nil {
				logger.Println("error on pre rec url request", err)
				return
			}
			defer res.Body.Close()
			body, _ := ioutil.ReadAll(res.Body)
			bodyText := string(body)
			if u.Expect != "" && !strings.Contains(bodyText, u.Expect) {
				logger.Printf("pre rec unexpected result. expected %s, got %s", u.Expect, bodyText)
			}
		}()
	}
}

var (
	logger    *log.Logger
	videosDir string
	duration  time.Duration
	ffmpeg    string
	led       struct {
		Blink func()
		On    func()
		Off   func()
	}
	mountedDir string
	mountDev   string

	preRecClient = http.Client{
		Timeout: time.Second * 3,
	}
)

func main() {
	logger = log.New(os.Stdout, "", log.LstdFlags)

	var configPath string
	if configPath = os.Getenv("CONFIG"); configPath == "" {
		logger.Println("no CONFIG env, using default value")
		configPath = "./config.yaml"
	}
	f, err := os.Open(configPath)
	errIsNil(err)

	c := new(Config)
	errIsNil(yaml.NewDecoder(f).Decode(c))

	if videosDir = c.VideosDir; videosDir == "" {
		logger.Println("no videos_dir defined, using default value")
		videosDir = "./cameras"
	}

	if ffmpeg = c.FFMPEG; ffmpeg == "" {
		logger.Println("ffmpeg path undifined, using default value")
		ffmpeg = "/usr/local/bin/ffmpeg"
	}

	if duration = c.Duration; duration == 0 {
		logger.Println("no duration defined, using default value")
		duration = time.Hour * 1
	}

	logger.Printf("videos duration: %s", duration)

	if c.RaspberryPI.LEDPin > 0 {
		unmapGPIO := setupLED(c.RaspberryPI.LEDPin)
		defer unmapGPIO()
	}
	mountedDir = safeShell(c.MountDir)
	mountDev = safeShell(c.MountDev)

	logger.Println("started!")

	if !hddIsMounted() {
		led.Blink()
		tryMount()
		for !hddIsMounted() {
			logger.Println("hdd is not mounted. waiting..")
			time.Sleep(time.Second * 10)
		}
	}
	logger.Println("hdd is mounted")
	led.On()

	go updater()

	rec := make(chan Camera, 0)
	done := make(chan struct{}, 0)
	go func() {
		for c := range rec {
			c := c
			go func() {
				record(c)
				rec <- c
			}()
		}
	}()
	for _, camera := range c.Cameras {
		rec <- camera
	}
	<-done
	logger.Println("finished")
}

func record(c Camera) {
	start := time.Now()
	dayDir := start.Format("2006_01_02")
	fileName := start.Format("15_04_05_") + c.Name + ".mp4"

	if !hddIsMounted() {
		logger.Println("can't record: hdd is not mounted")
		led.Blink()
		tryMount()
		return
	}

	recDir := path.Join(videosDir, dayDir)
	if err := os.MkdirAll(recDir, 0774); err != nil {
		logger.Printf("error creating recording directory %s: %s", recDir, err)
		led.Blink()
		return
	}

	c.RunPreRecURLs()

	led.On()

	logger.Printf("recording %s...\n", c.Name)

	args := []string{
		ffmpeg,
		"-nostdin",
		"-nostats",
		"-r",
		"10",
		"-i",
		c.URL,
		"-c:v",
		"copy",
		"-r",
		"10",
		"-an",
		/*
			sets duration
			"-to",
			"60",
		*/
		path.Join(videosDir, dayDir, fileName),
	}

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
	// logger.Println(strings.Join(args, " "))

	if err != nil {
		logger.Printf("error running ffmpeg for %s - %s", c.Name, err)
		return
	}

	time.Sleep(duration)
	go func() {
		p.Signal(os.Interrupt)
		s, err := p.Wait()
		if err != nil {
			logger.Printf("error getting proccess state %s - %s", c.Name, err)
			return
		}
		for !s.Exited() {
		}
		logger.Printf("recording %s took %s\n", c.Name, time.Now().Sub(start))
	}()
}

func errIsNil(err error) {
	if err != nil {
		logger.Fatal(err)
	}
}

func updater() {

}

func setupLED(ledPin int) func() error {
	led.Blink = func() {
		logger.Println("blink led")
	}
	led.On = func() {
		logger.Println("led on")
	}
	led.Off = func() {
		logger.Println("led off")
	}
	pin := rpio.Pin(ledPin)
	if err := rpio.Open(); err != nil {
		logger.Println("Error setuping LED:", err)
		return func() error {
			return nil
		}
	}
	pin.Output()

	var blinking bool
	led.Blink = func() {
		blinking = true
		go func() {
			for blinking {
				pin.Toggle()
				time.Sleep(time.Second / 2)
			}
		}()
	}
	led.On = func() {
		blinking = false
		pin.High()
	}
	led.Off = func() {
		blinking = false
		pin.Low()
	}
	return rpio.Close
}

func hddIsMounted() bool {
	if mountedDir == "" {
		return true
	}
	res, err := exec.Command("lsblk", "-o", "NAME,MOUNTPOINT", "--json").Output()
	if err != nil {
		logger.Println("error on mount cmd", err)
		return false
	}
	var resp struct {
		Devices []struct {
			Name       string `json:"name"`
			Mountpoint string `json:"mountpoint"`
			Children   []struct {
				Name       string `json:"name"`
				Mountpoint string `json:"mountpoint"`
			}
		} `json:"blockdevices"`
	}
	err = json.Unmarshal(res[:], &resp)
	if err != nil {
		logger.Println("cant unmarshal lsblk response:", err)
		return false
	}
	for _, device := range resp.Devices {
		if device.Mountpoint == mountedDir {
			return true
		}
		for _, child := range device.Children {
			if child.Mountpoint == mountedDir {
				return true
			}
		}
	}
	return false
}

func tryMount() {
	if mountDev == "" {
		return
	}
	if mountedDir == "" {
		logger.Println("no mount directory specified")
		return
	}
	logger.Println("trying to mount...")
	_, err := exec.Command(
		"mount",
		"-t",
		"vfat",
		"-o",
		"umask=0022,gid=1000,uid=1000",
		mountDev,
		mountedDir,
	).Output()
	if err != nil {
		logger.Println("error when trying to mount:", err)
	}
}

var shellR = strings.NewReplacer(
	"$", "",
	"`", "",
	"!", "",
	"(", "",
	")", "",
)

func safeShell(s string) string {
	return shellR.Replace(s)
}
