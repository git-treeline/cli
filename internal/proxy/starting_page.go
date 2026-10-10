package proxy

import (
	"html/template"
	"net/http"
)

// statusPageData drives the self-contained status template.
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

func toneClasses(tone string) string {
	switch tone {
	case "amber":
		return "tone-amber"
	case "rose":
		return "tone-rose"
	default:
		return "tone-slate"
	}
}

var statusPageTemplate = template.Must(template.New("status").Funcs(template.FuncMap{
	"toneClasses": toneClasses,
}).Parse(statusPageHTML))

// statusPageHTML is intentionally readable: served once per click, no
// build step, external assets, or third-party script authority. The CSS is
// dark-mode aware via prefers-color-scheme.
const statusPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · {{.Subdomain}}</title>
{{if .Refresh}}<meta http-equiv="refresh" content="{{.Refresh}}">{{end}}
<style>
  :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #f8fafc; color: #0f172a; -webkit-font-smoothing: antialiased; }
  main { width: min(28rem, calc(100% - 2rem)); }
  .card { padding: 2rem; border: 1px solid #e2e8f0; border-radius: 1rem; background: #fff; box-shadow: 0 1px 2px rgb(15 23 42 / .06); }
  .header, .footer { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
  .title-wrap { min-width: 0; }
  h1 { margin: 0; font-size: 1.25rem; line-height: 1.75rem; font-weight: 600; letter-spacing: -.025em; }
  .subdomain { margin: .25rem 0 0; overflow: hidden; color: #64748b; font: .875rem/1.25rem ui-monospace, SFMono-Regular, Menlo, monospace; text-overflow: ellipsis; white-space: nowrap; }
  .badge { flex: none; display: inline-flex; align-items: center; gap: .375rem; padding: .25rem .625rem; border-radius: 9999px; font-size: .75rem; line-height: 1rem; font-weight: 500; box-shadow: inset 0 0 0 1px var(--ring); background: var(--badge-bg); color: var(--badge-fg); }
  .badge-dot { width: .375rem; height: .375rem; border-radius: 9999px; background: var(--dot); }
  .tone-amber { --badge-bg: #fef3c7; --badge-fg: #92400e; --ring: #fde68a; --dot: #f59e0b; }
  .tone-rose { --badge-bg: #ffe4e6; --badge-fg: #9f1239; --ring: #fecdd3; --dot: #f43f5e; }
  .tone-slate { --badge-bg: #f1f5f9; --badge-fg: #334155; --ring: #e2e8f0; --dot: #94a3b8; }
  .pulse { animation: pulse 2s cubic-bezier(.4,0,.6,1) infinite; }
  .message { margin: 1.25rem 0 0; color: #334155; line-height: 1.625; }
  .hint { margin: 1rem 0 0; color: #475569; font-size: .875rem; line-height: 1.25rem; }
  .command { position: relative; margin-top: .75rem; }
  pre { margin: 0; padding: .5rem .75rem; overflow-x: auto; border-radius: .5rem; background: #f1f5f9; color: #1e293b; font: .875rem/1.25rem ui-monospace, SFMono-Regular, Menlo, monospace; }
  button { position: absolute; top: .375rem; right: .375rem; padding: .125rem .5rem; border: 1px solid #e2e8f0; border-radius: .375rem; background: #fff; color: #475569; cursor: pointer; font-family: inherit; font-size: .75rem; line-height: 1rem; opacity: 0; transition: opacity .15s; }
  .command:hover button, button:focus-visible { opacity: 1; }
  .elapsed { margin: 1.25rem 0 0; color: #64748b; font-size: .75rem; line-height: 1rem; }
  .footer { align-items: center; margin-top: 1.5rem; padding-top: 1.25rem; border-top: 1px solid #f1f5f9; color: #94a3b8; font-size: .75rem; line-height: 1rem; }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .local-note { margin: .75rem 0 0; color: #94a3b8; text-align: center; font-size: .75rem; line-height: 1rem; }
  @keyframes pulse { 50% { opacity: .45; } }
  @media (prefers-color-scheme: dark) {
    body { background: #020617; color: #f1f5f9; }
    .card { border-color: #1e293b; background: #0f172a; box-shadow: none; }
    .subdomain, .hint, .elapsed { color: #94a3b8; }
    .message { color: #cbd5e1; }
    .tone-amber { --badge-bg: rgb(120 53 15 / .4); --badge-fg: #fde68a; --ring: #92400e; }
    .tone-rose { --badge-bg: rgb(136 19 55 / .4); --badge-fg: #fecdd3; --ring: #9f1239; }
    .tone-slate { --badge-bg: #1e293b; --badge-fg: #cbd5e1; --ring: #334155; }
    pre { background: rgb(30 41 59 / .8); color: #e2e8f0; }
    button { border-color: #334155; background: #0f172a; color: #cbd5e1; }
    .footer { border-color: #1e293b; color: #64748b; }
    .local-note { color: #475569; }
  }
</style>
</head>
<body>
  <main>
    <div class="card">
      <div class="header">
        <div class="title-wrap">
          <h1>{{.Title}}</h1>
          <p class="subdomain">{{.Subdomain}}</p>
        </div>
        <span class="badge {{toneClasses .Tone}}">
          <span class="badge-dot {{if eq .Tone "amber"}}pulse{{end}}"></span>
          {{.StatusBadge}}
        </span>
      </div>

      <p class="message">{{.Message}}</p>

      {{if .Hint}}<p class="hint">{{.Hint}}</p>{{end}}

      {{if .Command}}
      <div class="command">
        <pre><code id="cmd">{{.Command}}</code></pre>
        <button type="button"
                onclick="navigator.clipboard.writeText(document.getElementById('cmd').textContent).then(()=>{const b=event.currentTarget;b.textContent='copied';setTimeout(()=>b.textContent='copy',1200)})">copy</button>
      </div>
      {{end}}

      {{if .ShowElapsed}}
      <p id="elapsed" class="elapsed" aria-live="polite">Waiting…</p>
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

      <div class="footer">
        <span>git-treeline router</span>
        <span class="mono">port {{.Port}}</span>
      </div>
    </div>
    <p class="local-note">This page is served by your local <code class="mono">gtl serve</code> router.</p>
  </main>
</body>
</html>
`
