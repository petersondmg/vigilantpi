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
	b *tb.Bot
)

func telegramNotify(str string) {
	telegramNotifyf(str)
}

func telegramNotifyf(format string, a ...interface{}) {
	if config.TelegramBot.Token == "" {
		return
	}
	for _, sid := range db.GetArray("monitors", "user-monitors") {
		id, _ := strconv.Atoi(sid)
		b.Send(&tb.Chat{ID: int64(id)}, fmt.Sprintf(format, a...))
	}
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
		telegramNotify("This is monitor test. If you see it its all good")
		b.Send(m.Sender, "test sent!")
	})

	b.Handle(c("/start"), func(m *tb.Message) {
		b.Send(m.Sender, fmt.Sprintf(
			"VigilantPI - %s\nstarted: %s - now: %s\n\nYour number: %s",
			version,
			started.Format(time.RubyDate),
			serverDate(),
			m.Sender.Username,
		))
	})

	b.Handle(c("/admins"), func(m *tb.Message) {
		b.Send(m.Sender, fmt.Sprintf(
			"**Admin:**\n\n%s\n\n**Group Monitors:**\n\n%s\n\n**User Monitors:**\n\n%s",
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
			msg = append(msg, "📷 /snapshot "+cam.Name)
		}
		b.Send(m.Sender, fmt.Sprintf("Your cameras, sr:\n\n%s", strings.Join(msg, "\n\n")))
	})

	b.Handle(c("/snapshot"), func(m *tb.Message) {
		cam, ok := cameraByName[m.Payload]
		if !ok {
			b.Send(m.Sender, fmt.Sprintf("You have no camera with name '%s'!", m.Payload))
			var msg []string
			for _, cam := range config.Cameras {
				msg = append(msg, "📷 /snapshot "+cam.Name)
			}
			b.Send(m.Sender, fmt.Sprintf("Your cameras, sr:\n\n%s", strings.Join(msg, "\n\n")))
			return
		}

		b.Send(m.Sender, "Taking snapshot...")
		file, err := cam.Snapshot()
		if err != nil {
			b.Send(m.Sender, fmt.Sprintf("Error taking snapshot: %s", err))
			return
		}

		b.Send(m.Sender, "Uploading snapshot...")
		photo := &tb.Photo{File: tb.FromDisk(file)}
		b.Send(m.Sender, photo)
	})

	b.Handle(tb.OnText, func(m *tb.Message) {
		b.Send(m.Sender, fmt.Sprintf(
			"What do you mean by '"+m.Text+"'? 🤔\n\nAvailable commands:\n\n%s",
			strings.Join(cmds, "\n\n"),
		))
	})

	b.Handle(c("/files"), func(m *tb.Message) {
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
		for _, f := range files {
			prefix = "💾 /upload "
			if f.IsDir() {
				prefix = "📂 /files "
			}
			list = append(list, prefix+path.Join(dir, f.Name()))
		}

		b.Send(m.Sender, fmt.Sprintf("📂 %s:\n\n%s", dir, strings.Join(list, "\n\n")))
	})

	b.Handle(c("/upload"), func(m *tb.Message) {
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

	for {
		b.Start()
		time.Sleep(time.Second * 10)
	}
}
