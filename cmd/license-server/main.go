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

	service := onlinelicense.NewService(store, privateKey, onlinelicense.Options{})
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
	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
    .device-detail td{padding:0;background:#fbfbfd}.device-detail[hidden]{display:none}.detail-inner{padding:14px 16px}.detail-inner table{background:#fff}.detail-inner th{position:static}.detail-label{font-size:12px;font-weight:700;color:var(--muted);margin:12px 0 8px}
    dialog{width:min(480px,calc(100% - 32px));border:1px solid var(--line);border-radius:8px;padding:0;color:var(--text);box-shadow:0 28px 80px rgba(29,29,31,.22)}dialog::backdrop{background:rgba(29,29,31,.34);backdrop-filter:blur(2px)}.dialog-body{padding:20px}.dialog-body h2{font-size:18px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:16px}.remote-endpoint-dialog{width:min(680px,calc(100% - 32px))}.remote-table-wrap{width:100%;max-width:calc(100vw - 340px);min-width:0;overflow:auto;contain:inline-size;border:1px solid var(--soft-line);border-radius:8px;background:#fff}.remote-table-wrap table{min-width:1180px}
    #generated{white-space:pre-wrap;word-break:break-all;background:#fff;border:1px solid var(--soft-line);border-radius:8px;padding:12px;margin-top:12px;min-height:188px;max-height:280px;overflow:auto}.message{min-height:20px;margin-top:12px;color:var(--danger);font-size:13px}.empty{text-align:center;color:var(--muted);padding:26px!important}
    @media(max-width:1100px){.admin-shell{grid-template-columns:1fr}.sidebar{position:relative;height:auto;border-right:0;border-bottom:1px solid var(--soft-line)}.page-tabs{grid-template-columns:repeat(5,minmax(0,1fr))}.content{padding:20px}.topbar{margin:-20px -20px 18px;padding:16px 20px}.overview-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.generate-grid{grid-template-columns:1fr}.remote-table-wrap{max-width:calc(100vw - 40px)}}
    @media(max-width:720px){html,body{overflow-x:hidden}.topbar{align-items:flex-start;flex-direction:column}.toolbar{width:100%;justify-content:space-between}.page-tabs{grid-template-columns:1fr 1fr}.overview-grid,.row{grid-template-columns:1fr}.content{width:100%;max-width:100vw;overflow-x:hidden;padding:14px}.topbar{margin:-14px -14px 16px;padding:14px}.sidebar{padding:16px}.permission-grid{grid-template-columns:1fr}.remote-table-wrap{max-width:calc(100vw - 28px)}}
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
  <dialog id="remoteCreateEndpointDialog" class="remote-endpoint-dialog">
    <form class="dialog-body" onsubmit="submitRemoteCreateEndpoint(event)">
      <h2>新增远程端点</h2>
      <div class="row">
        <div><label for="remoteCreateName">端点名称</label><input id="remoteCreateName" required></div>
        <div><label for="remoteCreateAuthMode">认证模式</label><select id="remoteCreateAuthMode" onchange="remoteSyncCreateAuthMode()"><option value="api_key">API Key</option><option value="token_pool">Token Pool</option><option value="codex_token_pool">Codex Token Pool</option><option value="claude_oauth_token_pool">Claude OAuth Token Pool</option></select></div>
      </div>
      <label for="remoteCreateAPIUrl">Base URL</label>
      <input id="remoteCreateAPIUrl" required>
      <label for="remoteCreateAPIKey">API Key</label>
      <input id="remoteCreateAPIKey" type="password" autocomplete="new-password" placeholder="Token Pool 模式可留空">
      <div class="row">
        <div><label for="remoteCreateTransformer">转换器</label><select id="remoteCreateTransformer"><option value="claude">Claude</option><option value="openai">OpenAI Chat</option><option value="openai2">OpenAI Responses</option><option value="gemini">Gemini</option><option value="deepseek">DeepSeek</option><option value="kimi">Kimi</option><option value="poe">Poe</option></select></div>
        <div><label for="remoteCreateModel">模型</label><input id="remoteCreateModel" value="gpt-5"></div>
      </div>
      <div class="row">
        <div><label for="remoteCreateThinking">推理强度</label><select id="remoteCreateThinking"><option value="">上游默认</option><option value="off">关闭</option><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="xhigh">XHigh / Max</option></select></div>
        <div><label for="remoteCreateMaxConcurrentRequests">限制并发</label><input id="remoteCreateMaxConcurrentRequests" type="number" min="0" step="1" value="0"></div>
      </div>
      <label class="inline-check"><input id="remoteCreateCodexFastMode" type="checkbox" disabled> Codex 快速模式</label>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="remoteCreateEndpointDialog.close()">取消</button><button type="submit">下发新增命令</button></div>
    </form>
  </dialog>
  <dialog id="remoteEditEndpointDialog">
    <form class="dialog-body" onsubmit="submitRemoteEndpointEdit(event)">
      <h2>修改模型与推理</h2>
      <div id="remoteEditEndpointName" class="mono muted"></div>
      <label for="remoteEditModel">模型</label>
      <input id="remoteEditModel" placeholder="留空表示不强制覆盖请求模型">
      <label for="remoteEditThinking">推理强度</label>
      <select id="remoteEditThinking"><option value="__keep__">保持不变</option><option id="remoteEditThinkingDefault" value="">上游默认</option><option value="off">关闭</option><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="xhigh">XHigh / Max</option></select>
      <p id="remoteEditThinkingHelp" class="muted"></p>
      <div class="dialog-actions"><button type="button" class="secondary" onclick="remoteEditEndpointDialog.close()">取消</button><button type="submit">下发修改命令</button></div>
    </form>
  </dialog>
  <script>
    let historyRows = [];
    let accountRows = [];
    let currentAccount = null;
    let editingDeviceId = '';
    let remoteCreateDeviceIndex = -1;
    let remoteEditContext = null;
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
        return '<tr><td>'+ownerLabel(d)+'</td><td class="mono"><span id="deviceIdValue-'+index+'">'+privateValue(d.deviceId, revealed)+'</span> <button id="devicePrivacyButton-'+index+'" class="secondary small-btn" onclick="toggleDevicePrivacy('+index+')" title="'+esc(revealed ? '隐藏该行设备ID和IP' : '显示该行设备ID和IP')+'" aria-label="'+esc(revealed ? '隐藏该行设备ID和IP' : '显示该行设备ID和IP')+'">'+(revealed ? '🙈' : '👁')+'</button></td><td>'+esc(d.remark||'-')+'</td><td>'+statusCell(d.status)+'</td><td>'+dt(d.expiresAt)+'</td><td>'+dt(d.lastCheckedAt)+'</td><td>'+esc(d.platform)+'<br><span class="muted">'+esc(d.appVersion)+'</span></td><td class="mono"><span id="deviceIpValue-'+index+'">'+privateValue(d.ipAddress, revealed)+'</span></td><td>'+d.licenses.length+'</td><td><div class="actions"><button class="secondary small-btn" onclick="toggleDevice('+index+')">明细</button>'+(can('devices:remark')?'<button class="secondary small-btn" onclick="openRemark('+index+')">备注</button>':'')+(can('devices:expiry')?'<button class="small-btn" onclick="openExpiry('+index+')">修改到期</button>':'')+(d.status==='active'&&can('activations:disable')?'<button class="danger small-btn" onclick="disableActivation('+d.currentActivationId+')">禁用当前</button>':'')+'</div></td></tr><tr id="device-detail-'+index+'" class="device-detail" hidden><td colspan="10"><div class="detail-inner"><div id="remote-detail-'+index+'">'+(can('devices:remote:view')?'<div class="detail-label">远程端点维护</div><div class="muted">展开后加载远程状态</div>':'')+'</div><div class="detail-label">卡密兑换与失效明细</div><table><thead><tr><th>卡ID</th><th>状态</th><th>套餐</th><th>兑换时间</th><th>该次累计到期</th><th>客户/备注</th><th>操作</th></tr></thead><tbody>'+licenseRows(d)+'</tbody></table></div></td></tr>';
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
    async function toggleDevice(index){const row=document.getElementById('device-detail-'+index);row.hidden=!row.hidden;if(!row.hidden&&can('devices:remote:view'))await loadRemoteDetail(index)}
    async function loadRemoteDetail(index){const box=document.getElementById('remote-detail-'+index);const device=window.deviceRows[index];if(!box||!device)return;box.innerHTML='<div class="detail-label">远程端点维护</div><div class="muted">加载中</div>';try{const data=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote');const now=Date.now();const from24=new Date(now-24*60*60*1000).toISOString();const from7=new Date(now-7*24*60*60*1000).toISOString();let telemetry={summary24h:[],summary7d:[]};try{const base='/api/admin/telemetry/endpoint-errors/summary?deviceId='+encodeURIComponent(device.deviceId)+'&limit=50&from=';const results=await Promise.all([api(base+encodeURIComponent(from24)),api(base+encodeURIComponent(from7))]);telemetry={summary24h:results[0].summary||[],summary7d:results[1].summary||[]}}catch(telemetryErr){telemetry={summary24h:[],summary7d:[],error:telemetryErr.message||String(telemetryErr)}}data.endpointErrorSummary24h=telemetry.summary24h||[];data.endpointErrorSummary7d=telemetry.summary7d||[];data.endpointErrorTelemetryError=telemetry.error||'';window.remoteDetailData=window.remoteDetailData||{};window.remoteDetailData[index]=data;box.innerHTML=renderRemoteDetail(index,device,data)}catch(err){box.innerHTML='<div class="detail-label">远程端点维护</div><div class="status-disabled">'+esc(err.message||err)+'</div>'}}
    function remoteSupportsThinkingV2(state){return ((state&&state.capabilities)||[]).includes('endpoints:thinking:v2')}
    function thinkingDisplay(state,ep){const hasThinking=Object.prototype.hasOwnProperty.call(ep,'thinking');if(!hasThinking&&!remoteSupportsThinkingV2(state))return '未上报';const value=String(ep.thinking||'').trim().toLowerCase();return ({'':'上游默认',off:'关闭',low:'Low',medium:'Medium',high:'High',xhigh:'XHigh / Max'}[value]||value)}
    function renderRemoteDetail(index,device,data){
      const state=(data&&data.state)||{};
      if(!state.supported)return '<div class="detail-label">远程端点维护</div><div class="muted">该客户端版本暂不支持远程维护，不影响正常授权和本地使用。</div>';
      const snap=state.snapshot||{};
      const endpoints=snap.endpoints||[];
      const pools=snap.tokenPools||[];
      const endpointErrorSummary24h=(data&&data.endpointErrorSummary24h)||[];
      const endpointErrorSummary7d=(data&&data.endpointErrorSummary7d)||[];
      const remoteStatus='<div class="detail-label">远程端点维护</div><div class="muted">状态：'+(state.enabled?'已启用':'已关闭')+' · 版本：'+esc(state.clientVersion||'-')+' · 心跳：'+dt(state.lastHeartbeatAt)+'</div>'+(can('devices:remote:write')?'<div class="actions"><button class="small-btn" onclick="remoteCreateEndpoint('+index+')">新增端点</button></div>':'');
      const endpointRows=endpoints.length?endpoints.map((ep,epIndex)=>'<tr><td>'+esc(ep.name)+'</td><td>'+esc(ep.enabled?'启用':'停用')+'</td><td class="mono">'+esc(ep.model||'-')+'</td><td>'+esc(thinkingDisplay(state,ep))+'</td><td class="mono">'+esc(ep.apiUrl||'-')+'</td><td class="mono">'+esc(ep.apiKeyMasked||'-')+'</td><td>'+esc(ep.authMode||'-')+'</td><td>'+esc(ep.codexFastMode?'开启':'关闭')+'</td><td>'+esc((ep.maxConcurrentRequests||0)>0?ep.maxConcurrentRequests:'不限')+'</td><td>'+remoteStats(ep.stats)+'</td><td><div class="actions">'+(can('devices:remote:write')?'<button class="secondary small-btn" '+(epIndex===0?'disabled ':'')+'onclick="remoteMoveEndpoint('+index+','+epIndex+',-1)">上移</button><button class="secondary small-btn" '+(epIndex===endpoints.length-1?'disabled ':'')+'onclick="remoteMoveEndpoint('+index+','+epIndex+',1)">下移</button><button class="secondary small-btn" onclick="remoteOpenEndpointEditor('+index+','+epIndex+')">模型/推理</button><button class="secondary small-btn" onclick="remoteUpdateEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,&quot;apiUrl&quot;)">改URL</button><button class="secondary small-btn" onclick="remoteUpdateEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,&quot;apiKey&quot;)">改Key</button><button class="secondary small-btn" onclick="remoteUpdateEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,&quot;maxConcurrentRequests&quot;)">改并发</button>'+(ep.authMode==='codex_token_pool'?'<button class="secondary small-btn" onclick="remoteSetCodexFastMode('+index+',&quot;'+escAttr(ep.name)+'&quot;,'+(!ep.codexFastMode)+')">'+(ep.codexFastMode?'关闭快速':'开启快速')+'</button>':'')+'<button class="secondary small-btn" onclick="remoteToggleEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,'+(!ep.enabled)+')">'+(ep.enabled?'停用':'启用')+'</button><button class="danger small-btn" onclick="remoteDeleteEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;)">删除</button>':'')+(can('devices:remote:secrets')?'<button class="secondary small-btn" onclick="remoteRevealSecret('+index+',&quot;'+escAttr(ep.name)+'&quot;,0,&quot;apiKey&quot;)">查看Key</button>':'')+'</div></td></tr>').join(''):'<tr><td colspan="11" class="empty">暂无端点快照</td></tr>';
      const poolRows=pools.length?pools.map(pool=>'<tr><td>'+esc(pool.endpointName)+'</td><td colspan="6">'+renderRemoteCredentials(index,pool)+'</td></tr>').join(''):'<tr><td colspan="7" class="empty">暂无 Token Pool 快照</td></tr>';
      return remoteStatus+'<div class="remote-table-wrap"><table><thead><tr><th>端点</th><th>状态</th><th>模型</th><th>推理</th><th>Base URL</th><th>API Key</th><th>模式</th><th>快速</th><th>并发</th><th>用量</th><th>操作</th></tr></thead><tbody>'+endpointRows+'</tbody></table></div><div class="detail-label" style="margin-top:12px">端点错误遥测</div>'+renderEndpointErrorTelemetry(endpointErrorSummary24h,endpointErrorSummary7d,data.endpointErrorTelemetryError)+'<div class="detail-label" style="margin-top:12px">Codex Token Pool</div><table><thead><tr><th>端点</th><th colspan="6">账号/额度/维护</th></tr></thead><tbody>'+poolRows+'</tbody></table>';
    }
    function renderRemoteCredentials(index,pool){const creds=pool.credentials||[];if(!creds.length)return '<span class="muted">暂无账号</span>';return '<table><thead><tr><th>ID</th><th>账号</th><th>邮箱</th><th>状态</th><th>用量</th><th>额度</th><th>操作</th></tr></thead><tbody>'+creds.map(c=>'<tr><td>'+c.id+'</td><td class="mono">'+esc(c.accountIdMasked||'-')+'</td><td>'+esc(c.emailMasked||'-')+'</td><td>'+esc(c.status||'-')+(c.enabled?'':' / 停用')+'</td><td>'+remoteStats(c.usage)+'</td><td class="mono">'+esc(remoteQuota(c.quota))+'</td><td><div class="actions">'+(can('devices:remote:write')?'<button class="secondary small-btn" onclick="remoteCredentialEnabled('+index+','+c.id+','+(!c.enabled)+')">'+(c.enabled?'停用':'启用')+'</button><button class="secondary small-btn" onclick="remoteUpdateCredentialToken('+index+','+c.id+')">改Token</button><button class="danger small-btn" onclick="remoteDeleteCredential('+index+','+c.id+')">删除</button>':'')+(can('devices:remote:secrets')?'<button class="secondary small-btn" onclick="remoteRevealSecret('+index+',&quot;'+escAttr(pool.endpointName)+'&quot;,'+c.id+',&quot;accessToken&quot;)">查看Token</button>':'')+'</div></td></tr>').join('')+'</tbody></table>'}
    function remoteStats(stats){stats=stats||{};return '请求 '+(stats.requests||0)+' / Token '+((stats.inputTokens||0)+(stats.outputTokens||0))+' / 错误 '+(stats.errors||0)}
    function renderEndpointErrorTelemetry(rows24h,rows7d,errorText){rows24h=rows24h||[];rows7d=rows7d||[];if(errorText)return '<div class="status-disabled">'+esc(errorText)+'</div>';if(!rows24h.length&&!rows7d.length)return '<div class="muted">暂无端点错误遥测</div>';return renderEndpointErrorTelemetryTable('近24小时',rows24h)+renderEndpointErrorTelemetryTable('近7天',rows7d)}
    function renderEndpointErrorTelemetryTable(title,rows){rows=rows||[];return '<div class="muted" style="margin:6px 0">'+esc(title)+'</div><table><thead><tr><th>端点</th><th>API Host</th><th>原因</th><th>状态码</th><th>次数</th><th>最近</th><th>样例</th></tr></thead><tbody>'+(rows.length?rows.map(row=>'<tr><td>'+esc(row.endpointName||'-')+'</td><td>'+esc(row.apiHost||'-')+'</td><td>'+esc(row.reason||'-')+'</td><td>'+esc(row.statusCode||'-')+'</td><td>'+esc(row.count||0)+'</td><td>'+dt(row.lastAt)+'</td><td class="mono">'+esc(row.sample||'-')+'</td></tr>').join(''):'<tr><td colspan="7" class="empty">暂无</td></tr>')+'</tbody></table>'}
    function remoteQuota(q){if(!q)return '-';try{return JSON.stringify(q.data||q).slice(0,160)}catch(e){return '-'}}
    function escAttr(v){return String(v||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
    async function queueRemote(index,commandType,payload){const device=window.deviceRows[index];const command=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote/commands',{method:'POST',body:JSON.stringify({commandType,payload})});message.textContent='远程命令已下发，等待客户端拉取';await pollRemoteCommand(device.deviceId,command.id,null);await loadRemoteDetail(index)}
    async function remoteUpdateEndpoint(index,name,field){const label=field==='apiUrl'?'新的 Base URL':(field==='maxConcurrentRequests'?'限制并发（0 表示不限制）':'新的 API Key');const value=prompt(label);if(value===null)return;const payload={endpointName:name};if(field==='maxConcurrentRequests'){const n=Number(value);if(!Number.isInteger(n)||n<0){setError(new Error('限制并发必须是 0 或正整数'));return}payload[field]=n}else{payload[field]=value}try{await queueRemote(index,'endpoint.update',payload)}catch(err){setError(err)}}
    async function remoteToggleEndpoint(index,name,enabled){try{await queueRemote(index,'endpoint.update',{endpointName:name,enabled})}catch(err){setError(err)}}
    async function remoteSetCodexFastMode(index,name,enabled){try{await queueRemote(index,'endpoint.update',{endpointName:name,codexFastMode:enabled})}catch(err){setError(err)}}
    function remoteSyncCreateAuthMode(){const isCodex=remoteCreateAuthMode.value==='codex_token_pool';remoteCreateCodexFastMode.disabled=!isCodex;if(!isCodex)remoteCreateCodexFastMode.checked=false}
    function remoteCreateEndpoint(index){remoteCreateDeviceIndex=index;remoteCreateName.value='';remoteCreateAPIUrl.value='';remoteCreateAPIKey.value='';remoteCreateAuthMode.value='api_key';remoteCreateTransformer.value='openai';remoteCreateModel.value='gpt-5';remoteCreateThinking.value='';remoteCreateMaxConcurrentRequests.value='0';remoteCreateCodexFastMode.checked=false;remoteSyncCreateAuthMode();remoteCreateEndpointDialog.showModal()}
    async function submitRemoteCreateEndpoint(event){
      event.preventDefault();
      const name=remoteCreateName.value.trim();
      const apiUrl=remoteCreateAPIUrl.value.trim();
      const apiKey=remoteCreateAPIKey.value.trim();
      const authMode=remoteCreateAuthMode.value;
      const transformer=remoteCreateTransformer.value;
      const model=remoteCreateModel.value.trim();
      const thinking=remoteCreateThinking.value;
      const maxConcurrentRequests=Number(remoteCreateMaxConcurrentRequests.value||0);
      if(!name||!apiUrl){setError(new Error('端点名称和 Base URL 不能为空'));return}
      if(authMode==='api_key'&&!apiKey){setError(new Error('API Key 模式必须填写 API Key'));return}
      if(!Number.isInteger(maxConcurrentRequests)||maxConcurrentRequests<0){setError(new Error('限制并发必须是 0 或正整数'));return}
      try{
        await queueRemote(remoteCreateDeviceIndex,'endpoint.create',{name,apiUrl,apiKey,authMode,transformer,model,thinking,maxConcurrentRequests,enabled:true,codexFastMode:remoteCreateCodexFastMode.checked});
        remoteCreateEndpointDialog.close();
      }catch(err){setError(err)}
    }
    function remoteOpenEndpointEditor(index,endpointIndex){
      const data=window.remoteDetailData&&window.remoteDetailData[index];
      const state=((data||{}).state)||{};
      const endpoints=((state.snapshot||{}).endpoints)||[];
      const ep=endpoints[endpointIndex];
      if(!ep)return;
      const hasThinking=Object.prototype.hasOwnProperty.call(ep,'thinking');
      const supportsThinkingV2=remoteSupportsThinkingV2(state);
      const originalThinking=hasThinking?String(ep.thinking||''):'';
      remoteEditContext={index,endpointName:ep.name,originalModel:String(ep.model||''),originalThinking,thinkingKnown:hasThinking||supportsThinkingV2,supportsNullableUpdates:supportsThinkingV2};
      remoteEditEndpointName.textContent=ep.name;
      remoteEditModel.value=remoteEditContext.originalModel;
      document.getElementById('remoteEditThinkingDefault').disabled=!supportsThinkingV2;
      remoteEditThinking.value=supportsThinkingV2?originalThinking:(hasThinking&&originalThinking?originalThinking:'__keep__');
      remoteEditThinkingHelp.textContent=supportsThinkingV2?'该客户端支持显示推理强度并恢复上游默认。':'旧客户端未上报推理强度；默认保持不变，仍可下发明确强度。';
      remoteEditEndpointDialog.showModal();
    }
    async function submitRemoteEndpointEdit(event){
      event.preventDefault();
      if(!remoteEditContext)return;
      const model=remoteEditModel.value.trim();
      const thinking=remoteEditThinking.value;
      const payload={endpointName:remoteEditContext.endpointName};
      let changed=false;
      if(model===''&&remoteEditContext.originalModel!==''&&!remoteEditContext.supportsNullableUpdates){setError(new Error('旧客户端不支持远程清空模型，请升级客户端或填写新的模型名'));return}
      if(model!==remoteEditContext.originalModel){payload.model=model;changed=true}
      if(thinking!=='__keep__'){
        if(!remoteEditContext.thinkingKnown||thinking!==remoteEditContext.originalThinking){payload.thinking=thinking;changed=true}
      }
      if(!changed){remoteEditEndpointDialog.close();message.textContent='模型和推理配置没有变化';return}
      try{
        await queueRemote(remoteEditContext.index,'endpoint.update',payload);
        remoteEditEndpointDialog.close();
      }catch(err){setError(err)}
    }
    async function remoteDeleteEndpoint(index,name){if(!confirm('删除远程端点 '+name+'？'))return;try{await queueRemote(index,'endpoint.delete',{endpointName:name})}catch(err){setError(err)}}
    async function remoteMoveEndpoint(index,endpointIndex,direction){try{const data=window.remoteDetailData&&window.remoteDetailData[index];const endpoints=(((data||{}).state||{}).snapshot||{}).endpoints||[];const targetIndex=endpointIndex+direction;if(targetIndex<0||targetIndex>=endpoints.length)return;const names=endpoints.map(ep=>ep.name);const moved=names[endpointIndex];names[endpointIndex]=names[targetIndex];names[targetIndex]=moved;await queueRemote(index,'endpoint.reorder',{names})}catch(err){setError(err)}}
    async function remoteCredentialEnabled(index,id,enabled){try{await queueRemote(index,'credential.setEnabled',{credentialId:id,enabled})}catch(err){setError(err)}}
    async function remoteUpdateCredentialToken(index,id){const accessToken=prompt('新的 access token');if(accessToken===null)return;try{await queueRemote(index,'credential.updateToken',{credentialId:id,accessToken})}catch(err){setError(err)}}
    async function remoteDeleteCredential(index,id){if(!confirm('删除该远程 Token Pool 凭证？'))return;try{await queueRemote(index,'credential.delete',{credentialId:id})}catch(err){setError(err)}}
    async function remoteRevealSecret(index,endpointName,credentialId,field){const device=window.deviceRows[index];try{if(!window.isSecureContext||!crypto.subtle){throw new Error('查看明文需要 HTTPS 安全后台；当前 HTTP 页面只允许远程维护，不展示密钥明文。')}const keyPair=await createRevealKeyPair();const command=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote/secrets/reveal',{method:'POST',body:JSON.stringify({endpointName,credentialId,field,adminPublicKey:keyPair.publicKey})});message.textContent='已下发一次性查看命令，等待客户端拉取';const done=await pollRemoteCommand(device.deviceId,command.id,keyPair);await loadRemoteDetail(index);if(done&&done.value){alert('一次性明文（请立即使用，勿转存）：\n'+done.value)}}catch(err){setError(err)}}
    async function pollRemoteCommand(deviceId,commandId,keyPair){const started=Date.now();const deadline=started+90000;while(Date.now()<deadline){const elapsed=Date.now()-started;await sleep(elapsed<10000?500:1500);let command;try{command=await api('/api/admin/devices/'+encodeURIComponent(deviceId)+'/remote/commands/'+commandId)}catch(err){if(String(err.message||err).includes('expired'))throw new Error('远程命令结果已过期，请重试');throw err}if(command.status==='queued'){message.textContent=elapsed>10000?'等待客户端拉取；客户端可能网络较慢或版本较旧':'远程命令等待客户端拉取';continue}if(command.status==='delivered'){message.textContent='客户端已领取命令，正在执行';continue}if(command.status==='expired')throw new Error(command.error||'远程命令已过期，请重试');if(command.status==='failed')throw new Error(command.error||'远程命令执行失败');if(command.status==='applied'){message.textContent='远程命令已应用';const reveal=command.resultJson&&command.resultJson.secretReveal;if(reveal&&keyPair){return await decryptRevealResult(keyPair,reveal)}return command}}throw new Error('客户端未在 90 秒内回传结果，可能离线、版本不支持或网络不可达')}
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
