package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	tb "gopkg.in/tucnak/telebot.v2"
)

func telegramBot() {
	if config.TelegramBot.Token == "" {
		return
	}

	filter := func(update *tb.Update) bool {
		return true
	}

	if l := len(config.TelegramBot.Users); l != 0 {
		allowed := make(map[string]struct{}, l)
		for _, user := range config.TelegramBot.Users {
			allowed[user] = struct{}{}
		}

		filter = func(update *tb.Update) bool {
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

			_, ok := allowed[user]
			if !ok {
				logger.Printf("access denied for user %s", user)
				return false
			}
			return ok
		}
	}

	var errLogged bool

	for {
		func() {
			b, err := tb.NewBot(tb.Settings{
				Token: config.TelegramBot.Token,
				Poller: &tb.MiddlewarePoller{
					Poller: &tb.LongPoller{
						Timeout: 5 * time.Second,
					},
					Filter: filter,
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

			b.Handle(c("/start"), func(m *tb.Message) {
				b.Send(m.Sender, fmt.Sprintf(
					"VigilantPI - %s\nstarted: %s - now: %s\n\nYour number: %s",
					version,
					started.Format(time.RubyDate),
					serverDate(),
					m.Sender.Username,
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

			b.Start()
		}()

		time.Sleep(time.Second * 10)
	}
}
