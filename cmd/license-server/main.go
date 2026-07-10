package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/onlinelicense"
)

const defaultPort = 24220

func main() {
	port := flag.Int("port", envInt("CCNEXUS_LICENSE_PORT", defaultPort), "license server port")
	bind := flag.String("bind", envString("CCNEXUS_LICENSE_BIND", "0.0.0.0"), "bind address")
	dbPath := flag.String("db", envString("CCNEXUS_LICENSE_DB_PATH", defaultDBPath()), "SQLite database path")
	flag.Parse()

	store, err := onlinelicense.NewSQLiteStore(*dbPath)
	if err != nil {
		log.Fatalf("open license db: %v", err)
	}
	defer store.Close()

	privateKey, publicKey, err := loadOrCreatePrivateKey()
	if err != nil {
		log.Fatalf("load signing key: %v", err)
	}
	admin := onlinelicense.AdminConfig{
		Username: envString("CCNEXUS_LICENSE_ADMIN_USERNAME", "admin"),
		Password: os.Getenv("CCNEXUS_LICENSE_ADMIN_PASSWORD"),
	}
	if strings.TrimSpace(admin.Password) == "" {
		log.Fatal("CCNEXUS_LICENSE_ADMIN_PASSWORD is required")
	}

	service := onlinelicense.NewService(store, privateKey, onlinelicense.Options{
		RemoteSecretRevealEnabled: envBool("CCNEXUS_LICENSE_REMOTE_SECRET_REVEAL_ENABLED"),
	})
	if _, err := service.EnsureBootstrapAdmin(admin.Username, admin.Password); err != nil {
		log.Fatalf("bootstrap admin account: %v", err)
	}
	apiHandler := onlinelicense.NewHTTPHandler(service, admin)
	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)
	mux.HandleFunc("/admin/login", loginPage)
	mux.Handle("/admin/", apiHandler.AdminPageMiddleware(http.HandlerFunc(adminPage)))
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf("%s:%d", *bind, *port)
	log.Printf("AINexus license server listening on %s", addr)
	log.Printf("admin: http://%s/admin/", addr)
	log.Printf("public key for client builds: %s", base64.StdEncoding.EncodeToString(publicKey))
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server stopped: %v", err)
	}
}

func loadOrCreatePrivateKey() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if encoded := strings.TrimSpace(os.Getenv("CCNEXUS_LICENSE_PRIVATE_KEY")); encoded != "" {
		privateKey, err := onlinelicense.PrivateKeyFromString(encoded)
		if err != nil {
			return nil, nil, err
		}
		publicKey, _ := privateKey.Public().(ed25519.PublicKey)
		return privateKey, publicKey, nil
	}
	keyPath := envString("CCNEXUS_LICENSE_KEY_PATH", filepath.Join(defaultDataDir(), "private_key.txt"))
	if data, err := os.ReadFile(keyPath); err == nil {
		privateKey, err := onlinelicense.PrivateKeyFromString(string(data))
		if err != nil {
			return nil, nil, err
		}
		publicKey, _ := privateKey.Public().(ed25519.PublicKey)
		if err := writePublicKeyFile(keyPath, publicKey); err != nil {
			return nil, nil, err
		}
		return privateKey, publicKey, nil
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(privateKey)
	if err := os.WriteFile(keyPath, []byte(encoded), 0600); err != nil {
		return nil, nil, err
	}
	if err := writePublicKeyFile(keyPath, publicKey); err != nil {
		return nil, nil, err
	}
	return privateKey, publicKey, nil
}

func writePublicKeyFile(privateKeyPath string, publicKey ed25519.PublicKey) error {
	publicKeyPath := filepath.Join(filepath.Dir(privateKeyPath), "public_key.txt")
	return os.WriteFile(publicKeyPath, []byte(base64.StdEncoding.EncodeToString(publicKey)), 0644)
}

func defaultDBPath() string {
	return filepath.Join(defaultDataDir(), "license.db")
}

func defaultDataDir() string {
	if dataDir := strings.TrimSpace(os.Getenv("CCNEXUS_LICENSE_DATA_DIR")); dataDir != "" {
		return dataDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ccnexus-license"
	}
	return filepath.Join(home, ".ccnexus-license")
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func adminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(adminHTML))
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(loginHTML))
}

const loginHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>AINexus License Login</title>
  <style>
    :root{color-scheme:light;--bg:#f5f5f7;--panel:rgba(255,255,255,.86);--panel-solid:#fff;--line:#d7d7dd;--soft-line:#ececf0;--text:#1d1d1f;--muted:#6e6e73;--accent:#0071e3;--accent-hover:#0077ed;--danger:#b42318;--shadow:0 22px 70px rgba(29,29,31,.10)}
    *{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 18% 8%,rgba(0,113,227,.10),transparent 30%),linear-gradient(135deg,#fbfbfd 0%,var(--bg) 56%,#eef1f6 100%);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,"SF Pro Text","SF Pro Display","Helvetica Neue","Segoe UI",sans-serif;-webkit-font-smoothing:antialiased}
    .login-page{min-height:100dvh;display:grid;place-items:center;padding:28px}
    .login-card{width:min(430px,100%);background:var(--panel);border:1px solid rgba(255,255,255,.72);border-radius:8px;padding:30px;box-shadow:var(--shadow);backdrop-filter:saturate(180%) blur(22px)}
    .brand-mark{width:44px;height:44px;border-radius:12px;display:grid;place-items:center;background:linear-gradient(145deg,#0a84ff,#0071e3);color:#fff;font-weight:700;letter-spacing:-.02em;margin-bottom:18px}
    h1{margin:0;font-size:28px;line-height:1.08;font-weight:650;letter-spacing:-.03em}.login-subtitle{margin:8px 0 22px;color:var(--muted)}
    label{display:block;font-size:12px;font-weight:650;color:var(--muted);margin:14px 0 6px}
    input{width:100%;border:1px solid var(--line);border-radius:8px;padding:12px 13px;background:var(--panel-solid);color:var(--text);font:inherit;transition:border-color .18s,box-shadow .18s,background .18s}
    input:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 4px rgba(0,113,227,.13)}
    button{width:100%;margin-top:18px;border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:8px;padding:12px 14px;font-weight:650;cursor:pointer;transition:background .18s,transform .18s,box-shadow .18s}
    button:hover{background:var(--accent-hover);box-shadow:0 8px 22px rgba(0,113,227,.22)}button:active{transform:translateY(1px)}
    .error{min-height:20px;margin-top:13px;color:var(--danger);font-size:13px}.login-foot{margin-top:18px;padding-top:16px;border-top:1px solid var(--soft-line);color:var(--muted);font-size:12px}
    @media(max-width:520px){.login-page{padding:16px}.login-card{padding:24px}}
  </style>
</head>
<body>
  <main class="login-page">
    <form class="login-card" id="login-form">
      <div class="brand-mark">AI</div>
      <h1>AINexus License Admin</h1>
      <p class="login-subtitle">授权运营后台</p>
      <label for="username">账号</label>
      <input id="username" name="username" autocomplete="username" value="admin">
      <label for="password">密码</label>
      <input id="password" name="password" type="password" autocomplete="current-password" autofocus>
      <button type="submit">登录</button>
      <div id="error" class="error"></div>
      <div class="login-foot">卡密、设备激活与运营审计集中管理</div>
    </form>
  </main>
  <script>
    const form = document.getElementById('login-form');
    const error = document.getElementById('error');
    form.addEventListener('submit', async event => {
      event.preventDefault();
      error.textContent = '';
      const payload = {username: username.value, password: password.value};
      try {
        const res = await fetch('/api/admin/login', {method:'POST', credentials:'same-origin', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
        const data = await res.json();
        if (!res.ok || data.success === false) throw new Error(data.error || '登录失败');
        location.replace('/admin/');
      } catch (err) {
        error.textContent = err.message;
      }
    });
  </script>
</body>
</html>`

const adminHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>AINexus License Admin</title>
  <style>
    :root{color-scheme:light;--bg:#f5f5f7;--panel:#fff;--panel-soft:#fbfbfd;--line:#d7d7dd;--soft-line:#ececf0;--text:#1d1d1f;--muted:#6e6e73;--muted-2:#8f8f95;--accent:#0071e3;--accent-hover:#0077ed;--danger:#d70015;--danger-bg:#fff1f1;--ok:#1d7f43;--ok-bg:#edf8f1;--warn:#b56a00;--shadow:0 18px 50px rgba(29,29,31,.08)}
    *{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 8% 2%,rgba(0,113,227,.08),transparent 28%),linear-gradient(180deg,#fbfbfd 0%,var(--bg) 100%);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,"SF Pro Text","SF Pro Display","Helvetica Neue","Segoe UI",sans-serif;-webkit-font-smoothing:antialiased}
    .admin-shell{min-height:100dvh;display:grid;grid-template-columns:244px minmax(0,1fr);gap:0}
    .sidebar{position:sticky;top:0;height:100dvh;padding:22px 16px;border-right:1px solid var(--soft-line);background:rgba(255,255,255,.72);backdrop-filter:saturate(180%) blur(20px);display:flex;flex-direction:column;gap:20px}
    .brand{display:flex;gap:12px;align-items:center;padding:6px}.brand-mark{width:38px;height:38px;border-radius:12px;background:linear-gradient(145deg,#0a84ff,#0071e3);display:grid;place-items:center;color:#fff;font-weight:700}.brand h1{margin:0;font-size:18px;line-height:1.05;letter-spacing:-.02em}.top-note{color:var(--muted);font-size:12px;margin-top:3px}
    .page-tabs{display:grid;gap:6px}.page-tabs button{width:100%;justify-content:flex-start;background:transparent;border-color:transparent;color:var(--muted);box-shadow:none}.page-tabs button.active,.page-tabs button:hover{background:#fff;color:var(--text);border-color:var(--soft-line);box-shadow:0 1px 2px rgba(29,29,31,.04)}
    .sidebar-footer{margin-top:auto;display:grid;gap:8px}.content{min-width:0;padding:26px 30px 34px}.topbar{position:sticky;top:0;z-index:3;margin:-26px -30px 24px;padding:18px 30px;border-bottom:1px solid rgba(215,215,221,.72);background:rgba(245,245,247,.78);backdrop-filter:saturate(180%) blur(20px);display:flex;align-items:center;justify-content:space-between;gap:18px}
    .topbar-title h2{margin:0;font-size:30px;letter-spacing:-.035em;line-height:1.05}.topbar-title p{margin:6px 0 0;color:var(--muted)}.toolbar{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
    main{max-width:1480px;margin:0 auto}.overview-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:18px}.overview-card{background:rgba(255,255,255,.84);border:1px solid rgba(255,255,255,.88);border-radius:8px;padding:16px;box-shadow:0 10px 30px rgba(29,29,31,.06)}.overview-label{font-size:12px;color:var(--muted);font-weight:650}.overview-value{margin-top:8px;font-size:28px;line-height:1;font-weight:700;letter-spacing:-.035em}.overview-note{margin-top:7px;color:var(--muted-2);font-size:12px}
    section{background:rgba(255,255,255,.88);border:1px solid rgba(255,255,255,.92);border-radius:8px;padding:18px;box-shadow:var(--shadow)}h2{font-size:18px;letter-spacing:-.02em;margin:0}.stack{display:grid;gap:18px}.section-head{display:flex;align-items:flex-start;justify-content:space-between;gap:14px;margin-bottom:14px}.section-head p{margin:5px 0 0;color:var(--muted);font-size:13px}.admin-page[hidden]{display:none!important}
    label{display:block;font-size:12px;font-weight:650;color:var(--muted);margin:12px 0 6px}input,select,textarea{width:100%;border:1px solid var(--line);border-radius:8px;padding:10px 11px;background:#fff;color:var(--text);font:inherit;transition:border-color .18s,box-shadow .18s}input:focus,select:focus,textarea:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 4px rgba(0,113,227,.13)}textarea{min-height:84px;resize:vertical}.row{display:grid;grid-template-columns:1fr 1fr;gap:12px}.generate-grid{display:grid;grid-template-columns:minmax(280px,.9fr) minmax(320px,1.1fr);gap:18px;align-items:start}.output-panel{background:var(--panel-soft);border:1px solid var(--soft-line);border-radius:8px;padding:16px;min-height:100%}
    .actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:14px}.inline-check{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted);white-space:nowrap}.inline-check input{width:auto;margin:0}.permission-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:7px;margin-top:8px}.permission-grid label{margin:0;font-weight:500;color:var(--text);background:var(--panel-soft);border:1px solid var(--soft-line);border-radius:8px;padding:8px}
    button{display:inline-flex;align-items:center;justify-content:center;border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:8px;padding:9px 13px;font-weight:650;cursor:pointer;white-space:nowrap;transition:background .18s,border-color .18s,color .18s,transform .18s,box-shadow .18s}button:hover{background:var(--accent-hover);box-shadow:0 8px 20px rgba(0,113,227,.18)}button:active{transform:translateY(1px)}button.secondary{background:#fff;color:var(--text);border-color:var(--line)}button.secondary:hover{background:#f9f9fb;box-shadow:none}button.danger{border-color:var(--danger);background:var(--danger);color:#fff}button.danger:hover{background:#c40012}.small-btn{padding:6px 9px;font-size:12px}
    table{width:100%;border-collapse:separate;border-spacing:0;font-size:13px}th,td{border-bottom:1px solid var(--soft-line);padding:10px 9px;text-align:left;vertical-align:top}th{font-size:11px;color:var(--muted);background:rgba(251,251,253,.96);position:sticky;top:0;z-index:1;font-weight:700}tbody tr:hover td{background:#fbfbfd}
    .table-wrap{overflow:auto;max-height:520px;border:1px solid var(--soft-line);border-radius:8px;background:#fff}.mono{font-family:"SF Mono",ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}.muted{color:var(--muted)}.status-active,.status-disabled,.status-expired{display:inline-flex;border-radius:999px;padding:3px 8px;font-weight:650;font-size:12px}.status-active{background:var(--ok-bg);color:var(--ok)}.status-disabled,.status-expired{background:var(--danger-bg);color:var(--danger)}
    .detail-label{font-size:12px;font-weight:700;color:var(--muted);margin:12px 0 8px}
    dialog{width:min(480px,calc(100% - 32px));border:1px solid var(--line);border-radius:8px;padding:0;color:var(--text);box-shadow:0 28px 80px rgba(29,29,31,.22)}dialog::backdrop{background:rgba(29,29,31,.34);backdrop-filter:blur(2px)}.dialog-body{padding:20px}.dialog-body h2{font-size:18px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:16px}.remote-endpoint-dialog{width:min(720px,calc(100% - 32px))}.remote-table-wrap{width:100%;min-width:0;overflow:auto;contain:inline-size;border:1px solid var(--soft-line);border-radius:8px;background:#fff}.remote-table-wrap table{min-width:920px}
    body.workspace-open{overflow:hidden}.workspace-backdrop{position:fixed;inset:0;z-index:20;background:rgba(29,29,31,.34);backdrop-filter:blur(2px)}.workspace-backdrop[hidden],.device-workspace[hidden]{display:none!important}.device-workspace{position:fixed;z-index:21;top:0;right:0;width:min(1100px,calc(100vw - 40px));height:100dvh;background:#f7f7f9;border-left:1px solid var(--line);box-shadow:-24px 0 70px rgba(29,29,31,.18);display:grid;grid-template-rows:auto auto auto minmax(0,1fr);overflow:hidden}.workspace-header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;padding:20px 22px 14px;background:#fff;border-bottom:1px solid var(--soft-line)}.workspace-header h2{font-size:22px}.workspace-meta{display:flex;gap:8px 14px;flex-wrap:wrap;margin-top:8px;color:var(--muted);font-size:12px}.workspace-close{min-width:38px;padding:7px 10px}.workspace-tabs{display:flex;gap:4px;padding:10px 14px;background:#fff;border-bottom:1px solid var(--soft-line);overflow:auto}.workspace-tabs button{background:transparent;border-color:transparent;color:var(--muted);box-shadow:none}.workspace-tabs button.active{background:var(--panel-soft);border-color:var(--line);color:var(--text)}.workspace-task-bar{display:grid;gap:6px;padding:0 18px;background:#fff}.workspace-task-bar:not(:empty){padding-top:10px;padding-bottom:10px;border-bottom:1px solid var(--soft-line)}.workspace-task{display:flex;align-items:center;justify-content:space-between;gap:10px;border-left:3px solid var(--accent);padding:7px 10px;background:#f4f8ff}.workspace-task.error{border-left-color:var(--danger);background:var(--danger-bg)}.workspace-body{min-height:0;overflow:auto;padding:18px 20px}.workspace-panel[hidden]{display:none!important}.workspace-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.workspace-toolbar h3{margin:0;font-size:16px}.workspace-toolbar .actions{margin-top:0}.workspace-empty{padding:34px 12px;text-align:center;color:var(--muted)}.status-online,.status-stale,.status-offline,.status-queued,.status-delivered,.status-applied,.status-failed{display:inline-flex;border-radius:999px;padding:3px 8px;font-weight:650;font-size:12px}.status-online,.status-applied{background:var(--ok-bg);color:var(--ok)}.status-stale,.status-queued,.status-delivered{background:#fff7e8;color:var(--warn)}.status-offline,.status-failed{background:var(--danger-bg);color:var(--danger)}.endpoint-main{display:grid;gap:3px}.endpoint-main strong{font-size:13px}.endpoint-config{max-width:310px;word-break:break-word}.pending-note{color:var(--warn);font-size:11px;font-weight:650}.action-menu{position:relative}.action-menu summary{list-style:none;cursor:pointer;border:1px solid var(--line);border-radius:8px;padding:6px 9px;background:#fff;font-size:12px;font-weight:650}.action-menu summary::-webkit-details-marker{display:none}.action-menu[open] .action-menu-popover{display:grid}.action-menu-popover{display:none;position:absolute;z-index:5;right:0;top:36px;width:140px;padding:6px;background:#fff;border:1px solid var(--line);border-radius:8px;box-shadow:0 16px 36px rgba(29,29,31,.16)}.action-menu-popover button{width:100%;justify-content:flex-start;background:#fff;color:var(--text);border-color:transparent;box-shadow:none}.action-menu-popover button.danger{color:var(--danger)}.sort-list{display:grid;gap:8px;margin-top:12px}.sort-row{display:grid;grid-template-columns:minmax(0,1fr) auto auto;gap:8px;align-items:center;border:1px solid var(--soft-line);border-radius:8px;padding:8px 10px}.change-summary{margin:12px 0 0;padding:10px 12px;background:var(--panel-soft);border:1px solid var(--soft-line);border-radius:8px}.secret-value{white-space:pre-wrap;word-break:break-all;padding:12px;background:#111;color:#fff;border-radius:8px;min-height:48px}.telemetry-range{display:flex;gap:6px}.telemetry-range button.active{background:var(--accent);border-color:var(--accent);color:#fff}
    #generated{white-space:pre-wrap;word-break:break-all;background:#fff;border:1px solid var(--soft-line);border-radius:8px;padding:12px;margin-top:12px;min-height:188px;max-height:280px;overflow:auto}.message{min-height:20px;margin-top:12px;color:var(--danger);font-size:13px}.empty{text-align:center;color:var(--muted);padding:26px!important}
    @media(max-width:1100px){.admin-shell{grid-template-columns:1fr}.sidebar{position:relative;height:auto;border-right:0;border-bottom:1px solid var(--soft-line)}.page-tabs{grid-template-columns:repeat(5,minmax(0,1fr))}.content{padding:20px}.topbar{margin:-20px -20px 18px;padding:16px 20px}.overview-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.generate-grid{grid-template-columns:1fr}}
    @media(max-width:720px){html,body{overflow-x:hidden}.topbar{align-items:flex-start;flex-direction:column}.toolbar{width:100%;justify-content:space-between}.page-tabs{grid-template-columns:1fr 1fr}.overview-grid,.row{grid-template-columns:1fr}.content{width:100%;max-width:100vw;overflow-x:hidden;padding:14px}.topbar{margin:-14px -14px 16px;padding:14px}.sidebar{padding:16px}.permission-grid{grid-template-columns:1fr}.device-workspace{width:100vw;border-left:0}.workspace-header{padding:16px 14px 12px}.workspace-tabs{padding:8px}.workspace-body{padding:14px}.workspace-toolbar{align-items:flex-start;flex-direction:column}.workspace-toolbar .actions{width:100%}.remote-endpoint-dialog{width:calc(100% - 20px)}}
  </style>
</head>
<body>
  <div class="admin-shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">AI</div>
        <div>
          <h1>AINexus License</h1>
          <div class="top-note">授权运营后台</div>
        </div>
      </div>
      <nav class="page-tabs" aria-label="后台模块">
        <button type="button" data-page-target="generate" onclick="showPage('generate')">生成卡密</button>
        <button type="button" data-page-target="cards" onclick="showPage('cards')">卡密</button>
        <button type="button" data-page-target="accounts" onclick="showPage('accounts')">后台账号</button>
        <button type="button" data-page-target="devices" onclick="showPage('devices')">设备授权</button>
        <button type="button" data-page-target="history" onclick="showPage('history')">历史记录</button>
      </nav>
      <div class="sidebar-footer">
        <button class="secondary" onclick="refreshAll()">刷新</button>
        <button class="danger" onclick="logout()">退出账号</button>
      </div>
    </aside>
    <main class="content">
      <div class="topbar">
        <div class="topbar-title">
          <h2>License Operations</h2>
          <p>卡密、设备授权、账号和历史记录的一体化管理视图</p>
        </div>
        <div class="toolbar">
          <button class="secondary" onclick="refreshAll()">刷新</button>
          <button class="danger" onclick="logout()">退出账号</button>
        </div>
      </div>
      <div id="overview-cards" class="overview-grid"></div>
      <section class="admin-page" data-page="generate">
        <div class="section-head">
          <div>
            <h2>生成卡密</h2>
            <p>左侧填写参数，右侧即时查看生成结果。</p>
          </div>
        </div>
        <div class="generate-grid">
          <div>
            <label>套餐</label>
            <select id="plan"><option value="monthly">月卡 30天</option><option value="quarterly">季卡 90天</option><option value="half_year">半年 180天</option><option value="yearly">年卡 365天</option><option value="custom">自定义</option></select>
            <div class="row"><div><label>自定义天数</label><input id="days" type="number" min="1" value="30"></div><div><label>生成数量</label><input id="count" type="number" min="1" value="1"></div></div>
            <div class="row"><div><label>允许设备数</label><input id="maxDevices" type="number" min="1" value="1"></div><div><label>客户</label><input id="customer" placeholder="客户名"></div></div>
            <label>归属账号</label><select id="ownerAccount"></select>
            <label>备注</label><textarea id="remark" placeholder="订单、渠道、说明"></textarea>
            <div class="actions"><button onclick="generateCards()">生成</button><button class="secondary" onclick="refreshAll()">刷新</button><button class="secondary" onclick="copyGenerated()">复制结果</button></div>
          </div>
          <div class="output-panel">
            <div class="section-head" style="margin-bottom:10px;">
              <div>
                <h2>生成结果</h2>
                <p>结果支持直接复制到剪贴板。</p>
              </div>
            </div>
            <div id="generated" class="muted">尚未生成卡密</div>
            <div id="message" class="message"></div>
          </div>
        </div>
      </section>
      <div class="stack">
        <section class="admin-page" data-page="cards">
          <div class="section-head"><div><h2>卡密</h2><p>按状态、套餐和客户信息快速扫描。</p></div></div>
          <div class="table-wrap"><table><thead><tr><th>ID</th><th>归属</th><th>状态</th><th>套餐</th><th>天数</th><th>设备</th><th>客户/备注</th><th>创建时间</th><th>操作</th></tr></thead><tbody id="cards"><tr><td colspan="9" class="empty">加载中</td></tr></tbody></table></div>
        </section>
        <section class="admin-page" data-page="accounts">
          <div class="section-head"><div><h2>后台账号</h2><p>管理分销层级、权限和状态。</p></div><button class="secondary small-btn" onclick="refreshAccounts()">刷新账号</button></div>
          <div class="row"><div><label>用户名</label><input id="accountUsername" autocomplete="off"></div><div><label>密码</label><input id="accountPassword" type="password" autocomplete="new-password"></div></div>
          <div class="row"><div><label>显示名</label><input id="accountDisplayName"></div><div><label id="accountLevelLabel">级别</label><select id="accountLevel"><option value="2">二级分销</option><option value="3">三级分销</option><option value="1">一级管理员</option></select></div></div>
          <label>父级账号</label><select id="accountParent"></select>
          <div class="permission-grid" id="accountPermissions"></div>
          <div class="actions"><button onclick="createAccount()">创建账号</button></div>
          <div class="table-wrap"><table><thead><tr><th>ID</th><th>账号</th><th id="accountLevelHeader">级别</th><th id="accountParentHeader">父级</th><th>状态</th><th>权限</th><th>操作</th></tr></thead><tbody id="accounts"><tr><td colspan="7" class="empty">加载中</td></tr></tbody></table></div>
        </section>
        <section class="admin-page" data-page="devices">
          <div class="section-head"><div><h2>设备授权</h2><p>默认隐藏敏感值，按需展开查看。</p></div><button id="devicePrivacyBulkButton" class="secondary small-btn" onclick="toggleAllDevicePrivacy()" title="显示当前列表全部设备ID和IP">批量可视</button></div>
          <div class="table-wrap"><table><thead><tr><th>归属</th><th>设备ID</th><th>备注</th><th>状态</th><th>当前到期</th><th>最近校验</th><th>平台/版本</th><th>IP</th><th>兑换次数</th><th>操作</th></tr></thead><tbody id="devices"><tr><td colspan="10" class="empty">加载中</td></tr></tbody></table></div>
        </section>
        <section class="admin-page" data-page="history">
          <div class="section-head"><div><h2>历史记录</h2><p>保留操作轨迹，便于审计和追踪。</p></div><label class="inline-check"><input id="showRefresh" type="checkbox" onchange="renderHistory()">显示自动刷新</label></div>
          <div class="table-wrap"><table><thead><tr><th>ID</th><th>动作</th><th>对象</th><th>详情</th><th>时间</th></tr></thead><tbody id="history"><tr><td colspan="5" class="empty">加载中</td></tr></tbody></table></div>
        </section>
      </div>
    </main>
  </div>
  <div id="deviceWorkspaceBackdrop" class="workspace-backdrop" hidden onclick="closeDeviceWorkspace()"></div>
  <aside id="deviceWorkspace" class="device-workspace" aria-label="设备远程管理操作台" hidden>
    <header class="workspace-header">
      <div>
        <h2 id="workspaceDeviceTitle">设备管理</h2>
        <div id="workspaceDeviceMeta" class="workspace-meta"></div>
      </div>
      <button type="button" class="secondary workspace-close" onclick="closeDeviceWorkspace()" aria-label="关闭设备操作台">关闭</button>
    </header>
    <nav class="workspace-tabs" aria-label="设备操作台模块">
      <button type="button" data-workspace-tab="endpoints" onclick="showWorkspaceTab('endpoints')">端点</button>
      <button type="button" data-workspace-tab="tokens" onclick="showWorkspaceTab('tokens')">Token Pool</button>
      <button type="button" data-workspace-tab="errors" onclick="showWorkspaceTab('errors')">错误诊断</button>
      <button type="button" data-workspace-tab="commands" onclick="showWorkspaceTab('commands')">命令记录</button>
      <button type="button" data-workspace-tab="licenses" onclick="showWorkspaceTab('licenses')">授权明细</button>
    </nav>
    <div id="workspaceTaskBar" class="workspace-task-bar"></div>
    <div class="workspace-body">
      <section id="workspacePanelEndpoints" class="workspace-panel" data-workspace-panel="endpoints"></section>
      <section id="workspacePanelTokens" class="workspace-panel" data-workspace-panel="tokens" hidden></section>
      <section id="workspacePanelErrors" class="workspace-panel" data-workspace-panel="errors" hidden></section>
      <section id="workspacePanelCommands" class="workspace-panel" data-workspace-panel="commands" hidden></section>
      <section id="workspacePanelLicenses" class="workspace-panel" data-workspace-panel="licenses" hidden></section>
    </div>
  </aside>
  <dialog id="expiryDialog">
    <form class="dialog-body" onsubmit="submitExpiry(event)">
      <h2>修改设备到期时间</h2>
      <div id="expiryDevice" class="mono muted"></div>
      <label for="expiryInput">新的到期时间</label>
      <input id="expiryInput" type="datetime-local" step="1" required>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="expiryDialog.close()">取消</button><button type="submit">保存</button></div>
    </form>
  </dialog>
  <dialog id="remarkDialog">
    <form class="dialog-body" onsubmit="submitRemark(event)">
      <h2>修改设备备注</h2>
      <div id="remarkDevice" class="mono muted"></div>
      <label for="remarkInput">备注</label>
      <textarea id="remarkInput" maxlength="500" placeholder="例如：客户名、机器位置、订单信息"></textarea>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="remarkDialog.close()">取消</button><button type="submit">保存</button></div>
    </form>
  </dialog>
  <dialog id="remoteEndpointDialog" class="remote-endpoint-dialog">
    <form class="dialog-body" onsubmit="submitRemoteEndpoint(event)">
      <h2 id="remoteEndpointDialogTitle">新增远程端点</h2>
      <div class="row">
        <div><label for="remoteEndpointName">端点名称</label><input id="remoteEndpointName" required></div>
        <div><label for="remoteEndpointAuthMode">认证模式</label><select id="remoteEndpointAuthMode" onchange="remoteSyncEndpointAuthMode()"><option value="api_key">API Key</option><option value="token_pool">Token Pool</option><option value="codex_token_pool">Codex Token Pool</option><option value="claude_oauth_token_pool">Claude OAuth Token Pool</option></select></div>
      </div>
      <label for="remoteEndpointAPIUrl">Base URL</label>
      <input id="remoteEndpointAPIUrl" required>
      <label for="remoteEndpointAPIKey">API Key</label>
      <input id="remoteEndpointAPIKey" type="password" autocomplete="new-password" placeholder="编辑时留空表示保持不变">
      <div id="remoteEndpointAPIKeyHelp" class="muted">Token Pool 模式可留空</div>
      <div class="row">
        <div><label for="remoteEndpointTransformer">转换器</label><select id="remoteEndpointTransformer"><option value="claude">Claude</option><option value="openai">OpenAI Chat</option><option value="openai2">OpenAI Responses</option><option value="gemini">Gemini</option><option value="deepseek">DeepSeek</option><option value="kimi">Kimi</option><option value="poe">Poe</option></select></div>
        <div><label for="remoteEndpointModel">模型</label><input id="remoteEndpointModel" value="gpt-5" placeholder="留空表示不强制覆盖请求模型"></div>
      </div>
      <div class="row">
        <div><label for="remoteEndpointThinking">推理强度</label><select id="remoteEndpointThinking"><option value="__keep__">保持不变</option><option id="remoteEndpointThinkingDefault" value="">上游默认</option><option value="off">关闭</option><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="xhigh">XHigh / Max</option></select></div>
        <div><label for="remoteEndpointMaxConcurrentRequests">限制并发</label><input id="remoteEndpointMaxConcurrentRequests" type="number" min="0" step="1" value="0"></div>
      </div>
      <div class="row">
        <label class="inline-check"><input id="remoteEndpointCodexFastMode" type="checkbox" disabled> Codex 快速模式</label>
        <label class="inline-check"><input id="remoteEndpointEnabled" type="checkbox" checked> 启用端点</label>
      </div>
      <p id="remoteEndpointThinkingHelp" class="muted"></p>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="remoteEndpointDialog.close()">取消</button><button type="submit">查看变更并下发</button></div>
    </form>
  </dialog>
  <dialog id="remoteSortDialog">
    <form class="dialog-body" onsubmit="submitRemoteSort(event)">
      <h2>调整端点顺序</h2>
      <div class="muted">完成全部调整后只下发一次排序命令。</div>
      <div id="remoteSortList" class="sort-list"></div>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="remoteSortDialog.close()">取消</button><button type="submit">保存顺序</button></div>
    </form>
  </dialog>
  <dialog id="remoteTokenDialog">
    <form class="dialog-body" onsubmit="submitRemoteToken(event)">
      <h2>更新凭证 Token</h2>
      <div id="remoteTokenCredentialLabel" class="mono muted"></div>
      <label for="remoteTokenAccessToken">新的 access token</label>
      <input id="remoteTokenAccessToken" type="password" autocomplete="new-password" required>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="remoteTokenDialog.close()">取消</button><button type="submit">下发更新</button></div>
    </form>
  </dialog>
  <dialog id="remoteConfirmDialog">
    <div class="dialog-body">
      <h2 id="remoteConfirmTitle">确认远程操作</h2>
      <div id="remoteConfirmMessage" class="muted"></div>
      <div id="remoteConfirmSummary" class="change-summary"></div>
      <label id="remoteConfirmInputLabel" for="remoteConfirmInput" hidden>确认内容</label>
      <input id="remoteConfirmInput" hidden>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="resolveRemoteConfirmation(false)">取消</button><button id="remoteConfirmButton" type="button" onclick="resolveRemoteConfirmation(true)">确认下发</button></div>
    </div>
  </dialog>
  <dialog id="remoteSecretDialog">
    <div class="dialog-body">
      <h2>一次性敏感信息</h2>
      <div id="remoteSecretStatus" class="muted">等待客户端返回加密结果</div>
      <div id="remoteSecretValue" class="secret-value">等待中</div>
      <div id="remoteSecretCountdown" class="muted"></div>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="clearRemoteSecret()">立即清除</button><button id="remoteSecretCopyButton" type="button" onclick="copyRemoteSecret()" disabled>复制</button></div>
    </div>
  </dialog>
  <script>
    let historyRows = [];
    let accountRows = [];
    let currentAccount = null;
    let editingDeviceId = '';
    let workspaceDeviceIndex = -1;
    let workspaceData = null;
    let workspaceTab = 'endpoints';
    let workspaceTelemetry = {loaded:false,loading:false,range:'24h',summary24h:[],summary7d:[],error:''};
    let workspaceCommandPollError = '';
    let remoteEndpointEditorContext = null;
    let remoteSortNames = [];
    let remoteTokenCredentialID = 0;
    let remoteConfirmationResolve = null;
    let remoteConfirmationExpected = '';
    let remoteSecretPlaintext = '';
    let remoteSecretClearTimer = 0;
    const remoteCommandPollers = new Map();
    let revealedDeviceIndexes = new Set();
    const permissionCatalog = [
      ['cards:view','看卡密'],['cards:generate','生成卡密'],['cards:disable','禁用卡密'],['cards:delete','删除卡密'],
      ['devices:view','看设备'],['devices:remark','备注设备'],['devices:expiry','改到期'],['activations:disable','禁用授权'],
      ['devices:remote:view','远程查看'],['devices:remote:write','远程维护'],['devices:remote:secrets','查看密钥'],
      ['accounts:view','看账号'],['accounts:manage','管账号'],['history:view','看历史']
    ];
    const historyBody = document.getElementById('history');
    const showRefreshInput = document.getElementById('showRefresh');
    const overviewContainer = document.getElementById('overview-cards');
    async function api(path, options={}) {
      const headers = Object.assign({'Content-Type':'application/json'}, options.headers || {});
      const res = await fetch(path, Object.assign({}, options, {credentials:'same-origin', headers}));
      let data = {};
      try { data = await res.json(); } catch (err) {}
      if (res.status === 401) {
        location.replace('/admin/login');
        throw new Error('请先登录');
      }
      if (!res.ok || data.success === false) throw new Error(data.error || '请求失败');
      return data.data;
    }
    function esc(v){return String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
    function dt(v){return v ? new Date(v).toLocaleString() : '-'}
    function setError(err){message.textContent = err ? err.message || String(err) : ''}
    function can(permission){return currentAccount && Array.isArray(currentAccount.permissions) && currentAccount.permissions.includes(permission)}
    function statusCell(value){const names={active:'有效',disabled:'已禁用',expired:'已到期'};return '<span class="status-'+esc(value)+'">'+esc(names[value]||value)+'</span>'}
    function statCard(label, value, note){return '<div class="overview-card"><div class="overview-label">'+esc(label)+'</div><div class="overview-value">'+esc(value)+'</div><div class="overview-note">'+esc(note || '')+'</div></div>'}
    function formatCount(value){const num = Number(value || 0);if(num >= 1000000){return (num / 1000000).toFixed(1)+'M'}if(num >= 1000){return (num / 1000).toFixed(1)+'K'}return String(num)}
    function renderOverview(){
      if(!overviewContainer) return;
      const cardsData = Array.isArray(window.lastCards) ? window.lastCards : [];
      const devicesData = Array.isArray(window.deviceRows) ? window.deviceRows : [];
      const accountsData = Array.isArray(accountRows) ? accountRows : [];
      const activeCards = cardsData.filter(card => card.status === 'active').length;
      const activeDevices = devicesData.filter(device => device.status === 'active').length;
      overviewContainer.innerHTML = [
        statCard('卡密总数', formatCount(cardsData.length), '当前保存的卡密记录'),
        statCard('有效卡密', formatCount(activeCards), '可继续兑换与使用'),
        statCard('授权设备', formatCount(devicesData.length), '已激活设备记录'),
        statCard('后台账号', formatCount(accountsData.length), '当前可登录账号'),
      ].join('');
    }
    function actionName(value){return ({admin_login:'登录',admin_logout:'退出',generate_card:'生成卡密',activate:'兑换卡密',refresh:'自动校验',disable_card:'禁用卡密',delete_card:'删除卡密',disable_activation:'禁用授权明细',set_device_expiry:'修改设备到期',set_device_remark:'修改设备备注',create_admin_account:'创建账号',update_admin_account:'修改账号'}[value] || value)}
    function planName(value){return ({monthly:'月卡',quarterly:'季卡',half_year:'半年卡',yearly:'年卡',custom:'自定义'}[value] || value)}
    function isRootAccount(){return currentAccount && Number(currentAccount.level) === 1}
    function levelName(value){return ({1:'一级',2:'二级',3:'三级'}[Number(value)] || value)}
    function relationName(account){if(isRootAccount())return levelName(account.level);return ({self:'当前账号',downline:'下级账号'}[account.relationship] || '范围内账号')}
    function parentName(account){if(isRootAccount())return account.parentId || '-';return account.relationship === 'downline' ? '当前账号' : '-'}
    function toLocalInput(value){const date=new Date(value);date.setMinutes(date.getMinutes()-date.getTimezoneOffset());return date.toISOString().slice(0,19)}
    function privateValue(value, revealed){const text=String(value ?? '').trim();if(!text)return '-';return revealed ? esc(text) : '**'}
    function devicePrivacyRevealed(index){return revealedDeviceIndexes.has(index)}
    function devicePrivacyBulkLabel(){const rows=window.deviceRows||[];if(!rows.length)return '批量可视';return rows.every((_,index)=>revealedDeviceIndexes.has(index)) ? '批量隐藏' : '批量可视'}
    function syncDevicePrivacyBulkButton(){const bulkButton=document.getElementById('devicePrivacyBulkButton');if(!bulkButton)return;const label=devicePrivacyBulkLabel();bulkButton.textContent=label;bulkButton.title=label==='批量隐藏' ? '隐藏当前列表全部设备ID和IP' : '显示当前列表全部设备ID和IP'}
    function syncDevicePrivacyRow(index){const rows=window.deviceRows||[];const device=rows[index];if(!device)return;const revealed=devicePrivacyRevealed(index);const deviceIdCell=document.getElementById('deviceIdValue-'+index);if(deviceIdCell)deviceIdCell.innerHTML=privateValue(device.deviceId,revealed);const ipCell=document.getElementById('deviceIpValue-'+index);if(ipCell)ipCell.innerHTML=privateValue(device.ipAddress,revealed);const button=document.getElementById('devicePrivacyButton-'+index);if(button){button.textContent=revealed?'🙈':'👁';button.title=revealed?'隐藏该行设备ID和IP':'显示该行设备ID和IP';button.setAttribute('aria-label',revealed?'隐藏该行设备ID和IP':'显示该行设备ID和IP')}}
    function syncDevicePrivacyRows(){const rows=window.deviceRows||[];for(let i=0;i<rows.length;i++){syncDevicePrivacyRow(i)}syncDevicePrivacyBulkButton()}
    function toggleDevicePrivacy(index){if(revealedDeviceIndexes.has(index)){revealedDeviceIndexes.delete(index)}else{revealedDeviceIndexes.add(index)};syncDevicePrivacyRow(index);syncDevicePrivacyBulkButton()}
    function toggleAllDevicePrivacy(){const rows=window.deviceRows||[];if(!rows.length)return;const allRevealed=rows.every((_,index)=>revealedDeviceIndexes.has(index));revealedDeviceIndexes=allRevealed?new Set():new Set(rows.map((_,index)=>index));syncDevicePrivacyRows()}
    async function refreshMe(){
      const me = await api('/api/admin/me');
      currentAccount = me.account;
      renderAccountLevelControls();
    }
    function renderAccountLevelControls(){
      if (isRootAccount()) {
        accountLevelLabel.textContent = '级别';
        accountLevelHeader.textContent = '级别';
        accountParentHeader.textContent = '父级';
        accountLevel.innerHTML = '<option value="2">二级分销</option><option value="3">三级分销</option><option value="1">一级管理员</option>';
      } else {
        accountLevelLabel.textContent = '关系';
        accountLevelHeader.textContent = '关系';
        accountParentHeader.textContent = '上级关系';
        accountLevel.innerHTML = '<option value="0">下级账号</option>';
      }
    }
    function renderPermissionChoices(selected){
      const selectedSet = new Set(selected || []);
      accountPermissions.innerHTML = permissionCatalog.map(p => '<label><input type="checkbox" value="'+esc(p[0])+'" '+(selectedSet.has(p[0])?'checked':'')+'> '+esc(p[1])+'</label>').join('');
    }
    function accountLabel(account){return account ? account.username+' #'+account.id : '-'}
    function ownerLabel(row){return esc(row.ownerUsername || row.ownerAccountId || '-')}
    function showPage(page){
      const allowed = new Set(['generate','cards','accounts','devices','history']);
      if (!allowed.has(page)) page = 'cards';
      document.querySelectorAll('[data-page]').forEach(section => { section.hidden = section.getAttribute('data-page') !== page; });
      document.querySelectorAll('[data-page-target]').forEach(button => { button.classList.toggle('active', button.getAttribute('data-page-target') === page); });
      if (location.hash !== '#'+page) history.replaceState(null, '', '#'+page);
    }
    function refreshAccountSelectors(){
      const visible = accountRows.length ? accountRows : (currentAccount ? [currentAccount] : []);
      ownerAccount.innerHTML = visible.map(a => '<option value="'+a.id+'">'+esc(accountLabel(a))+'</option>').join('');
      accountParent.innerHTML = visible.map(a => '<option value="'+a.id+'">'+esc(accountLabel(a))+'</option>').join('');
      if (currentAccount) {
        ownerAccount.value = String(currentAccount.id);
        accountParent.value = String(currentAccount.id);
      }
    }
    async function refreshAccounts(){
      if (!can('accounts:view')) {
        accountRows = currentAccount ? [currentAccount] : [];
        accounts.innerHTML = '<tr><td colspan="7" class="empty">无账号管理权限</td></tr>';
        refreshAccountSelectors();
        renderPermissionChoices([]);
        return;
      }
      accountRows = await api('/api/admin/accounts');
      accounts.innerHTML = accountRows.length ? accountRows.map(a => { const self=currentAccount&&a.id===currentAccount.id; const actions=can('accounts:manage')&&!self?'<button class="secondary small-btn" onclick="editAccountPermissions('+a.id+')">权限</button><button class="secondary small-btn" onclick="toggleAccountStatus('+a.id+',&quot;'+(a.status==='active'?'disabled':'active')+'&quot;)">'+(a.status==='active'?'禁用':'启用')+'</button>':'-'; return '<tr><td>'+a.id+'</td><td>'+esc(a.username)+'<br><span class="muted">'+esc(a.displayName||'')+'</span></td><td>'+esc(relationName(a))+'</td><td>'+esc(parentName(a))+'</td><td>'+statusCell(a.status)+'</td><td class="mono">'+esc((a.permissions||[]).join(', '))+'</td><td><div class="actions">'+actions+'</div></td></tr>'; }).join('') : '<tr><td colspan="7" class="empty">暂无后台账号</td></tr>';
      refreshAccountSelectors();
      renderPermissionChoices(defaultPermissionsForLevel(Number(accountLevel.value)));
      renderOverview();
    }
    function defaultPermissionsForLevel(level){
      if (level === 1) return permissionCatalog.map(p => p[0]);
      if (level === 2) return ['cards:view','cards:generate','cards:disable','devices:view','devices:remark','devices:expiry','devices:remote:view','devices:remote:write','activations:disable','accounts:view','accounts:manage','history:view'];
      return ['cards:view','cards:generate','cards:disable','devices:view','devices:remark','devices:expiry','devices:remote:view','devices:remote:write','activations:disable','history:view'];
    }
    async function createAccount(){
      setError('');
      try {
        const permissions = Array.from(accountPermissions.querySelectorAll('input:checked')).map(i => i.value);
        const payload = {username:accountUsername.value,password:accountPassword.value,displayName:accountDisplayName.value,level:Number(accountLevel.value),parentId:Number(accountParent.value||0),permissions};
        await api('/api/admin/accounts',{method:'POST',body:JSON.stringify(payload)});
        accountUsername.value=''; accountPassword.value=''; accountDisplayName.value='';
        await refreshAccounts();
      } catch (err) { setError(err); }
    }
    async function toggleAccountStatus(id,status){try{await api('/api/admin/accounts/'+id,{method:'PATCH',body:JSON.stringify({status})});await refreshAccounts();}catch(err){setError(err)}}
    async function editAccountPermissions(id){const account=accountRows.find(a=>a.id===id);const value=prompt('权限列表（英文逗号分隔）',(account.permissions||[]).join(','));if(value===null)return;try{await api('/api/admin/accounts/'+id,{method:'PATCH',body:JSON.stringify({permissions:value.split(',').map(v=>v.trim()).filter(Boolean)})});await refreshAccounts();}catch(err){setError(err)}}
    accountLevel.addEventListener('change', () => renderPermissionChoices(defaultPermissionsForLevel(Number(accountLevel.value))));
    async function generateCards(){
      setError('');
      try {
        const payload = {plan:plan.value,days:Number(days.value||0),count:Number(count.value||1),maxDevices:Number(maxDevices.value||1),customer:customer.value,remark:remark.value,ownerAccountId:Number(ownerAccount.value||0)};
        const data = await api('/api/admin/cards/generate',{method:'POST',body:JSON.stringify(payload)});
        generated.textContent = data.cards.map(c => c.cardKey).join('\n');
        await refreshAll();
      } catch (err) { setError(err); }
    }
    async function refreshCards(){
      const rows = await api('/api/admin/cards');
      window.lastCards = rows;
      cards.innerHTML = rows.length ? rows.map(c => '<tr><td>'+c.id+'</td><td>'+esc(c.ownerUsername||c.ownerAccountId||'-')+'</td><td>'+statusCell(c.status)+'</td><td>'+esc(c.plan)+'</td><td>'+c.days+'</td><td>'+c.activations+'/'+c.maxDevices+'</td><td>'+esc(c.customer)+'<br><span class="muted">'+esc(c.remark)+'</span></td><td>'+dt(c.createdAt)+'</td><td><div class="actions">'+(c.status==='active'&&can('cards:disable')?'<button class="danger small-btn" onclick="disableCard('+c.id+')">禁用</button>':'')+(can('cards:delete')?'<button class="danger small-btn" onclick="deleteCard('+c.id+')">删除</button>':'')+'</div></td></tr>').join('') : '<tr><td colspan="9" class="empty">暂无卡密</td></tr>';
      renderOverview();
    }
    function licenseRows(device){
      return device.licenses.map(a => '<tr><td>'+a.cardId+'</td><td>'+statusCell(a.status)+'</td><td>'+esc(planName(a.plan))+' / '+a.days+'天</td><td>'+dt(a.activatedAt)+'</td><td>'+dt(a.expiresAt)+'</td><td>'+esc(a.customer)+'<br><span class="muted">'+esc(a.remark)+'</span></td><td>'+(a.status==='active'&&can('activations:disable')?'<button class="danger small-btn" onclick="disableActivation('+a.id+')">禁用此明细</button>':'-')+'</td></tr>').join('');
    }
    async function refreshDevices(){
      const rows = await api('/api/admin/devices');
      revealedDeviceIndexes = new Set();
      window.deviceRows = rows;
      renderDevices();
      renderOverview();
    }
    function renderDevices(){
      const rows = window.deviceRows || [];
      devices.innerHTML = rows.length ? rows.map((d,index) => {
        const revealed = devicePrivacyRevealed(index);
        return '<tr><td>'+ownerLabel(d)+'</td><td class="mono"><span id="deviceIdValue-'+index+'">'+privateValue(d.deviceId, revealed)+'</span> <button id="devicePrivacyButton-'+index+'" class="secondary small-btn" onclick="toggleDevicePrivacy('+index+')" title="'+esc(revealed ? '隐藏该行设备ID和IP' : '显示该行设备ID和IP')+'" aria-label="'+esc(revealed ? '隐藏该行设备ID和IP' : '显示该行设备ID和IP')+'">'+(revealed ? '🙈' : '👁')+'</button></td><td>'+esc(d.remark||'-')+'</td><td>'+statusCell(d.status)+'</td><td>'+dt(d.expiresAt)+'</td><td>'+dt(d.lastCheckedAt)+'</td><td>'+esc(d.platform)+'<br><span class="muted">'+esc(d.appVersion)+'</span></td><td class="mono"><span id="deviceIpValue-'+index+'">'+privateValue(d.ipAddress, revealed)+'</span></td><td>'+d.licenses.length+'</td><td><div class="actions"><button class="secondary small-btn" onclick="openDeviceWorkspace('+index+')">管理</button>'+(can('devices:remark')?'<button class="secondary small-btn" onclick="openRemark('+index+')">备注</button>':'')+(can('devices:expiry')?'<button class="small-btn" onclick="openExpiry('+index+')">修改到期</button>':'')+(d.status==='active'&&can('activations:disable')?'<button class="danger small-btn" onclick="disableActivation('+d.currentActivationId+')">禁用当前</button>':'')+'</div></td></tr>';
      }).join('') : '<tr><td colspan="10" class="empty">暂无授权设备</td></tr>';
      syncDevicePrivacyBulkButton();
      renderOverview();
    }
    async function refreshHistory(){
      historyRows = await api('/api/admin/history');
      renderHistory();
      renderOverview();
    }
    function renderHistory(){const rows=showRefreshInput.checked?historyRows:historyRows.filter(h=>h.action!=='refresh');historyBody.innerHTML=rows.length?rows.map(h=>'<tr><td>'+h.id+'</td><td>'+esc(actionName(h.action))+'</td><td>'+esc(h.targetType)+' #'+h.targetId+'</td><td class="mono">'+esc(h.detail||'-')+'</td><td>'+dt(h.createdAt)+'</td></tr>').join(''):'<tr><td colspan="5" class="empty">暂无历史记录</td></tr>'}
    function currentWorkspaceDevice(){return workspaceDeviceIndex>=0?(window.deviceRows||[])[workspaceDeviceIndex]:null}
    function maskedDeviceID(value){const text=String(value||'');if(text.length<=10)return text?'**':'-';return text.slice(0,5)+'…'+text.slice(-5)}
    async function openDeviceWorkspace(index){
      const device=(window.deviceRows||[])[index];
      if(!device)return;
      stopRemoteCommandPollers();
      workspaceDeviceIndex=index;
      workspaceData=null;
      workspaceTab='endpoints';
      workspaceTelemetry={loaded:false,loading:false,range:'24h',summary24h:[],summary7d:[],error:''};
      workspaceCommandPollError='';
      deviceWorkspaceBackdrop.hidden=false;
      deviceWorkspace.hidden=false;
      document.body.classList.add('workspace-open');
      renderWorkspaceHeader();
      renderWorkspaceLicenses();
      workspacePanelEndpoints.innerHTML='<div class="workspace-empty">正在加载远程状态</div>';
      workspacePanelTokens.innerHTML='<div class="workspace-empty">正在加载远程状态</div>';
      workspacePanelCommands.innerHTML='<div class="workspace-empty">正在加载命令记录</div>';
      workspacePanelErrors.innerHTML='<div class="workspace-empty">进入此标签后加载错误诊断</div>';
      showWorkspaceTab('endpoints');
      await loadWorkspaceRemote();
    }
    function closeDeviceWorkspace(){
      stopRemoteCommandPollers();
      clearRemoteSecret();
      deviceWorkspace.hidden=true;
      deviceWorkspaceBackdrop.hidden=true;
      document.body.classList.remove('workspace-open');
      workspaceDeviceIndex=-1;
      workspaceData=null;
    }
    function showWorkspaceTab(tab){
      const allowed=new Set(['endpoints','tokens','errors','commands','licenses']);
      workspaceTab=allowed.has(tab)?tab:'endpoints';
      document.querySelectorAll('[data-workspace-tab]').forEach(button=>button.classList.toggle('active',button.getAttribute('data-workspace-tab')===workspaceTab));
      document.querySelectorAll('[data-workspace-panel]').forEach(panel=>{panel.hidden=panel.getAttribute('data-workspace-panel')!==workspaceTab});
      if(workspaceTab==='errors')loadWorkspaceTelemetry();
    }
    function workspaceOnlineState(state){
      state=state||{};
      const values=[state.lastSnapshotAt,state.lastHeartbeatAt].map(v=>v?Date.parse(v):0).filter(v=>Number.isFinite(v)&&v>0);
      const latest=values.length?Math.max(...values):0;
      const age=latest?Date.now()-latest:Number.POSITIVE_INFINITY;
      if(age<=2*60*1000)return {key:'online',label:'在线'};
      if(age<=10*60*1000)return {key:'stale',label:'状态较旧'};
      return {key:'offline',label:'可能离线'};
    }
    function renderWorkspaceHeader(){
      const device=currentWorkspaceDevice();
      if(!device)return;
      const state=(workspaceData&&workspaceData.state)||{};
      const online=workspaceOnlineState(state);
      workspaceDeviceTitle.textContent=device.remark||'设备管理';
      workspaceDeviceMeta.innerHTML='<span class="mono">'+esc(maskedDeviceID(device.deviceId))+'</span><span class="status-'+online.key+'">'+online.label+'</span><span>客户端 '+esc(state.clientVersion||device.appVersion||'-')+'</span><span>快照 '+dt(state.lastSnapshotAt)+'</span><span>心跳 '+dt(state.lastHeartbeatAt)+'</span><span>能力 '+esc((state.capabilities||[]).join(', ')||'-')+'</span>';
    }
    async function loadWorkspaceRemote(){
      const device=currentWorkspaceDevice();
      if(!device||!can('devices:remote:view'))return;
      try{
        workspaceData=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote');
        workspaceCommandPollError='';
        renderWorkspaceHeader();
        renderWorkspaceEndpoints();
        renderWorkspaceTokenPools();
        renderWorkspaceCommands();
        renderWorkspaceTasks();
        resumeWorkspaceCommands();
      }catch(err){
        workspacePanelEndpoints.innerHTML='<div class="status-failed">'+esc(err.message||err)+'</div>';
        workspacePanelTokens.innerHTML='<div class="status-failed">'+esc(err.message||err)+'</div>';
        workspacePanelCommands.innerHTML='<div class="status-failed">'+esc(err.message||err)+'</div>';
      }
    }
    function remoteSupportsThinkingV2(state){return ((state&&state.capabilities)||[]).includes('endpoints:thinking:v2')}
    function thinkingDisplay(state,ep){const hasThinking=Object.prototype.hasOwnProperty.call(ep,'thinking');if(!hasThinking&&!remoteSupportsThinkingV2(state))return '未上报';const value=String(ep.thinking||'').trim().toLowerCase();return ({'':'上游默认',off:'关闭',low:'Low',medium:'Medium',high:'High',xhigh:'XHigh / Max'}[value]||value)}
    function renderWorkspaceEndpoints(){
      const state=(workspaceData&&workspaceData.state)||{};
      if(!state.supported){workspacePanelEndpoints.innerHTML='<div class="workspace-empty">该客户端版本暂不支持远程维护，不影响授权和本地配置。</div>';return}
      const endpoints=((state.snapshot||{}).endpoints)||[];
      const toolbar=can('devices:remote:write')?'<div class="actions"><button class="secondary small-btn" onclick="openRemoteSortDialog()" '+(endpoints.length<2||hasPendingRemoteOrder()?'disabled':'')+'>调整顺序</button><button class="small-btn" onclick="openRemoteEndpointEditor(workspaceDeviceIndex,-1)">新增端点</button></div>':'';
      const rows=endpoints.length?endpoints.map((ep,epIndex)=>{
        const pending=hasPendingRemoteTarget('endpoint',ep.name,0);
        const disabled=pending?'disabled':'';
        const actions=can('devices:remote:write')?'<div class="actions"><button class="secondary small-btn" '+disabled+' onclick="remoteToggleEndpoint(workspaceDeviceIndex,&quot;'+escAttr(ep.name)+'&quot;,'+(!ep.enabled)+')">'+(pending?'处理中':(ep.enabled?'停用':'启用'))+'</button><button class="small-btn" '+disabled+' onclick="openRemoteEndpointEditor(workspaceDeviceIndex,'+epIndex+')">编辑</button><details class="action-menu"><summary>更多</summary><div class="action-menu-popover">'+(can('devices:remote:secrets')?'<button type="button" onclick="remoteRevealSecret(workspaceDeviceIndex,&quot;'+escAttr(ep.name)+'&quot;,0,&quot;apiKey&quot;)">查看 Key</button>':'')+'<button type="button" class="danger" '+disabled+' onclick="remoteDeleteEndpoint(workspaceDeviceIndex,&quot;'+escAttr(ep.name)+'&quot;)">删除端点</button></div></details></div>':'-';
        return '<tr><td><div class="endpoint-main"><strong>'+esc(ep.name)+'</strong><span class="status-'+(ep.enabled?'online':'offline')+'">'+(ep.enabled?'启用':'停用')+'</span>'+(pending?'<span class="pending-note">远程命令处理中</span>':'')+'</div></td><td class="endpoint-config"><div class="mono">'+esc(ep.apiUrl||'-')+'</div><div class="muted">'+esc(ep.authMode||'-')+' · '+esc(ep.transformer||'-')+' · Key '+esc(ep.apiKeyMasked||'-')+'</div></td><td><div class="mono">'+esc(ep.model||'-')+'</div><div class="muted">'+esc(thinkingDisplay(state,ep))+'</div></td><td>'+esc(ep.codexFastMode?'开启':'关闭')+'<br><span class="muted">并发 '+esc((ep.maxConcurrentRequests||0)>0?ep.maxConcurrentRequests:'不限')+'</span></td><td>'+remoteStats(ep.stats)+'</td><td>'+actions+'</td></tr>';
      }).join(''):'<tr><td colspan="6" class="empty">暂无端点快照</td></tr>';
      workspacePanelEndpoints.innerHTML='<div class="workspace-toolbar"><div><h3>远程端点</h3><div class="muted">配置以客户端回传快照为准，不对尚未应用的命令做乐观更新。</div></div>'+toolbar+'</div><div class="remote-table-wrap"><table><thead><tr><th>端点</th><th>目标与模式</th><th>模型</th><th>快速/并发</th><th>用量</th><th>操作</th></tr></thead><tbody>'+rows+'</tbody></table></div>';
    }
    function renderWorkspaceTokenPools(){
      const state=(workspaceData&&workspaceData.state)||{};
      if(!state.supported){workspacePanelTokens.innerHTML='<div class="workspace-empty">该客户端未提供 Token Pool 快照。</div>';return}
      const pools=((state.snapshot||{}).tokenPools)||[];
      if(!pools.length){workspacePanelTokens.innerHTML='<div class="workspace-empty">暂无 Token Pool 快照。</div>';return}
      workspacePanelTokens.innerHTML='<div class="workspace-toolbar"><div><h3>Token Pool</h3><div class="muted">只提供现有凭证的启停、Token 更新、删除和一次性查看。</div></div></div>'+pools.map(pool=>'<div class="detail-label">'+esc(pool.endpointName)+' · '+esc(pool.authMode||'-')+'</div>'+renderWorkspaceCredentials(pool)).join('');
    }
    function renderWorkspaceCredentials(pool){
      const creds=pool.credentials||[];
      if(!creds.length)return '<div class="workspace-empty">该端点暂无凭证</div>';
      const rows=creds.map(c=>{
        const pending=hasPendingRemoteTarget('credential','',c.id);
        const disabled=pending?'disabled':'';
        const actions=can('devices:remote:write')?'<div class="actions"><button class="secondary small-btn" '+disabled+' onclick="remoteCredentialEnabled(workspaceDeviceIndex,'+c.id+','+(!c.enabled)+')">'+(pending?'处理中':(c.enabled?'停用':'启用'))+'</button><button class="small-btn" '+disabled+' onclick="openRemoteTokenDialog('+c.id+')">更新 Token</button><details class="action-menu"><summary>更多</summary><div class="action-menu-popover">'+(can('devices:remote:secrets')?'<button type="button" onclick="remoteRevealSecret(workspaceDeviceIndex,&quot;'+escAttr(pool.endpointName)+'&quot;,'+c.id+',&quot;accessToken&quot;)">查看 Token</button>':'')+'<button type="button" class="danger" '+disabled+' onclick="remoteDeleteCredential(workspaceDeviceIndex,'+c.id+')">删除凭证</button></div></details></div>':'-';
        return '<tr><td>'+c.id+(pending?'<div class="pending-note">处理中</div>':'')+'</td><td class="mono">'+esc(c.accountIdMasked||'-')+'</td><td>'+esc(c.emailMasked||'-')+'</td><td>'+esc(c.status||'-')+(c.enabled?'':' / 停用')+'</td><td>'+remoteStats(c.usage)+'</td><td class="mono">'+esc(remoteQuota(c.quota))+'</td><td>'+actions+'</td></tr>';
      }).join('');
      return '<div class="remote-table-wrap"><table><thead><tr><th>ID</th><th>账号</th><th>邮箱</th><th>状态</th><th>用量</th><th>额度</th><th>操作</th></tr></thead><tbody>'+rows+'</tbody></table></div>';
    }
    function remoteStats(stats){stats=stats||{};return '请求 '+(stats.requests||0)+' / Token '+((stats.inputTokens||0)+(stats.outputTokens||0))+' / 错误 '+(stats.errors||0)}
    async function loadWorkspaceTelemetry(){
      const device=currentWorkspaceDevice();
      if(!device||workspaceTelemetry.loading)return;
      if(workspaceTelemetry.loaded){renderWorkspaceTelemetry();return}
      workspaceTelemetry.loading=true;
      workspacePanelErrors.innerHTML='<div class="workspace-empty">正在加载端点错误遥测</div>';
      try{
        const now=Date.now();
        const from24=new Date(now-24*60*60*1000).toISOString();
        const from7=new Date(now-7*24*60*60*1000).toISOString();
        const base='/api/admin/telemetry/endpoint-errors/summary?deviceId='+encodeURIComponent(device.deviceId)+'&limit=50&from=';
        const results=await Promise.all([api(base+encodeURIComponent(from24)),api(base+encodeURIComponent(from7))]);
        workspaceTelemetry.summary24h=results[0].summary||[];
        workspaceTelemetry.summary7d=results[1].summary||[];
        workspaceTelemetry.error='';
        workspaceTelemetry.loaded=true;
      }catch(err){
        workspaceTelemetry.error=err.message||String(err);
        workspaceTelemetry.loaded=true;
      }finally{
        workspaceTelemetry.loading=false;
        renderWorkspaceTelemetry();
      }
    }
    function setWorkspaceTelemetryRange(range){workspaceTelemetry.range=range==='7d'?'7d':'24h';renderWorkspaceTelemetry()}
    function renderWorkspaceTelemetry(){
      const rows=workspaceTelemetry.range==='7d'?workspaceTelemetry.summary7d:workspaceTelemetry.summary24h;
      const title=workspaceTelemetry.range==='7d'?'近7天':'近24小时';
      const content=workspaceTelemetry.error?'<div class="status-failed">'+esc(workspaceTelemetry.error)+'</div>':(rows.length?renderEndpointErrorTelemetryTable(title,rows):'<div class="workspace-empty">暂无端点错误遥测</div>');
      workspacePanelErrors.innerHTML='<div class="workspace-toolbar"><div><h3>端点错误遥测</h3><div class="muted">诊断数据按需加载，不影响端点配置刷新。</div></div><div class="telemetry-range"><button class="secondary small-btn '+(workspaceTelemetry.range==='24h'?'active':'')+'" onclick="setWorkspaceTelemetryRange(&quot;24h&quot;)">近24小时</button><button class="secondary small-btn '+(workspaceTelemetry.range==='7d'?'active':'')+'" onclick="setWorkspaceTelemetryRange(&quot;7d&quot;)">近7天</button></div></div>'+content;
    }
    function renderEndpointErrorTelemetryTable(title,rows){rows=rows||[];return '<div class="muted" style="margin:6px 0">'+esc(title)+'</div><table><thead><tr><th>端点</th><th>API Host</th><th>原因</th><th>状态码</th><th>次数</th><th>最近</th><th>样例</th></tr></thead><tbody>'+(rows.length?rows.map(row=>'<tr><td>'+esc(row.endpointName||'-')+'</td><td>'+esc(row.apiHost||'-')+'</td><td>'+esc(row.reason||'-')+'</td><td>'+esc(row.statusCode||'-')+'</td><td>'+esc(row.count||0)+'</td><td>'+dt(row.lastAt)+'</td><td class="mono">'+esc(row.sample||'-')+'</td></tr>').join(''):'<tr><td colspan="7" class="empty">暂无</td></tr>')+'</tbody></table>'}
    function remoteQuota(q){if(!q)return '-';try{return JSON.stringify(q.data||q).slice(0,160)}catch(e){return '-'}}
    function escAttr(v){return String(v||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
    function remoteCommandStatusName(status){return ({queued:'等待客户端',delivered:'客户端执行中',applied:'已应用',failed:'失败',expired:'已过期'}[status]||status||'-')}
    function remoteCommandStatusClass(status){return ({queued:'queued',delivered:'delivered',applied:'applied',failed:'failed',expired:'failed'}[status]||'offline')}
    function remoteFieldName(field){return ({name:'名称',apiUrl:'Base URL',apiKey:'API Key',authMode:'认证模式',transformer:'转换器',model:'模型',thinking:'推理强度',codexFastMode:'快速模式',maxConcurrentRequests:'限制并发',enabled:'启停状态',order:'端点顺序',accessToken:'Token',secret:'敏感信息'}[field]||field)}
    function remoteCommandSummaryText(command){
      const summary=(command&&command.summary)||{};
      let target=summary.targetName||'';
      if(!target&&summary.credentialId)target='凭证 #'+summary.credentialId;
      if(!target)target=summary.targetType==='endpoint'?'端点':'远程配置';
      const fields=(summary.changedFields||[]).map(remoteFieldName);
      return target+(fields.length?'：'+fields.join('、'):'');
    }
    function renderWorkspaceCommands(){
      const commands=(workspaceData&&workspaceData.commands)||[];
      if(!commands.length){workspacePanelCommands.innerHTML='<div class="workspace-empty">暂无远程命令记录</div>';return}
      const rows=commands.map(command=>{
        const retry=(command.status==='failed'||command.status==='expired')?remoteRetryButton(command):'';
        return '<tr><td><span class="status-'+remoteCommandStatusClass(command.status)+'">'+esc(remoteCommandStatusName(command.status))+'</span></td><td>'+esc(remoteCommandSummaryText(command))+'<br><span class="mono muted">'+esc(command.commandType)+'</span></td><td>'+esc(command.actorName||'-')+'</td><td>'+dt(command.createdAt)+'<br><span class="muted">更新 '+dt(command.updatedAt)+'</span></td><td class="status-failed">'+esc(command.error||'-')+'</td><td>'+retry+'</td></tr>';
      }).join('');
      workspacePanelCommands.innerHTML='<div class="workspace-toolbar"><div><h3>命令记录</h3><div class="muted">最近 20 条远程操作；摘要不包含密钥、Token、URL 或修改值。</div></div><button class="secondary small-btn" onclick="loadWorkspaceRemote()">刷新</button></div><div class="remote-table-wrap"><table><thead><tr><th>状态</th><th>操作摘要</th><th>操作者</th><th>时间</th><th>错误</th><th>重试</th></tr></thead><tbody>'+rows+'</tbody></table></div>';
    }
    function remoteRetryButton(command){
      const summary=command.summary||{};
      if(command.commandType==='endpoint.update'&&summary.targetName)return '<button class="secondary small-btn" onclick="retryRemoteCommand('+command.id+')">重新编辑</button>';
      if(command.commandType==='endpoint.create')return '<button class="secondary small-btn" onclick="retryRemoteCommand('+command.id+')">重新新增</button>';
      if(command.commandType==='endpoint.reorder')return '<button class="secondary small-btn" onclick="retryRemoteCommand('+command.id+')">重新排序</button>';
      if(command.commandType==='credential.updateToken'&&summary.credentialId)return '<button class="secondary small-btn" onclick="retryRemoteCommand('+command.id+')">重新编辑</button>';
      return '-';
    }
    function retryRemoteCommand(commandID){
      const command=((workspaceData&&workspaceData.commands)||[]).find(item=>item.id===commandID);
      if(!command)return;
      const summary=command.summary||{};
      if(command.commandType==='endpoint.update'){
        const endpoints=((((workspaceData||{}).state||{}).snapshot||{}).endpoints)||[];
        const index=endpoints.findIndex(ep=>ep.name===summary.targetName);
        if(index>=0)openRemoteEndpointEditor(workspaceDeviceIndex,index);
      }else if(command.commandType==='endpoint.create'){
        openRemoteEndpointEditor(workspaceDeviceIndex,-1);
      }else if(command.commandType==='endpoint.reorder'){
        openRemoteSortDialog();
      }else if(command.commandType==='credential.updateToken'&&summary.credentialId){
        openRemoteTokenDialog(summary.credentialId);
      }
    }
    function renderWorkspaceLicenses(){
      const device=currentWorkspaceDevice();
      if(!device)return;
      const rows=(device.licenses||[]).length?licenseRows(device):'<tr><td colspan="7" class="empty">暂无授权明细</td></tr>';
      workspacePanelLicenses.innerHTML='<div class="workspace-toolbar"><div><h3>授权明细</h3><div class="muted">卡密兑换、累计到期和失效记录。</div></div></div><div class="remote-table-wrap"><table><thead><tr><th>卡ID</th><th>状态</th><th>套餐</th><th>兑换时间</th><th>该次累计到期</th><th>客户/备注</th><th>操作</th></tr></thead><tbody>'+rows+'</tbody></table></div>';
    }
    function renderWorkspaceTasks(){
      const commands=(workspaceData&&workspaceData.commands)||[];
      const active=commands.filter(command=>command.status==='queued'||command.status==='delivered');
      let html=active.slice(0,4).map(command=>'<div class="workspace-task"><div><strong>'+esc(remoteCommandStatusName(command.status))+'</strong> · '+esc(remoteCommandSummaryText(command))+'<div class="muted">'+(command.status==='queued'?'前 5 分钟内上线即可执行':'客户端已经领取命令')+'</div></div><span class="mono">#'+command.id+'</span></div>').join('');
      if(workspaceCommandPollError)html+='<div class="workspace-task error"><div><strong>状态暂不可用</strong><div>'+esc(workspaceCommandPollError)+'</div></div></div>';
      workspaceTaskBar.innerHTML=html;
    }
    function pendingRemoteCommands(){return ((workspaceData&&workspaceData.commands)||[]).filter(command=>command.status==='queued'||command.status==='delivered')}
    function hasPendingRemoteTarget(targetType,targetName,credentialID){
      return pendingRemoteCommands().some(command=>{
        const summary=command.summary||{};
        if(summary.targetType!==targetType)return false;
        if(targetType==='credential')return Number(summary.credentialId||0)===Number(credentialID||0);
        return String(summary.targetName||'')===String(targetName||'');
      });
    }
    function hasPendingRemoteOrder(){return pendingRemoteCommands().some(command=>((command.summary||{}).changedFields||[]).includes('order'))}
    function upsertWorkspaceCommand(command){
      if(!workspaceData)workspaceData={state:{},commands:[]};
      workspaceData.commands=workspaceData.commands||[];
      const index=workspaceData.commands.findIndex(item=>item.id===command.id);
      if(index>=0)workspaceData.commands[index]=command;else workspaceData.commands.unshift(command);
    }
    async function queueRemote(index,commandType,payload){
      const device=(window.deviceRows||[])[index];
      if(!device)throw new Error('设备不存在');
      const command=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote/commands',{method:'POST',body:JSON.stringify({commandType,payload})});
      upsertWorkspaceCommand(command);
      workspaceCommandPollError='';
      renderWorkspaceTasks();
      renderWorkspaceCommands();
      renderWorkspaceEndpoints();
      renderWorkspaceTokenPools();
      trackRemoteCommand(device.deviceId,command);
      message.textContent='远程命令已排队，可在设备操作台查看状态';
      return command;
    }
    function trackRemoteCommand(deviceID,command){
      if(!command||!(command.status==='queued'||command.status==='delivered')||remoteCommandPollers.has(command.id))return;
      remoteCommandPollers.set(command.id,{deviceID,startedAt:Date.now(),timer:0});
      scheduleRemoteCommandPoll(command.id,1000);
    }
    function scheduleRemoteCommandPoll(commandID,delay){
      const tracked=remoteCommandPollers.get(commandID);
      if(!tracked)return;
      clearTimeout(tracked.timer);
      tracked.timer=setTimeout(()=>pollTrackedRemoteCommand(commandID),delay);
    }
    async function pollTrackedRemoteCommand(commandID){
      const tracked=remoteCommandPollers.get(commandID);
      const device=currentWorkspaceDevice();
      if(!tracked||deviceWorkspace.hidden||!device||device.deviceId!==tracked.deviceID){remoteCommandPollers.delete(commandID);return}
      try{
        const command=await api('/api/admin/devices/'+encodeURIComponent(tracked.deviceID)+'/remote/commands/'+commandID);
        upsertWorkspaceCommand(command);
        workspaceCommandPollError='';
        renderWorkspaceTasks();
        renderWorkspaceCommands();
        renderWorkspaceEndpoints();
        renderWorkspaceTokenPools();
        if(command.status==='queued'||command.status==='delivered'){
          scheduleRemoteCommandPoll(commandID,Date.now()-tracked.startedAt<15000?1000:3000);
        }else{
          remoteCommandPollers.delete(commandID);
          if(command.status==='applied')await loadWorkspaceRemote();
        }
      }catch(err){
        workspaceCommandPollError=err.message||String(err);
        renderWorkspaceTasks();
        scheduleRemoteCommandPoll(commandID,3000);
      }
    }
    function resumeWorkspaceCommands(){
      const device=currentWorkspaceDevice();
      if(!device)return;
      pendingRemoteCommands().forEach(command=>trackRemoteCommand(device.deviceId,command));
    }
    function stopRemoteCommandPollers(){
      remoteCommandPollers.forEach(tracked=>clearTimeout(tracked.timer));
      remoteCommandPollers.clear();
    }
    function remoteSyncEndpointAuthMode(){
      const isCodex=remoteEndpointAuthMode.value==='codex_token_pool';
      remoteEndpointCodexFastMode.disabled=!isCodex;
      if(!isCodex)remoteEndpointCodexFastMode.checked=false;
    }
    function openRemoteEndpointEditor(index,endpointIndex){
      const state=(workspaceData&&workspaceData.state)||{};
      const endpoints=((state.snapshot||{}).endpoints)||[];
      const ep=endpointIndex>=0?endpoints[endpointIndex]:null;
      const hasThinking=!!ep&&Object.prototype.hasOwnProperty.call(ep,'thinking');
      const supportsThinkingV2=remoteSupportsThinkingV2(state);
      const originalThinking=ep&&hasThinking?String(ep.thinking||''):'';
      remoteEndpointEditorContext={index,mode:ep?'edit':'create',endpointName:ep?ep.name:'',original:ep?Object.assign({},ep):null,thinkingKnown:!!ep&&(hasThinking||supportsThinkingV2),supportsNullableUpdates:!ep||supportsThinkingV2};
      remoteEndpointDialogTitle.textContent=ep?'编辑远程端点':'新增远程端点';
      remoteEndpointName.value=ep?ep.name:'';
      remoteEndpointAPIUrl.value=ep?ep.apiUrl:'';
      remoteEndpointAPIKey.value='';
      remoteEndpointAPIKeyHelp.textContent=ep?'留空保持当前 Key；输入新值将覆盖客户端配置。':'API Key 模式必须填写；Token Pool 模式可留空。';
      remoteEndpointAuthMode.value=ep?(ep.authMode||'api_key'):'api_key';
      remoteEndpointTransformer.value=ep?(ep.transformer||'openai'):'openai';
      remoteEndpointModel.value=ep?String(ep.model||''):'gpt-5';
      document.getElementById('remoteEndpointThinkingDefault').disabled=!!ep&&!supportsThinkingV2;
      remoteEndpointThinking.value=ep?(supportsThinkingV2?originalThinking:(hasThinking&&originalThinking?originalThinking:'__keep__')):'';
      remoteEndpointMaxConcurrentRequests.value=ep?String(ep.maxConcurrentRequests||0):'0';
      remoteEndpointCodexFastMode.checked=!!(ep&&ep.codexFastMode);
      remoteEndpointEnabled.checked=ep?!!ep.enabled:true;
      remoteEndpointThinkingHelp.textContent=!ep||supportsThinkingV2?'该客户端支持显示推理强度并恢复上游默认。':'旧客户端未上报推理强度；默认保持不变，仍可下发明确强度。';
      remoteSyncEndpointAuthMode();
      remoteEndpointDialog.showModal();
    }
    async function submitRemoteEndpoint(event){
      event.preventDefault();
      if(!remoteEndpointEditorContext)return;
      const context=remoteEndpointEditorContext;
      const name=remoteEndpointName.value.trim();
      const apiUrl=remoteEndpointAPIUrl.value.trim();
      const apiKey=remoteEndpointAPIKey.value.trim();
      const authMode=remoteEndpointAuthMode.value;
      const transformer=remoteEndpointTransformer.value;
      const model=remoteEndpointModel.value.trim();
      const thinking=remoteEndpointThinking.value;
      const maxConcurrentRequests=Number(remoteEndpointMaxConcurrentRequests.value||0);
      const enabled=remoteEndpointEnabled.checked;
      const codexFastMode=remoteEndpointCodexFastMode.checked;
      if(!name||!apiUrl){setError(new Error('端点名称和 Base URL 不能为空'));return}
      if(context.mode==='create'&&authMode==='api_key'&&!apiKey){setError(new Error('API Key 模式必须填写 API Key'));return}
      if(!Number.isInteger(maxConcurrentRequests)||maxConcurrentRequests<0){setError(new Error('限制并发必须是 0 或正整数'));return}
      const pendingTarget=context.mode==='create'?name:context.endpointName;
      if(hasPendingRemoteTarget('endpoint',pendingTarget,0)){setError(new Error('该端点已有待执行远程命令，请等待完成后重试'));return}
      const payload=context.mode==='create'?{name,apiUrl,apiKey,authMode,transformer,model,thinking:thinking==='__keep__'?'':thinking,maxConcurrentRequests,enabled,codexFastMode}:{endpointName:context.endpointName};
      const changed=[];
      if(context.mode==='edit'){
        const original=context.original||{};
        if(name!==original.name){payload.name=name;changed.push('name')}
        if(apiUrl!==String(original.apiUrl||'')){payload.apiUrl=apiUrl;changed.push('apiUrl')}
        if(apiKey){payload.apiKey=apiKey;changed.push('apiKey')}
        if(authMode!==String(original.authMode||'')){payload.authMode=authMode;changed.push('authMode')}
        if(transformer!==String(original.transformer||'')){payload.transformer=transformer;changed.push('transformer')}
        if(model===''&&String(original.model||'')!==''&&!context.supportsNullableUpdates){setError(new Error('旧客户端不支持远程清空模型，请升级客户端或填写新的模型名'));return}
        if(model!==String(original.model||'')){payload.model=model;changed.push('model')}
        if(thinking!=='__keep__'&&(!context.thinkingKnown||thinking!==String(original.thinking||''))){payload.thinking=thinking;changed.push('thinking')}
        if(maxConcurrentRequests!==Number(original.maxConcurrentRequests||0)){payload.maxConcurrentRequests=maxConcurrentRequests;changed.push('maxConcurrentRequests')}
        if(enabled!==!!original.enabled){payload.enabled=enabled;changed.push('enabled')}
        if(codexFastMode!==!!original.codexFastMode){payload.codexFastMode=codexFastMode;changed.push('codexFastMode')}
        if(!changed.length){remoteEndpointDialog.close();message.textContent='端点配置没有变化';return}
      }else{
        changed.push('name','apiUrl');
        if(apiKey)changed.push('apiKey');
        changed.push('authMode','transformer','model','thinking','maxConcurrentRequests','enabled','codexFastMode');
      }
      const risk=changed.includes('apiKey')||changed.includes('authMode')?'sensitive':'normal';
      const confirmed=await confirmRemoteAction(context.mode==='create'?'确认新增远程端点':'确认修改远程端点',workspaceOfflineMessage(),changed.map(remoteFieldName),risk,'');
      if(!confirmed)return;
      try{
        await queueRemote(context.index,context.mode==='create'?'endpoint.create':'endpoint.update',payload);
        remoteEndpointDialog.close();
      }catch(err){setError(err)}
    }
    function workspaceOfflineMessage(){const online=workspaceOnlineState((workspaceData&&workspaceData.state)||{});return online.key==='online'?'客户端在线，命令将在下一次轮询时执行。':'设备当前'+online.label+'；命令会排队，前 5 分钟内上线即可执行。'}
    async function remoteToggleEndpoint(index,name,enabled){try{await queueRemote(index,'endpoint.update',{endpointName:name,enabled})}catch(err){setError(err)}}
    async function remoteDeleteEndpoint(index,name){
      const confirmed=await confirmRemoteAction('删除远程端点','删除后需要客户端重新创建才能恢复。',[name],'destructive',name);
      if(!confirmed)return;
      try{await queueRemote(index,'endpoint.delete',{endpointName:name})}catch(err){setError(err)}
    }
    function openRemoteSortDialog(){
      const endpoints=((workspaceData&&workspaceData.state&&workspaceData.state.snapshot)||{}).endpoints||[];
      remoteSortNames=endpoints.map(ep=>ep.name);
      renderRemoteSortList();
      remoteSortDialog.showModal();
    }
    function renderRemoteSortList(){remoteSortList.innerHTML=remoteSortNames.map((name,index)=>'<div class="sort-row"><span>'+esc(name)+'</span><button type="button" class="secondary small-btn" '+(index===0?'disabled':'')+' onclick="moveRemoteSortItem('+index+',-1)">上移</button><button type="button" class="secondary small-btn" '+(index===remoteSortNames.length-1?'disabled':'')+' onclick="moveRemoteSortItem('+index+',1)">下移</button></div>').join('')}
    function moveRemoteSortItem(index,direction){const target=index+direction;if(target<0||target>=remoteSortNames.length)return;const value=remoteSortNames[index];remoteSortNames[index]=remoteSortNames[target];remoteSortNames[target]=value;renderRemoteSortList()}
    async function submitRemoteSort(event){
      event.preventDefault();
      const current=((((workspaceData||{}).state||{}).snapshot||{}).endpoints||[]).map(ep=>ep.name);
      if(JSON.stringify(current)===JSON.stringify(remoteSortNames)){remoteSortDialog.close();return}
      if(!await confirmRemoteAction('确认调整端点顺序',workspaceOfflineMessage(),['端点顺序'],'normal',''))return;
      try{await queueRemote(workspaceDeviceIndex,'endpoint.reorder',{names:remoteSortNames});remoteSortDialog.close()}catch(err){setError(err)}
    }
    async function remoteCredentialEnabled(index,id,enabled){try{await queueRemote(index,'credential.setEnabled',{credentialId:id,enabled})}catch(err){setError(err)}}
    function openRemoteTokenDialog(id){remoteTokenCredentialID=id;remoteTokenCredentialLabel.textContent='凭证 #'+id;remoteTokenAccessToken.value='';remoteTokenDialog.showModal()}
    async function submitRemoteToken(event){
      event.preventDefault();
      const accessToken=remoteTokenAccessToken.value.trim();
      if(!accessToken)return;
      if(!await confirmRemoteAction('确认更新 Token',workspaceOfflineMessage(),['凭证 #'+remoteTokenCredentialID,'Token'],'sensitive',''))return;
      try{await queueRemote(workspaceDeviceIndex,'credential.updateToken',{credentialId:remoteTokenCredentialID,accessToken});remoteTokenDialog.close()}catch(err){setError(err)}
    }
    async function remoteDeleteCredential(index,id){
      const expected=String(id);
      if(!await confirmRemoteAction('删除远程凭证','删除后无法通过后台恢复。',['凭证 #'+id],'destructive',expected))return;
      try{await queueRemote(index,'credential.delete',{credentialId:id})}catch(err){setError(err)}
    }
    function confirmRemoteAction(title,message,summary,risk,expected){
      remoteConfirmTitle.textContent=title;
      remoteConfirmMessage.textContent=message||'';
      remoteConfirmSummary.innerHTML=(summary||[]).map(item=>'<div>• '+esc(item)+'</div>').join('')||'<div>无字段变化</div>';
      remoteConfirmButton.className=risk==='normal'?'':'danger';
      remoteConfirmButton.textContent=risk==='destructive'?'确认删除':'确认下发';
      remoteConfirmationExpected=String(expected||'');
      remoteConfirmInput.value='';
      remoteConfirmInput.hidden=!remoteConfirmationExpected;
      remoteConfirmInputLabel.hidden=!remoteConfirmationExpected;
      remoteConfirmInputLabel.textContent=remoteConfirmationExpected?'请输入 '+remoteConfirmationExpected+' 以确认':'确认内容';
      remoteConfirmDialog.showModal();
      return new Promise(resolve=>{remoteConfirmationResolve=resolve});
    }
    function resolveRemoteConfirmation(confirmed){
      if(confirmed&&remoteConfirmationExpected&&remoteConfirmInput.value.trim()!==remoteConfirmationExpected){remoteConfirmInput.setCustomValidity('输入内容不匹配');remoteConfirmInput.reportValidity();remoteConfirmInput.setCustomValidity('');return}
      remoteConfirmDialog.close();
      const resolve=remoteConfirmationResolve;
      remoteConfirmationResolve=null;
      remoteConfirmationExpected='';
      if(resolve)resolve(!!confirmed);
    }
    async function remoteRevealSecret(index,endpointName,credentialId,field){
      const device=(window.deviceRows||[])[index];
      if(!device)return;
      try{
        if(!window.isSecureContext||!crypto.subtle)throw new Error('查看明文需要 HTTPS 安全后台；当前 HTTP 页面只允许远程维护，不展示密钥明文。');
        clearRemoteSecret();
        remoteSecretStatus.textContent='等待客户端返回加密结果';
        remoteSecretValue.textContent='等待中';
        remoteSecretCopyButton.disabled=true;
        remoteSecretDialog.showModal();
        const keyPair=await createRevealKeyPair();
        const command=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote/secrets/reveal',{method:'POST',body:JSON.stringify({endpointName,credentialId,field,adminPublicKey:keyPair.publicKey})});
        upsertWorkspaceCommand(command);
        renderWorkspaceTasks();
        renderWorkspaceCommands();
        await waitForRemoteSecretCommand(device.deviceId,command,keyPair);
      }catch(err){
        remoteSecretStatus.textContent=err.message||String(err);
        remoteSecretValue.textContent='无法显示';
      }
    }
    async function waitForRemoteSecretCommand(deviceID,queued,keyPair){
      const deadline=queued.expiresAt?Date.parse(queued.expiresAt):Date.now()+2*60*1000;
      const started=Date.now();
      while(Date.now()<deadline&&remoteSecretDialog.open){
        await sleep(Date.now()-started<15000?1000:3000);
        const command=await api('/api/admin/devices/'+encodeURIComponent(deviceID)+'/remote/commands/'+queued.id);
        upsertWorkspaceCommand(command);
        renderWorkspaceTasks();
        renderWorkspaceCommands();
        remoteSecretStatus.textContent=remoteCommandStatusName(command.status);
        if(command.status==='failed'||command.status==='expired')throw new Error(command.error||remoteCommandStatusName(command.status));
        if(command.status==='applied'){
          const reveal=command.resultJson&&command.resultJson.secretReveal;
          if(!reveal)throw new Error('客户端未返回加密敏感信息');
          const plain=await decryptRevealResult(keyPair,reveal);
          showRemoteSecret(plain.value,reveal.expiresAt);
          await loadWorkspaceRemote();
          return;
        }
      }
      throw new Error('一次性敏感信息已过期，请重新请求');
    }
    function showRemoteSecret(value,expiresAt){
      remoteSecretPlaintext=String(value||'');
      remoteSecretValue.textContent=remoteSecretPlaintext||'空值';
      remoteSecretCopyButton.disabled=!remoteSecretPlaintext;
      remoteSecretStatus.textContent='仅在当前安全弹窗中临时显示';
      const update=()=>{
        const remaining=Math.max(0,Math.ceil((Date.parse(expiresAt)-Date.now())/1000));
        remoteSecretCountdown.textContent='剩余有效时间 '+remaining+' 秒';
        if(remaining<=0){clearRemoteSecret();return}
        remoteSecretClearTimer=setTimeout(update,1000);
      };
      update();
    }
    function clearRemoteSecret(closeDialog=true){
      clearTimeout(remoteSecretClearTimer);
      remoteSecretClearTimer=0;
      remoteSecretPlaintext='';
      if(typeof remoteSecretValue!=='undefined')remoteSecretValue.textContent='已清除';
      if(typeof remoteSecretCountdown!=='undefined')remoteSecretCountdown.textContent='';
      if(typeof remoteSecretCopyButton!=='undefined')remoteSecretCopyButton.disabled=true;
      if(closeDialog&&typeof remoteSecretDialog!=='undefined'&&remoteSecretDialog.open)remoteSecretDialog.close();
    }
    async function copyRemoteSecret(){if(remoteSecretPlaintext)await navigator.clipboard.writeText(remoteSecretPlaintext)}
    function sleep(ms){return new Promise(resolve=>setTimeout(resolve,ms))}
    function b64u(bytes){return btoa(String.fromCharCode(...new Uint8Array(bytes))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')}
    function b64uBytes(value){value=String(value||'').replace(/-/g,'+').replace(/_/g,'/');while(value.length%4)value+='=';return Uint8Array.from(atob(value),c=>c.charCodeAt(0))}
    async function createRevealKeyPair(){const pair=await crypto.subtle.generateKey({name:'ECDH',namedCurve:'P-256'},true,['deriveBits']);const publicRaw=await crypto.subtle.exportKey('raw',pair.publicKey);return {privateKey:pair.privateKey,publicRaw:new Uint8Array(publicRaw),publicKey:b64u(publicRaw)}}
    async function decryptRevealResult(keyPair,result){const info=new TextEncoder().encode('ainexus-license-remote-reveal-v1');const clientPublicRaw=b64uBytes(result.clientPublicKey);const peer=await crypto.subtle.importKey('raw',clientPublicRaw,{name:'ECDH',namedCurve:'P-256'},false,[]);const shared=await crypto.subtle.deriveBits({name:'ECDH',public:peer},keyPair.privateKey,256);const salt=new Uint8Array(clientPublicRaw.length+keyPair.publicRaw.length);salt.set(clientPublicRaw,0);salt.set(keyPair.publicRaw,clientPublicRaw.length);const hkdf=await crypto.subtle.importKey('raw',shared,'HKDF',false,['deriveKey']);const aesKey=await crypto.subtle.deriveKey({name:'HKDF',hash:'SHA-256',salt,info},hkdf,{name:'AES-GCM',length:256},false,['decrypt']);const plain=await crypto.subtle.decrypt({name:'AES-GCM',iv:b64uBytes(result.nonce),additionalData:info},aesKey,b64uBytes(result.ciphertext));return JSON.parse(new TextDecoder().decode(plain))}
    function openExpiry(index){const device=window.deviceRows[index];editingDeviceId=device.deviceId;expiryDevice.textContent=device.deviceId;expiryInput.value=toLocalInput(device.expiresAt);expiryDialog.showModal()}
    async function submitExpiry(event){event.preventDefault();try{await api('/api/admin/devices/expiry',{method:'PATCH',body:JSON.stringify({deviceId:editingDeviceId,expiresAt:new Date(expiryInput.value).toISOString()})});expiryDialog.close();await refreshAll();}catch(err){setError(err)}}
    function openRemark(index){const device=window.deviceRows[index];editingDeviceId=device.deviceId;remarkDevice.textContent=device.deviceId;remarkInput.value=device.remark||'';remarkDialog.showModal()}
    async function submitRemark(event){event.preventDefault();try{await api('/api/admin/devices/remark',{method:'PATCH',body:JSON.stringify({deviceId:editingDeviceId,remark:remarkInput.value})});remarkDialog.close();await refreshAll();}catch(err){setError(err)}}
    async function disableCard(id){if(confirm('禁用这张卡密？该卡对应的激活会立即失效，到期时间会同步调整。')){try{await api('/api/admin/cards/'+id+'/disable',{method:'POST'});await refreshAll();}catch(err){setError(err);}}}
    async function deleteCard(id){if(confirm('删除这张卡密及其设备激活记录？')){try{await api('/api/admin/cards/'+id,{method:'DELETE'});await refreshAll();}catch(err){setError(err);}}}
    async function disableActivation(id){if(confirm('禁用这条授权明细？它的到期时间会立即调整。')){try{await api('/api/admin/activations/'+id+'/disable',{method:'POST'});await refreshAll();}catch(err){setError(err);}}}
    async function refreshAll(){setError('');try{await refreshMe();await refreshAccounts();await Promise.all([refreshCards(),refreshDevices(),refreshHistory()]);}catch(err){setError(err);}}
    async function copyGenerated(){try{await navigator.clipboard.writeText(generated.textContent || '');}catch(err){setError(err);}}
    async function logout(){try{await api('/api/admin/logout',{method:'POST'});}finally{location.replace('/admin/login');}}
    showPage((location.hash || '#cards').slice(1));
    refreshAll();
  </script>
</body>
</html>`
