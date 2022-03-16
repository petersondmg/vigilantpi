package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"vigilantpi/db"

	tb "gopkg.in/tucnak/telebot.v2"
)

var (
	b         *tb.Bot
	notifyCh  chan TelegramNotification
	customCMD map[string]func(m *tb.Message)
)

func init() {
	const queueSize = 20
	notifyCh = make(chan TelegramNotification, 20)
	customCMD = make(map[string]func(m *tb.Message))
}

type TelegramNotification struct {
	Text   string
	Images []string
}

func sendNotifications() {
	for {
		if b == nil {
			time.Sleep(time.Second * 10)
			continue
		}
		for msg := range notifyCh {
			photos := make([]*tb.Photo, len(msg.Images))
			for i, file := range msg.Images {
				photos[i] = &tb.Photo{File: tb.FromDisk(file)}
			}

			for _, sid := range db.GetArray("monitors", "user-monitors") {
				id, _ := strconv.Atoi(sid)
				chat := &tb.Chat{ID: int64(id)}
				b.Send(chat, msg.Text)
				for _, p := range photos {
					b.Send(chat, p)
				}
			}
		}
	}
}

func telegramNotify(msg TelegramNotification) {
	if config.TelegramBot.Token == "" {
		return
	}
	select {
	case notifyCh <- msg:
	default:
		logger.Printf("telegram queue is full, can't send %v", msg)
	}
}

func telegramNotifyf(format string, a ...interface{}) {
	telegramNotify(TelegramNotification{
		Text: fmt.Sprintf(format, a...),
	})
}

func telegramBot() {
	if config.TelegramBot.Token == "" {
		return
	}

	filter := func(update *tb.Update, b *tb.Bot) bool {
		return true
	}

	var allowed map[string]struct{}

	if l := len(config.TelegramBot.Users); l != 0 {
		allowed = make(map[string]struct{}, l)
		for _, user := range config.TelegramBot.Users {
			allowed[user] = struct{}{}
		}

		filter = func(update *tb.Update, b *tb.Bot) bool {
			var m *tb.Message
			switch {
			case update.Message != nil:
				m = update.Message
			case update.EditedMessage != nil:
				m = update.EditedMessage
			case update.ChannelPost != nil:
				m = update.ChannelPost
			case update.EditedChannelPost != nil:
				m = update.EditedChannelPost
			}

			if m == nil {
				logger.Printf("nil telegram message, not allowed")
				return false
			}

			var user string
			if m.Chat != nil {
				user = m.Chat.Username
			}
			if m.Sender != nil {
				user = m.Sender.Username
			}

			if m.UserLeft != nil && m.UserLeft.ID == b.Me.ID {
				msg := "Group removed from monitors list"
				if err := db.RemoveFromArray("monitors", strconv.Itoa(int(m.Chat.ID))); err != nil {
					logger.Printf("err removing group from monitors: %s", err)
					msg = "Sorry, something went wrong"
				}
				b.Send(m.Sender, msg)
			}

			_, ok := allowed[user]
			if !ok {
				var joined bool
				for _, u := range m.UsersJoined {
					if u.ID == b.Me.ID {
						joined = true
						break
					}
				}
				if joined || m.GroupCreated || m.SuperGroupCreated || m.ChannelCreated {
					b.Leave(m.Chat)
					b.Send(m.Sender, "You are not authorized")
				}
				logger.Printf("access denied for user %s", user)
			}

			return ok
		}
	}

	var errLogged bool

	connect := func() {
		var err error
		b, err = tb.NewBot(tb.Settings{
			Token: config.TelegramBot.Token,
			Poller: &tb.MiddlewarePoller{
				Poller: &tb.LongPoller{
					Timeout: 5 * time.Second,
				},
				Filter: func(u *tb.Update) bool {
					return filter(u, b)
				},
			},
			Reporter: func(_ error) {},
		})

		if err != nil {
			if !errLogged {
				logger.Printf("can't start telegram bot %s", err)
				errLogged = true
			}
			return
		}

		logger.Print("telegram bot started")
		errLogged = false

		var cmds []string
		c := func(cmd string) string {
			cmds = append(cmds, "👉 "+cmd)
			return cmd
		}
		custom := func(cmd string, h func(m *tb.Message)) {
			c(cmd)
			customCMD[cmd] = h
			b.Handle(cmd, h)
		}

		b.Handle(tb.OnAddedToGroup, func(m *tb.Message) {
			if err := db.AppendArray("monitors", strconv.Itoa(int(m.Chat.ID))); err != nil {
				logger.Printf("error trying to save on db: %s", err)
				b.Send(m.Sender, "Sorry, something went wrong")
				return
			}

			b.Send(m.Chat, "Group added to monitors list")
		})

		b.Handle(c("/addmonitor"), func(m *tb.Message) {
			if err := db.AppendArray("user-monitors", strconv.Itoa(int(m.Sender.ID))); err != nil {
				logger.Printf("error trying to add monitor: %s", err)
				b.Send(m.Sender, "Sorry, something went wrong")
				return
			}
			b.Send(m.Sender, "You are now a monitor")
		})

		b.Handle(c("/delmonitor"), func(m *tb.Message) {
			if err := db.RemoveFromArray("user-monitors", strconv.Itoa(int(m.Sender.ID))); err != nil {
				logger.Printf("error trying to remove monitor: %s", err)
				b.Send(m.Sender, "Sorry, something went wrong")
				return
			}
			b.Send(m.Sender, "Monitor removed")
		})

		b.Handle(c("/clearmonitors"), func(m *tb.Message) {
			if err := db.SetArray("user-monitors", []string{}); err != nil {
				logger.Printf("error trying to remove users monitors: %s", err)
				b.Send(m.Sender, "Sorry, something went wrong")
			} else {
				b.Send(m.Sender, "Monitor users removed")
			}

			groups := db.GetArray("monitors")
			for _, g := range groups {
				id, _ := strconv.Atoi(g)
				b.Leave(&tb.Chat{ID: int64(id)})
			}
			if err := db.SetArray("monitors", []string{}); err != nil {
				logger.Printf("error trying to remove groups monitors: %s", err)
				b.Send(m.Sender, "Sorry, something went wrong")
			} else {
				b.Send(m.Sender, "Monitor groups removed")
			}
		})

		b.Handle(c("/testmonitors"), func(m *tb.Message) {
			telegramNotifyf("This is monitor test. If you see it its all good")
			b.Send(m.Sender, "test sent!")
		})

		b.Handle(c("/start"), func(m *tb.Message) {
			b.Send(m.Sender, fmt.Sprintf(
				"VigilantPI - %s\nstarted: %s\nnow: %s\nAdmin: http://%s\n\nYour username: %s\n\nCommands:\n\n%s",
				version,
				started.Format("15:04:05 - 02/01/2006"),
				serverDate(),
				localIP(),
				m.Sender.Username,
				strings.Join(cmds, "\n\n"),
			))
		})

		b.Handle(c("/monitors"), func(m *tb.Message) {
			b.Send(m.Sender, fmt.Sprintf(
				"*Admin:*\n\n%s\n\n*Group Monitors:*\n\n%s\n\n*User Monitors:*\n\n%s",
				strings.Join(config.TelegramBot.Users, ", "),
				strings.Join(db.GetArray("monitors"), ", "),
				strings.Join(db.GetArray("user-monitors"), ", "),
			))
		})

		b.Handle(c("/log"), func(m *tb.Message) {
			b.Send(m.Sender, serverLog())
		})

		b.Handle(c("/config"), func(m *tb.Message) {
			b.Send(m.Sender, serverConfig())
		})

		b.Handle(c("/storage"), func(m *tb.Message) {
			b.Send(m.Sender, "Wait...", tb.Silent)
			b.Send(m.Sender, serverDF())
		})

		b.Handle(c("/date"), func(m *tb.Message) {
			b.Send(m.Sender, serverDate())
		})

		b.Handle(c("/reboot"), func(m *tb.Message) {
			b.Send(m.Sender, "Good bye...")
			go func() {
				time.Sleep(time.Second)
				reboot()
			}()
		})

		b.Handle(c("/restart"), func(m *tb.Message) {
			b.Send(m.Sender, "Let's start again... You're welcome!")
			db.Del("pause")
			go func() {
				time.Sleep(time.Second)
				restart()
			}()
		})

		b.Handle(c("/pause"), func(m *tb.Message) {
			d, err := time.ParseDuration(m.Payload)
			if err != nil {
				b.Send(m.Sender, fmt.Sprintf("invalid duration '%s'. Ex.: /pause 10m", m.Payload))
				return
			}
			db.Set("pause", d.String())
			b.Send(m.Sender, fmt.Sprintf("pausing %s. restarting...", d))
			go func() {
				time.Sleep(time.Second)
				restart()
			}()
		})

		b.Handle(c("/resume"), func(m *tb.Message) {
			b.Send(m.Sender, serverLog())
			go func() {
				time.Sleep(time.Second)
				restart()
			}()
		})

		b.Handle(c("/tasks"), func(m *tb.Message) {
			if len(config.Tasks) == 0 {
				b.Send(m.Sender, "You have no tasks!")
				return
			}
			var msg []string
			for _, t := range config.Tasks {
				msg = append(msg, "👉 "+t.Name)
			}
			b.Send(m.Sender, fmt.Sprintf("Your tasks, sr:\n\n%s", strings.Join(msg, "\n")))
		})

		b.Handle(c("/cameras"), func(m *tb.Message) {
			if len(config.Cameras) == 0 {
				b.Send(m.Sender, "You have no cameras!")
				return
			}
			var msg []string
			for _, cam := range config.Cameras {
				msg = append(msg, click("📷 /snapshot", cam.Name))
			}
			b.Send(m.Sender, fmt.Sprintf("Your cameras, sr:\n\n%s", strings.Join(msg, "\n\n")))
		})

		custom("/snapshot", func(m *tb.Message) {
			if !config.TelegramBot.AllowSnapshots {
				b.Send(m.Sender, "Snapshots are not allowed!")
				return
			}
			cam, ok := cameraByName[m.Payload]
			if !ok {
				b.Send(m.Sender, fmt.Sprintf("You have no camera with name '%s'!", m.Payload))
				var msg []string
				for _, cam := range config.Cameras {
					msg = append(msg, click("📷 /snapshot", cam.Name))
				}
				b.Send(m.Sender, fmt.Sprintf("Your cameras, sr:\n\n%s", strings.Join(msg, "\n\n")))
				return
			}

			b.Send(m.Sender, "Taking snapshot...", tb.Silent)
			file, _, err := cam.Snapshot()
			if err != nil {
				b.Send(m.Sender, fmt.Sprintf("Error taking snapshot: %s", err))
				return
			}

			b.Send(m.Sender, "Uploading snapshot...", tb.Silent)
			photo := &tb.Photo{File: tb.FromDisk(file)}
			b.Send(m.Sender, photo)
		})

		b.Handle(tb.OnText, func(m *tb.Message) {
			if handleCustomCMD(m) {
				return
			}
			b.Send(m.Sender, fmt.Sprintf(
				"What do you mean by '"+m.Text+"'? 🤔\n\nAvailable commands:\n\n%s",
				strings.Join(cmds, "\n\n"),
			))
		})

		custom("/files", func(m *tb.Message) {
			dir := strings.TrimSpace(strings.ReplaceAll(m.Payload, "../", ""))
			if dir == "" {
				dir = "."
			}

			dirPath := path.Join(videosDir, dir)
			files, err := ioutil.ReadDir(dirPath)
			if err != nil {
				b.Send(m.Sender, fmt.Sprintf("Error opening %s: %s", dirPath, err))
				return
			}

			var list []string
			var prefix string
			var fName string
			var txt string
			var ln int

			b.Send(m.Sender, fmt.Sprintf("📂 %s:", dir))

			send := func() {
				b.Send(m.Sender, strings.Join(list, "\n\n"))
			}

			for _, f := range files {
				prefix = "📂 /files"
				fName = f.Name()
				if !f.IsDir() {
					prefix = "💾 /upload"
					strings.LastIndex(fName, ".")
				}
				txt = click(prefix, path.Join(dir, fName))
				list = append(list, txt)
				ln = ln + len(txt)
				if ln >= 3500 {
					send()
					list = []string{}
					ln = 0
				}
			}

			if ln != 0 {
				send()
			}
		})

		custom("/remove", func(m *tb.Message) {
			file := path.Join(videosDir, strings.TrimSpace(strings.ReplaceAll(m.Payload, "../", "")))
			err := os.RemoveAll(file)
			if err != nil {
				b.Send(m.Sender, fmt.Sprintf("error removing '%s'", file))
				return
			}
			b.Send(m.Sender, fmt.Sprintf("'%s' removed", file))
		})

		custom("/upload", func(m *tb.Message) {
			if !config.TelegramBot.AllowUpload {
				b.Send(m.Sender, "Upload is not allowed!")
				return
			}

			file := path.Join(videosDir, strings.TrimSpace(strings.ReplaceAll(m.Payload, "../", "")))
			info, err := os.Stat(file)
			if os.IsNotExist(err) {
				b.Send(m.Sender, fmt.Sprintf("file %s doesn't exists", file))
				return
			}
			if err != nil {
				b.Send(m.Sender, fmt.Sprintf("can't open %s: %s", file, err))
				return
			}
			if info.IsDir() {
				b.Send(m.Sender, fmt.Sprintf("can't upload %s! it's a directory", file))
				return
			}

			b.Send(m.Sender, "Uploading ...")
			doc := &tb.Document{File: tb.FromDisk(file)}
			b.Send(m.Sender, doc)
		})

		b.Start()
	}

	go sendNotifications()

	for {
		connect()
		time.Sleep(time.Second * 10)
	}
}

func cSep(str string, chr rune, add int) string {
	und := 0
	max := 1
	for _, c := range str {
		if c == chr {
			und++
			if und > max {
				max = und
			}
		} else {
			und = 0
		}
	}
	sl := max + add
	s := make([]rune, sl)
	for i := 0; i < sl; i++ {
		s[i] = chr
	}
	return string(s)
}

func click(cmd, item string) string {
	slashSep := cSep(item, '_', 1)
	dotSep := cSep(item, '0', 1)
	return strings.TrimSpace(cmd) + "_" + strings.ReplaceAll(strings.ReplaceAll(item, "/", slashSep), ".", dotSep)
}

func handleCustomCMD(m *tb.Message) bool {
	i := strings.Index(m.Text, "_")
	if i == -1 {
		return false
	}
	cmd := m.Text[0:i]
	if h, ok := customCMD[cmd]; ok {
		text := m.Text[i+1:]
		slashSep := cSep(text, '_', 0)
		dotSep := cSep(text, '0', 0)
		m.Payload = strings.ReplaceAll(strings.ReplaceAll(text, slashSep, "/"), dotSep, ".")
		h(m)
		return true
	}
	return false
}
