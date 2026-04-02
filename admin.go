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
	<pre id="logs">:log:</pre>
	<hr>
	<br>

	<h4>Config</h4>
	<pre>:config:</pre>
	<hr>
	<br>

	<script>
		function updateLogs() {
			fetch('/log-raw')
				.then(response => response.text())
				.then(data => {
					document.getElementById('logs').innerText = data;
				});
		}
		setInterval(updateLogs, 3000);
	</script>
</body>
</html>
`

func httpServer(addr, user, pass string) {
	mux := http.NewServeMux()

	fs := http.FileServer(http.Dir(config.VideosDir))
	mux.Handle("/videos/", http.StripPrefix("/videos/", fs))

	mux.HandleFunc("/log-raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(serverLog()))
	})

	mux.HandleFunc("/force-reboot", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/reboot", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/restart", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/clearlog", func(w http.ResponseWriter, r *http.Request) {
		go clearLogs()
		time.Sleep(time.Second)
		http.Redirect(w, r, "/", 302)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		var dfOption = `<a href="/?withdf=1">Update</a>`
		if r.URL.Query().Get("withdf") != "" {
			dfOption = serverDF()
		}

		//logger.Printf("local ip: %v", ipsA)

		replacer := strings.NewReplacer(
			":started:", started.Format(time.RubyDate),
			":date:", serverDate(),
			":df:", dfOption,
			":log:", serverLog(),
			":config:", serverConfig(),
			":version:", version,
			":ip:", localIP(),
		)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(replacer.Replace(tpl)))
	})

	if addr == "" {
		addr = ":80"
	}
	logger.Printf("starting admin server on %s", addr)
	err := http.ListenAndServe(addr, auth(user, pass, mux))
	if err != nil {
		logger.Printf("error on http server: %s", err)
	}
}

func auth(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" || pass != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != user || p != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized.", 401)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
