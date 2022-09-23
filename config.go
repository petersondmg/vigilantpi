package main

import (
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// Config yaml ...
type Config struct {
	FFMPEG             string        `yaml:"ffmpeg"`
	MountDir           string        `yaml:"mount_dir"`
	MountDev           string        `yaml:"mount_dev"`
	MountLabel         string        `yaml:"mount_label"`
	PreventHDDSpindown bool          `yaml:"prevent_hdd_spindown"`
	TerminationTimeout time.Duration `yaml:"termination_timeout"`

	HealthCheckURL string `yaml:"health_check_url"`

	Admin struct {
		User string `yaml:"user"`
		Pass string `yaml:"pass"`
		Addr string `yaml:"addr"`
	} `yaml:"admin"`

	VideosDir string        `yaml:"videos_dir"`
	Duration  time.Duration `yaml:"duration"`
	Cameras   []Camera      `yaml:"cameras"`

	DeleteAfterDays int `yaml:"delete_after_days"`

	DailyBackup struct {
		ScpURL    string `yaml:"scp_url"`
		PublicKey string `yaml:"public_key"`
	} `yaml:"daily_backup"`

	RaspberryPI struct {
		LEDPin int `yaml:"led_pin"`
	} `yaml:"raspberry_pi"`

	WifiSSID string `yaml:"wifi_ssid"`
	WifiPass string `yaml:"wifi_pass"`

	Cron []Cron `yaml:"cron"`

	Tasks Tasks `yaml:"tasks"`

	TelegramBot struct {
		Token          string   `yaml:"token"`
		Users          []string `yaml:"users"`
		AllowSnapshots bool     `yaml:"allow_snapshots"`
		AllowUpload    bool     `yaml:"allow_upload"`
	} `yaml:"telegram_bot"`

	Debug bool `yaml:"debug"`
}

func updateConfig() {
	newConfig := path.Join(videosDir, "config.yaml")
	oldConfig := path.Join(videosDir, "config.old.yaml")
	newConfigBkp := path.Join(videosDir, "config.bkp.yaml")
	f, err := os.Open(newConfig)
	if err != nil {
		logger.Println("no config to update", err)
		return
	}

	c := new(Config)
	err = yaml.NewDecoder(f).Decode(c)
	if err != nil {
		logger.Println("new config is invalid...wont update:", err)
		return
	}

	oldBackupFile, err := os.OpenFile(oldConfig, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0755)
	if err != nil {
		logger.Printf("error creating config.old.yaml (backup): %s", err)
	} else {
		err = yaml.NewEncoder(oldBackupFile).Encode(config)
		if err != nil {
			logger.Println("error writing on config.old.yaml (backup)")
		}
		oldBackupFile.Close()
	}

	currentFile, err := os.OpenFile(configPath, os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		logger.Println("wont udpate...error opening current config.yaml", err)
		return
	}
	defer currentFile.Close()
	if err = os.Rename(newConfig, newConfigBkp); err != nil {
		logger.Println("error renaming config.yaml to config.bpk.yaml on videos dir")
	}
	ssid := c.WifiSSID
	pass := c.WifiPass

	c.WifiSSID = ""
	c.WifiPass = ""

	err = yaml.NewEncoder(currentFile).Encode(c)
	if err == nil {
		err = currentFile.Close()
	}
	if err != nil {
		logger.Println("error updating config.yaml", err)
		return
	}
	logger.Println("config.yaml updated")

	led.Confirm()

	if ssid != "" {
		setWifi(ssid, pass)
		reboot()
		return
	}

	// only restart
	os.Exit(0)
}

func tryRollback() {
	configBkp := path.Join(videosDir, "config.bkp.yaml")
	f, err := os.Open(configBkp)
	if err != nil {
		return
	}
	defer f.Close()
	logger.Println("config.bkp.yaml found, trying to restore...")

	var c Config
	err = yaml.NewDecoder(f).Decode(&c)
	if err != nil {
		logger.Printf("err parsing config.bkp.yaml: %s", err)
		return
	}

	currentFile, err := os.OpenFile(configPath, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0755)
	if err != nil {
		logger.Println("wont udpate...error opening current config.yaml", err)
		return
	}

	defer func() {
		err = currentFile.Close()
		if err != nil {
			logger.Printf("err closing config.yaml: %s", err)
		}
	}()

	err = yaml.NewEncoder(currentFile).Encode(c)
	if err != nil {
		logger.Printf("err encoding config.yaml: %s", err)
		return
	}

	reboot()
}

func serverConfig() string {
	b, _ := yaml.Marshal(config)
	return string(b)
}

func loadConfig() {
	if configPath = os.Getenv("CONFIG"); configPath == "" {
		logger.Println("no CONFIG env, using default value")
		configPath = "./config.yaml"
	}
	f, err := os.Open(configPath)
	if err != nil {
		logger.Printf("error reading config.yaml: %s", err)
		tryRollback()
		panic(err)
	}

	c := new(Config)
	err = yaml.NewDecoder(f).Decode(c)
	f.Close()
	if err != nil {
		logger.Printf("error parsing config.yaml: %s", err)
		tryRollback()
		panic(err)
	}

	config = c
	confReplacement()
}

var (
	confReplacer map[string]string
)

func confReplacement() {
	confReplacer = make(map[string]string)

	for _, c := range config.Cameras {
		key := func(k string) string {
			return "cameras." + c.Name + "." + k
		}
		confReplacer[key("name")] = c.Name
		confReplacer[key("url.raw")] = c.URL
		u, _ := url.Parse(c.URL)
		if u == nil {
			continue
		}

		confReplacer[key("url")] = u.String()
		confReplacer[key("url.scheme")] = u.Scheme
		confReplacer[key("url.host")] = u.Host
		confReplacer[key("url.query")] = u.RawQuery
		confReplacer[key("url.hostname")] = u.Hostname()
		confReplacer[key("url.request_uri")] = u.RequestURI()

		if u.User == nil {
			continue
		}
		confReplacer[key("url.username")] = u.User.Username()
		confReplacer[key("url.password")], _ = u.User.Password()
	}
}

var (
	confReplaceRE = regexp.MustCompile(`\$\{\{ *([^}])+ *\}\}`)
)

func replaceWithConf(str string) string {
	now := time.Now()
	confReplacer["_now"] = now.Format("2006_01_02_15_04_05")
	confReplacer["now"] = now.Format("2006-01-02 15:04:05")

	return confReplaceRE.ReplaceAllStringFunc(str, func(token string) string {
		key := strings.Trim(token, "${{}} ")
		val, ok := confReplacer[key]
		if !ok {
			logger.Printf("bad substitution. key %s doesn't exists", key)
			return token
		}
		return val
	})
}
