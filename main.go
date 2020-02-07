package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/sparrc/go-ping"
	"github.com/stianeikeland/go-rpio/v4"
)

// Config yaml ...
type Config struct {
	FFMPEG   string `yaml:"ffmpeg"`
	MountDir string `yaml:"mount_dir"`
	MountDev string `yaml:"mount_dev"`
	Admin    struct {
		User string `yaml:"user"`
		Pass string `yaml:"pass"`
		Addr string `yaml:"addr"`
	} `yaml:"admin"`
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
	WifiSSID string `yaml:"wifi_ssid"`
	WifiPass string `yaml:"wifi_pass"`
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
				"camera %s in not responding. ping stats - sent: %d, recv: %d, loss: %v%%",
				c.Name,
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss,
			)
			led.BadCamera()
			return
		}

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

	go p()

	return func() {
		t.Stop()
		stop <- struct{}{}
	}
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

const (
	logPath = "/home/alarm/vigilantpi.log"
)

var (
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

	preRecClient = http.Client{
		Timeout: time.Second * 60,
	}

	started = time.Now()

	config *Config
)

func main() {
	kill := make(chan os.Signal, 1)
	signal.Notify(kill, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})

	go func() {
		<-kill
		done <- struct{}{}
	}()

	logger = log.New(os.Stdout, "", log.LstdFlags)

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

	go httpServer(c.Admin.Addr, c.Admin.User, c.Admin.Pass)

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

	led.BadHD()

	mountedDir = safeShell(c.MountDir)
	mountDev = safeShell(c.MountDev)

	logger.Println("started!")

	go run(c.Cameras)

	<-done
	logger.Println("finished")
}

const (
	dayDirLayout = "rec_2006_01_02"
)

func record(c Camera) {
	start := time.Now()
	dayDir := start.Format(dayDirLayout)
	fileName := start.Format("15_04_05_") + c.Name + ".mp4"

	if !hddIsMounted() {
		logger.Println("can't record: hdd is not mounted")
		led.BadHD()
		tryMount()
		return
	}

	recDir := path.Join(videosDir, dayDir)
	if err := os.MkdirAll(recDir, 0774); err != nil {
		logger.Printf("error creating recording directory %s: %s", recDir, err)
		led.BadHD()
		return
	}

	stopCheck := c.HealthCheck()
	defer stopCheck()

	c.RunPreRecURLs()

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
		//sets duration
		"-to",
		strconv.Itoa(int(duration.Seconds())),
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
		led.BadCamera()
		return
	}

	timeout := time.NewTimer(duration + (time.Second * 30))
	defer timeout.Stop()

	exited := make(chan struct{})

	go func() {
		state, err := p.Wait()
		if err != nil {
			logger.Printf("error getting proccess state %s - %s", c.Name, err)
			return
		}
		if !state.Exited() {
			logger.Printf("p.Wait() returned but process hasn't exited for %s. killing process...", c.Name)
			p.Kill()
		}
		exited <- struct{}{}
	}()

	select {
	case <-timeout.C:
		logger.Printf("recording process of %s has timeout. killing process...", c.Name)
		p.Signal(os.Interrupt)
		p.Kill()
		return

	case <-exited:
	}

	logger.Printf("recording %s took %s\n", c.Name, time.Now().Sub(start))
}

func errIsNil(err error) {
	if err != nil {
		logger.Fatal(err)
	}
}

func updater() {

}

const (
	blinkInterval = time.Second
)

func setupLED(ledPin int) func() error {
	pin := rpio.Pin(ledPin)
	if err := rpio.Open(); err != nil {
		logger.Println("Error setuping LED:", err)
		return func() error {
			return nil
		}
	}
	pin.Output()

	var ticker *time.Ticker
	times := make(chan int)
	stop := make(chan struct{})

	go func() {
		for blinks := range times {
			if ticker != nil {
				stop <- struct{}{}
				ticker.Stop()
				ticker = nil
			}

			if blinks == 0 {
				continue
			}

			ticker = time.NewTicker(blinkInterval)
			go func() {
				var on bool
				pin.Low()

				for {
					select {
					case <-ticker.C:
						if on {
							on = false
							pin.Low()
							continue
						}
						on = true

						iterations := blinks * 2
						stateDuration := blinkInterval / time.Duration(iterations)

						for i := 0; i < iterations; i++ {
							pin.Toggle()
							time.Sleep(stateDuration)
						}
					case <-stop:
						return
					}
				}
			}()
		}
	}()

	led.BadHD = func() {
		times <- 1
	}
	led.BadCamera = func() {
		times <- 2
	}
	led.BadNetwork = func() {
		times <- 3
	}
	led.Confirm = func() {
		times <- 0
		for i := 0; i < 10; i++ {
			pin.Toggle()
			time.Sleep(time.Millisecond * 200)
		}
	}
	led.On = func() {
		times <- 0
		pin.High()
	}
	led.Off = func() {
		times <- 0
		pin.Low()
	}
	return func() error {
		times <- 0
		led.Off()
		return rpio.Close()
	}
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
	}

	reboot()
}

func setWifi(ssid, pass string) {
	logger.Println("setting wifi to", ssid, pass)
	_, err := exec.Command("sh", "-c", fmt.Sprintf("wpa_passphrase '%s' '%s' > /etc/wpa_supplicant/wpa_supplicant-wlan0.conf", ssid, pass)).Output()
	if err != nil {
		logger.Println("error wpa_passphrase cmd", err)
		return
	}
	logger.Println("wifi updated")
}

func run(cameras []Camera) {
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

	go every24Hours()

	go updater()

	rec := make(chan Camera, 0)
	go func() {
		for c := range rec {
			c := c
			go func() {
				record(c)
				rec <- c
			}()
		}
	}()

	for _, camera := range cameras {
		rec <- camera
	}
}

const tpl = `
<!DOCTYPE html>
<html charset="utf-8">
<body>
	<h3 style="color:blue">VigilantPI - Admin</h3>

	<br>
	<a href="/videos/">Videos</a>
	<hr>

	<a href="/reboot" onclick="confirm('Are you sure?')">Reboot</a> | <a href="/clearlog" onclick="confirm('Are you sure?')">Clear log</a>


	<h4>Server Date</h4>
	<pre>:date:</pre>
	<pre>Up since: :started:</pre>
	<hr>
	<br>

	<h4>DF (disk space)</h4>
	<pre>:df:</pre>
	<hr>
	<br>

	<h4>Log</h4>
	<pre>:log:</pre>
	<hr>
	<br>

	<h4>Config</h4>
	<pre>:config:</pre>
	<hr>
	<br>

</body>
</html>
`

func httpServer(addr, user, pass string) {
	fs := http.FileServer(http.Dir(config.VideosDir))
	http.Handle("/videos/", http.StripPrefix("/videos/", fs))

	http.HandleFunc("/reboot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
		<html>
		<body>
		<h3 style="color:blue">restarting... waiting 60 seconds...</h3>
		<script>
		setTimeout(function() {
			window.location = "/";
		}, 1000*60);
		</script>		
		</body>
		</html>
		`))
		go func() {
			time.Sleep(time.Second)
			reboot()
		}()
	})

	http.HandleFunc("/clearlog", func(w http.ResponseWriter, r *http.Request) {
		go clearLogs()
		time.Sleep(time.Second)
		http.Redirect(w, r, "/", 302)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		replacer := strings.NewReplacer(
			":started:", started.Format(time.RubyDate),
			":date:", serverDate(),
			":df:", serverDF(),
			":log:", serverLog(),
			":config:", serverConfig(),
		)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(replacer.Replace(tpl)))
	})

	if addr == "" {
		addr = ":80"
	}
	logger.Printf("starting admin server on %s", addr)
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		logger.Print("error on http server: %s", err)
	}
}

func auth(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if config.Admin.User != "" || config.Admin.Pass != "" {
			user, pass, _ := r.BasicAuth()
			if user != config.Admin.User || pass != config.Admin.Pass {
				http.Error(w, "Unauthorized.", 401)
				return
			}
		}
		fn(w, r)
	}
}

func execString(cmd string, args ...string) string {
	res, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return err.Error()
	}
	return string(res)
}

func serverDate() string {
	return execString("date")
}

func serverDF() string {
	return execString("df", "-H")
}

func serverLog() string {
	return execString("tail", "-n", "50", logPath)
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

func serverConfig() string {
	b, _ := yaml.Marshal(config)
	return string(b)
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

func reboot() {
	logger.Println("rebooting...")
	_, err := exec.Command("reboot").Output()
	if err != nil {
		logger.Printf("error rebooting: %s", err)
	}
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
