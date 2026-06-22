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
	log.Printf("ccNexus license server listening on %s", addr)
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
  <title>ccNexus License Login</title>
  <style>
    :root{color-scheme:light;--bg:#eef2f6;--panel:#fff;--line:#d9dee7;--text:#172033;--muted:#657184;--accent:#1769aa;--danger:#b42318}
    *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    main{min-height:100dvh;display:grid;place-items:center;padding:22px}.login{width:min(420px,100%);background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:22px}
    h1{margin:0 0 18px;font-size:20px}label{display:block;font-size:12px;font-weight:700;color:var(--muted);margin:12px 0 5px}
    input{width:100%;border:1px solid #c8d0dc;border-radius:6px;padding:10px;background:#fff;color:var(--text)}input:focus{outline:2px solid rgba(23,105,170,.22);border-color:var(--accent)}
    button{width:100%;margin-top:16px;border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:6px;padding:10px 12px;font-weight:700;cursor:pointer}
    .error{min-height:20px;margin-top:12px;color:var(--danger)}
  </style>
</head>
<body>
  <main>
    <form class="login" id="login-form">
      <h1>ccNexus License Admin</h1>
      <label for="username">账号</label>
      <input id="username" name="username" autocomplete="username" value="admin">
      <label for="password">密码</label>
      <input id="password" name="password" type="password" autocomplete="current-password" autofocus>
      <button type="submit">登录</button>
      <div id="error" class="error"></div>
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
  <title>ccNexus License Admin</title>
  <style>
    :root{color-scheme:light;--bg:#f4f6f8;--panel:#fff;--line:#d9dee7;--text:#172033;--muted:#657184;--accent:#1769aa;--danger:#b42318;--ok:#067647}
    *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    header{padding:16px 22px;border-bottom:1px solid var(--line);background:#fff;display:flex;align-items:center;justify-content:space-between;gap:16px;position:sticky;top:0;z-index:2}
    h1{margin:0;font-size:20px}main{padding:18px 22px 28px;max-width:1460px;margin:0 auto}.grid{display:grid;grid-template-columns:360px minmax(0,1fr);gap:18px;align-items:start}
    section{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:16px}h2{font-size:15px;margin:0 0 12px}.stack{display:grid;gap:18px}
    label{display:block;font-size:12px;font-weight:700;color:var(--muted);margin:10px 0 5px}input,select,textarea{width:100%;border:1px solid #c8d0dc;border-radius:6px;padding:9px;background:#fff;color:var(--text)}
    input:focus,select:focus,textarea:focus{outline:2px solid rgba(23,105,170,.22);border-color:var(--accent)}textarea{min-height:72px;resize:vertical}.row{display:grid;grid-template-columns:1fr 1fr;gap:10px}
    .actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:14px}.toolbar{display:flex;align-items:center;gap:8px}.top-note{color:var(--muted)}
    button{border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:6px;padding:8px 12px;font-weight:700;cursor:pointer;white-space:nowrap}button:hover{filter:brightness(.96)}button:active{transform:translateY(1px)}
    button.secondary{background:#fff;color:var(--accent)}button.danger{border-color:var(--danger);background:var(--danger);color:#fff}.small-btn{padding:6px 9px;font-size:12px}
    table{width:100%;border-collapse:collapse;font-size:13px}th,td{border-bottom:1px solid #e7ebf0;padding:8px;text-align:left;vertical-align:top}th{font-size:12px;color:var(--muted);background:#fafbfc;position:sticky;top:0}
    .table-wrap{overflow:auto;max-height:430px;border:1px solid #e7ebf0;border-radius:6px}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}.muted{color:var(--muted)}.status-active{color:var(--ok);font-weight:700}.status-disabled{color:var(--danger);font-weight:700}
    #generated{white-space:pre-wrap;word-break:break-all;background:#f8fafc;border:1px solid #e7ebf0;border-radius:6px;padding:10px;margin-top:12px;max-height:180px;overflow:auto}.message{min-height:20px;margin-top:10px;color:var(--danger)}.empty{text-align:center;color:var(--muted);padding:20px!important}
    @media(max-width:980px){.grid{grid-template-columns:1fr}header{align-items:flex-start;flex-direction:column}.toolbar{width:100%;justify-content:space-between}.row{grid-template-columns:1fr}}
  </style>
</head>
<body>
  <header>
    <div>
      <h1>ccNexus License Admin</h1>
      <div class="top-note">卡密、设备激活和历史记录</div>
    </div>
    <div class="toolbar">
      <button class="secondary" onclick="refreshAll()">刷新</button>
      <button class="danger" onclick="logout()">退出账号</button>
    </div>
  </header>
  <main class="grid">
    <section>
      <h2>生成卡密</h2>
      <label>套餐</label>
      <select id="plan"><option value="monthly">月卡 30天</option><option value="quarterly">季卡 90天</option><option value="half_year">半年 180天</option><option value="yearly">年卡 365天</option><option value="custom">自定义</option></select>
      <div class="row"><div><label>自定义天数</label><input id="days" type="number" min="1" value="30"></div><div><label>生成数量</label><input id="count" type="number" min="1" value="1"></div></div>
      <div class="row"><div><label>允许设备数</label><input id="maxDevices" type="number" min="1" value="1"></div><div><label>客户</label><input id="customer" placeholder="客户名"></div></div>
      <label>备注</label><textarea id="remark" placeholder="订单、渠道、说明"></textarea>
      <div class="actions"><button onclick="generateCards()">生成</button><button class="secondary" onclick="refreshAll()">刷新</button><button class="secondary" onclick="copyGenerated()">复制结果</button></div>
      <div id="generated" class="muted">尚未生成卡密</div>
      <div id="message" class="message"></div>
    </section>
    <div class="stack">
      <section>
        <h2>卡密</h2>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>状态</th><th>套餐</th><th>天数</th><th>设备</th><th>客户/备注</th><th>创建时间</th><th>操作</th></tr></thead><tbody id="cards"><tr><td colspan="8" class="empty">加载中</td></tr></tbody></table></div>
      </section>
      <section>
        <h2>设备激活</h2>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>卡ID</th><th>设备ID</th><th>状态</th><th>到期</th><th>最近校验</th><th>平台/版本</th><th>IP</th><th>操作</th></tr></thead><tbody id="activations"><tr><td colspan="9" class="empty">加载中</td></tr></tbody></table></div>
      </section>
      <section>
        <h2>历史记录</h2>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>动作</th><th>对象</th><th>详情</th><th>时间</th></tr></thead><tbody id="history"><tr><td colspan="5" class="empty">加载中</td></tr></tbody></table></div>
      </section>
    </div>
  </main>
  <script>
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
    function statusCell(value){return '<span class="status-'+esc(value)+'">'+esc(value)+'</span>'}
    function actionName(value){return ({admin_login:'登录',admin_logout:'退出',generate_card:'生成卡密',activate:'激活',refresh:'刷新',disable_card:'禁用卡密',delete_card:'删除卡密',disable_activation:'禁用设备'}[value] || value)}
    async function generateCards(){
      setError('');
      try {
        const payload = {plan:plan.value,days:Number(days.value||0),count:Number(count.value||1),maxDevices:Number(maxDevices.value||1),customer:customer.value,remark:remark.value};
        const data = await api('/api/admin/cards/generate',{method:'POST',body:JSON.stringify(payload)});
        generated.textContent = data.cards.map(c => c.cardKey).join('\n');
        await refreshAll();
      } catch (err) { setError(err); }
    }
    async function refreshCards(){
      const rows = await api('/api/admin/cards');
      cards.innerHTML = rows.length ? rows.map(c => '<tr><td>'+c.id+'</td><td>'+statusCell(c.status)+'</td><td>'+esc(c.plan)+'</td><td>'+c.days+'</td><td>'+c.activations+'/'+c.maxDevices+'</td><td>'+esc(c.customer)+'<br><span class="muted">'+esc(c.remark)+'</span></td><td>'+dt(c.createdAt)+'</td><td><div class="actions">'+(c.status==='active'?'<button class="danger small-btn" onclick="disableCard('+c.id+')">禁用</button>':'')+'<button class="danger small-btn" onclick="deleteCard('+c.id+')">删除</button></div></td></tr>').join('') : '<tr><td colspan="8" class="empty">暂无卡密</td></tr>';
    }
    async function refreshActivations(){
      const rows = await api('/api/admin/activations');
      activations.innerHTML = rows.length ? rows.map(a => '<tr><td>'+a.id+'</td><td>'+a.cardId+'</td><td class="mono">'+esc(a.deviceId)+'</td><td>'+statusCell(a.status)+'</td><td>'+dt(a.expiresAt)+'</td><td>'+dt(a.lastCheckedAt)+'</td><td>'+esc(a.platform)+'<br><span class="muted">'+esc(a.appVersion)+'</span></td><td class="mono">'+esc(a.ipAddress)+'</td><td>'+(a.status==='active'?'<button class="danger small-btn" onclick="disableActivation('+a.id+')">禁用</button>':'-')+'</td></tr>').join('') : '<tr><td colspan="9" class="empty">暂无激活设备</td></tr>';
    }
    async function refreshHistory(){
      const rows = await api('/api/admin/history');
      history.innerHTML = rows.length ? rows.map(h => '<tr><td>'+h.id+'</td><td>'+esc(actionName(h.action))+'</td><td>'+esc(h.targetType)+' #'+h.targetId+'</td><td class="mono">'+esc(h.detail)+'</td><td>'+dt(h.createdAt)+'</td></tr>').join('') : '<tr><td colspan="5" class="empty">暂无历史记录</td></tr>';
    }
    async function disableCard(id){if(confirm('禁用这张卡密？')){try{await api('/api/admin/cards/'+id+'/disable',{method:'POST'});await refreshAll();}catch(err){setError(err);}}}
    async function deleteCard(id){if(confirm('删除这张卡密及其设备激活记录？')){try{await api('/api/admin/cards/'+id,{method:'DELETE'});await refreshAll();}catch(err){setError(err);}}}
    async function disableActivation(id){if(confirm('禁用这个设备激活？')){try{await api('/api/admin/activations/'+id+'/disable',{method:'POST'});await refreshAll();}catch(err){setError(err);}}}
    async function refreshAll(){setError('');try{await Promise.all([refreshCards(),refreshActivations(),refreshHistory()]);}catch(err){setError(err);}}
    async function copyGenerated(){try{await navigator.clipboard.writeText(generated.textContent || '');}catch(err){setError(err);}}
    async function logout(){try{await api('/api/admin/logout',{method:'POST'});}finally{location.replace('/admin/login');}}
    refreshAll();
  </script>
</body>
</html>`
