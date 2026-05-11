package proxy

import (
	"html/template"
	"net/http"
	"strings"
)

// statusPageData drives the Tailwind status template.
type statusPageData struct {
	Title       string
	Subdomain   string
	Port        int
	Message     string
	Hint        template.HTML // raw HTML allowed (e.g. inline <code>)
	Command     string        // shell command to copy/run; rendered in a <pre>
	StatusBadge string        // small label shown next to subdomain (e.g. "starting")
	Tone        string        // color tone: "amber" | "slate" | "rose"
	Refresh     int           // meta refresh interval in seconds; 0 disables
	ShowElapsed bool          // whether to render the JS elapsed counter
}

// renderBackendStatusPage maps a BackendState into a fully styled status
// page and writes it to w. status is the HTTP status code to return.
func renderBackendStatusPage(w http.ResponseWriter, state BackendState, subdomain string, port int) {
	data := dataForState(state, subdomain, port)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch state {
	case BackendStarting:
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("Retry-After", "2")
	case BackendNotStarted, BackendStopped:
		w.WriteHeader(http.StatusServiceUnavailable)
	case BackendUnreachable, BackendUnknown:
		w.WriteHeader(http.StatusBadGateway)
	default:
		w.WriteHeader(http.StatusBadGateway)
	}

	_ = statusPageTemplate.Execute(w, data)
}

func dataForState(state BackendState, subdomain string, port int) statusPageData {
	d := statusPageData{
		Subdomain: subdomain,
		Port:      port,
	}
	switch state {
	case BackendStarting:
		d.Title = "Starting up"
		d.StatusBadge = "starting"
		d.Tone = "amber"
		d.Message = "Your dev server is booting. This page will refresh automatically once it's ready."
		d.Refresh = 2
		d.ShowElapsed = true
	case BackendNotStarted:
		d.Title = "Server not started"
		d.StatusBadge = "idle"
		d.Tone = "slate"
		d.Message = "The router has a route for this worktree but nothing's running on its port yet."
		d.Hint = template.HTML("Start the server from the worktree directory:")
		d.Command = "gtl start"
		d.Refresh = 10
	case BackendStopped:
		d.Title = "Server stopped"
		d.StatusBadge = "stopped"
		d.Tone = "rose"
		d.Message = "The supervisor is up but the server isn't running. It may have crashed or been stopped manually."
		d.Hint = template.HTML("Restart it and check the logs if it keeps exiting:")
		d.Command = "gtl start"
	case BackendUnreachable:
		d.Title = "Backend unreachable"
		d.StatusBadge = "error"
		d.Tone = "rose"
		d.Message = "The router couldn't proxy this request even though the port was listening. The connection was dropped mid-request."
	default:
		d.Title = "Backend unreachable"
		d.StatusBadge = "unknown"
		d.Tone = "slate"
		d.Message = "The router couldn't determine what's wrong with this backend."
	}
	return d
}

// toneClasses returns the Tailwind utility classes for the small status
// badge based on tone. Kept as a template function so the HTML stays
// declarative.
func toneClasses(tone string) string {
	switch tone {
	case "amber":
		return "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200 ring-1 ring-amber-200 dark:ring-amber-800"
	case "rose":
		return "bg-rose-100 text-rose-800 dark:bg-rose-900/40 dark:text-rose-200 ring-1 ring-rose-200 dark:ring-rose-800"
	default:
		return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300 ring-1 ring-slate-200 dark:ring-slate-700"
	}
}

func toneDot(tone string) string {
	switch tone {
	case "amber":
		return "bg-amber-500"
	case "rose":
		return "bg-rose-500"
	default:
		return "bg-slate-400"
	}
}

var statusPageTemplate = template.Must(template.New("status").Funcs(template.FuncMap{
	"toneClasses": toneClasses,
	"toneDot":     toneDot,
	"trimSpace":   strings.TrimSpace,
}).Parse(statusPageHTML))

// statusPageHTML is intentionally readable: served once per click, no
// build step, dark-mode aware via prefers-color-scheme. Tailwind's play
// CDN handles class compilation in the browser.
const statusPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · {{.Subdomain}}</title>
{{if .Refresh}}<meta http-equiv="refresh" content="{{.Refresh}}">{{end}}
<script src="https://cdn.tailwindcss.com"></script>
<script>tailwind.config={darkMode:'media'}</script>
</head>
<body class="min-h-screen flex items-center justify-center bg-slate-50 dark:bg-slate-950 text-slate-900 dark:text-slate-100 font-sans antialiased">
  <main class="w-full max-w-md mx-4">
    <div class="rounded-2xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 shadow-sm p-8">
      <div class="flex items-start justify-between gap-4 mb-5">
        <div class="min-w-0">
          <h1 class="text-xl font-semibold tracking-tight">{{.Title}}</h1>
          <p class="mt-1 text-sm font-mono text-slate-500 dark:text-slate-400 truncate">{{.Subdomain}}</p>
        </div>
        <span class="shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium {{toneClasses .Tone}}">
          <span class="w-1.5 h-1.5 rounded-full {{toneDot .Tone}} {{if eq .Tone "amber"}}animate-pulse{{end}}"></span>
          {{.StatusBadge}}
        </span>
      </div>

      <p class="text-slate-700 dark:text-slate-300 leading-relaxed">{{.Message}}</p>

      {{if .Hint}}<p class="mt-4 text-sm text-slate-600 dark:text-slate-400">{{.Hint}}</p>{{end}}

      {{if .Command}}
      <div class="mt-3 group relative">
        <pre class="px-3 py-2 rounded-lg bg-slate-100 dark:bg-slate-800/80 text-sm font-mono text-slate-800 dark:text-slate-200 overflow-x-auto"><code id="cmd">{{.Command}}</code></pre>
        <button type="button"
                onclick="navigator.clipboard.writeText(document.getElementById('cmd').textContent).then(()=>{const b=event.currentTarget;b.textContent='copied';setTimeout(()=>b.textContent='copy',1200)})"
                class="absolute top-1.5 right-1.5 px-2 py-0.5 text-xs rounded-md bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-700 opacity-0 group-hover:opacity-100 transition-opacity">copy</button>
      </div>
      {{end}}

      {{if .ShowElapsed}}
      <p id="elapsed" class="mt-5 text-xs text-slate-500 dark:text-slate-400" aria-live="polite">Waiting…</p>
      <script>
        (function(){
          var start = Date.now();
          function tick(){
            var s = Math.floor((Date.now()-start)/1000);
            var el = document.getElementById('elapsed');
            if (el) el.textContent = 'Retrying every 2s · waiting ' + s + 's';
          }
          tick();
          setInterval(tick, 1000);
        })();
      </script>
      {{end}}

      <div class="mt-6 pt-5 border-t border-slate-100 dark:border-slate-800 flex items-center justify-between text-xs text-slate-400 dark:text-slate-500">
        <span>git-treeline router</span>
        <span class="font-mono">port {{.Port}}</span>
      </div>
    </div>
    <p class="mt-3 text-center text-xs text-slate-400 dark:text-slate-600">This page is served by your local <code class="font-mono">gtl serve</code> router.</p>
  </main>
</body>
</html>
`
