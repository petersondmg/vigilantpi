package main

import (
	"net/http"
	"os"
	"strings"
	"time"
)

const tpl = `
<!DOCTYPE html>
<html charset="utf-8">
<body>
	<h3 style="color:blue">VigilantPI - Admin</h3>
	<pre>Version: :version:</pre>

	<pre>IP: :ip:</pre>

	<br>
	<a href="/videos/">Videos</a>
	<hr>

	<a href="/restart" onclick="return confirm('Are you sure?')">Restart</a> | <a href="/reboot" onclick="return confirm('Are you sure?')">Reboot OS</a> | <a href="/force-reboot" style="color:red" onclick="return confirm('This may DAMAGE your system. Are you sure?')">Force Reboot OS</a> | <a href="/clearlog" onclick="return confirm('Are you sure?')">Clear log</a>


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

	http.HandleFunc("/force-reboot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
		<html>
		<body>
		<h3 style="color:red">force rebooting... waiting 60 seconds...</h3>
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
			os.Exit(2)
		}()
	})

	http.HandleFunc("/reboot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
		<html>
		<body>
		<h3 style="color:blue">rebooting... waiting 60 seconds...</h3>
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

	http.HandleFunc("/restart", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
		<html>
		<body>
		<h3 style="color:blue">restarting...</h3>
		<script>
		setTimeout(function() {
			window.location = "/";
		}, 1000*8);
		</script>		
		</body>
		</html>
		`))
		go func() {
			time.Sleep(time.Second)
			restart()
		}()
	})

	http.HandleFunc("/clearlog", func(w http.ResponseWriter, r *http.Request) {
		go clearLogs()
		time.Sleep(time.Second)
		http.Redirect(w, r, "/", 302)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		var dfOption = `<a href="/?withdf=1">Update</a>`
		if r.URL.Query().Get("withdf") != "" {
			dfOption = serverDF()
		}

		var ipsA []string
		ips, err := getLocalIP()
		if err != nil {
			logger.Printf("error getting local ip: %s", err)
		}
		for _, ip := range ips {
			ipsA = append(ipsA, ip.String())
		}

		//logger.Printf("local ip: %v", ipsA)

		replacer := strings.NewReplacer(
			":started:", started.Format(time.RubyDate),
			":date:", serverDate(),
			":df:", dfOption,
			":log:", serverLog(),
			":config:", serverConfig(),
			":version:", version,
			":ip:", strings.Join(ipsA, ""),
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
