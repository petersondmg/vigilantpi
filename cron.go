package main

import (
	"io/ioutil"
	"os"
	"path"
	"time"
)

// Cron ...
type Cron struct {
	Every []time.Duration `yaml:"every"`
	At    []time.Time     `yaml:"at"`
	Hooks []Hook          `yaml:"hooks"`
}

func crond(entries []Cron) {
	if len(entries) == 0 {
		return
	}
	logger.Println("setuping cron hooks")
	for _, cron := range entries {
		cron := cron
		for _, d := range cron.Every {
			go func() {
				for now := range time.Tick(d) {
					for _, h := range cron.Hooks {
						logger.Printf("running cron %s at %v", h.Description, now)
						h.Run()
					}
				}
			}()
		}
	}
}

func every24Hours() {
	ticker := time.NewTicker(24 * time.Hour)
	deleteOldStuff := func() {
		logger.Println("veryfing old content")
		files, err := ioutil.ReadDir(videosDir)
		if err != nil {
			logger.Printf("error getting files on %s when deleting old content: %s", videosDir, err)
			return
		}

		oneMonthAgo := time.Now().AddDate(0, -1, 0)
		logger.Println("deleting files older than", oneMonthAgo.Format("02/01/2006"))

		for _, f := range files {
			if !f.IsDir() {
				continue
			}
			fileTime, err := time.Parse(dayDirLayout, f.Name())
			if err != nil {
				continue
			}
			if !fileTime.Before(oneMonthAgo) {
				continue
			}
			go func(path string) {
				logger.Printf("deleting %s", path)
				if err := os.RemoveAll(path); err != nil {
					logger.Printf("error deleting %s: %s", path, err)
				}
			}(path.Join(videosDir, f.Name()))
		}
	}
	go deleteOldStuff()
	for {
		select {
		case <-ticker.C:
			deleteOldStuff()
		}
	}
}
