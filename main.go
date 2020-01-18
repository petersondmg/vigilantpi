package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Config yaml ...
type Config struct {
	FFMPEG    string        `yaml:"ffmpeg"`
	User      string        `yaml:"user"`
	Pass      string        `yaml:"pass"`
	VideosDir string        `yaml:"videos_dir"`
	LokiURL   string        `yaml:"loki_url"`
	Duration  time.Duration `yaml:"duration"`
	Cameras   []Camera      `yaml:"cameras"`
}

// Camera ...
type Camera struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Proto string `json:"proto"`
}

var (
	logger    *log.Logger
	videosDir string
	duration  time.Duration
	ffmpeg    string
)

func main() {
	logger = log.New(os.Stdout, "", log.LstdFlags)

	var configPath string
	if configPath = os.Getenv("CONFIG"); configPath == "" {
		logger.Println("no CONFIG env, using default value")
		configPath = "./config.yml"
	}
	f, err := os.Open(configPath)
	errIsNil(err)

	c := new(Config)
	errIsNil(yaml.NewDecoder(f).Decode(c))

	fmt.Println(c)

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

	logger.Println("started!")

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
	fmt.Println("finished")
}

func record(c Camera) {
	const layout = "2006_01_02_15_04_05"
	fmt.Printf("recording %s...\n", c.Name)
	fileName := c.Name + "_" + time.Now().Format(layout) + ".mp4"
	start := time.Now()

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
		path.Join(videosDir, fileName),
	}

	p, err := os.StartProcess(
		ffmpeg,
		args,
		&os.ProcAttr{
			Env: os.Environ(),

			Files: []*os.File{
				nil,
				os.Stdout,
				os.Stdout,
			},
		},
	)
	fmt.Println(strings.Join(args, " "))

	if err != nil {
		logger.Printf("error recording %s - %s", c.Name, err)
		return
	}
	//fmt.Println("sleeping..")
	time.Sleep(duration)
	go func() {
		p.Signal(os.Interrupt)
		//fmt.Println("SIGINT sent")
		s, err := p.Wait()
		if err != nil {
			logger.Printf("error getting proccess state %s - %s", c.Name, err)
			return
		}
		//fmt.Println("wait finished", s)
		for !s.Exited() {
		}
		fmt.Printf("recording %s took %s\n", c.Name, time.Now().Sub(start))
	}()
}

func errIsNil(err error) {
	if err != nil {
		logger.Fatal(err)
	}
}

func updater() {

}
