package main

import (
	"io/ioutil"
	"os"
	"path"
	"time"
)

// Cron ...
type Cron struct {
	Every time.Duration `yaml:"every"`
	//At    time.Time     `yaml:"at"`
	Tasks []string `yaml:"tasks"`
}

func crond(entries []Cron) {
	if len(entries) == 0 {
		return
	}
	logger.Println("setuping cron")
	for _, cron := range entries {
		cron := cron
		go func() {
			for range time.Tick(cron.Every) {
				for _, taskName := range cron.Tasks {
					if task, ok := taskByName[taskName]; ok {
						task.Run(nil)
						continue
					}
					logger.Printf("invalid cron task. task %s was not declared", taskName)
				}
			}
		}()
		logger.Printf("%s scheduled to every %s", cron.Tasks, cron.Every)
	}
}

func oldFilesWatcher(days int) {
	if days <= 0 {
		days = 20
	}

	ticker := time.NewTicker(6 * time.Hour)
	deleteOldStuff := func() {
		logger.Println("checking old content")
		files, err := ioutil.ReadDir(videosDir)
		if err != nil {
			logger.Printf("error getting files on %s when deleting old content: %s", videosDir, err)
			return
		}

		periodAgo := time.Now().AddDate(0, 0, -days)
		logger.Println("deleting files older than", periodAgo.Format("02/01/2006"))

		for _, f := range files {
			if !f.IsDir() {
				continue
			}
			fileTime, err := time.Parse(dayDirLayout, f.Name())
			if err != nil {
				continue
			}
			if !fileTime.Before(periodAgo) {
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

	for range ticker.C {
		deleteOldStuff()
	}
}
