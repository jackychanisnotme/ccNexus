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
	mux.Handle("/admin/", onlinelicense.AdminMiddleware(admin, http.HandlerFunc(adminPage)))
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

const adminHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ccNexus License Admin</title>
  <style>
    :root{color-scheme:light;--bg:#f4f6f8;--panel:#fff;--line:#d9dee7;--text:#172033;--muted:#657184;--accent:#1769aa;--danger:#b42318}
    *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    header{padding:18px 24px;border-bottom:1px solid var(--line);background:#fff;display:flex;align-items:center;justify-content:space-between;gap:16px}
    h1{margin:0;font-size:20px}main{padding:22px;max-width:1360px;margin:0 auto}.grid{display:grid;grid-template-columns:360px 1fr;gap:18px;align-items:start}
    section{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:16px}h2{font-size:15px;margin:0 0 12px}
    label{display:block;font-size:12px;font-weight:700;color:var(--muted);margin:10px 0 5px}input,select,textarea{width:100%;border:1px solid #c8d0dc;border-radius:6px;padding:9px;background:#fff;color:var(--text)}
    textarea{min-height:72px;resize:vertical}.row{display:grid;grid-template-columns:1fr 1fr;gap:10px}.actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:14px}
    button{border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:6px;padding:8px 12px;font-weight:700;cursor:pointer}button.secondary{background:#fff;color:var(--accent)}button.danger{border-color:var(--danger);background:var(--danger)}
    table{width:100%;border-collapse:collapse;font-size:13px}th,td{border-bottom:1px solid #e7ebf0;padding:8px;text-align:left;vertical-align:top}th{font-size:12px;color:var(--muted);background:#fafbfc;position:sticky;top:0}
    .table-wrap{overflow:auto;max-height:520px;border:1px solid #e7ebf0;border-radius:6px}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}.muted{color:var(--muted)}.status-active{color:#067647;font-weight:700}.status-disabled{color:var(--danger);font-weight:700}
    #generated{white-space:pre-wrap;word-break:break-all;background:#f8fafc;border:1px solid #e7ebf0;border-radius:6px;padding:10px;margin-top:12px;max-height:180px;overflow:auto}.notice{margin-left:auto;color:var(--muted)}
    @media(max-width:900px){.grid{grid-template-columns:1fr}header{align-items:flex-start;flex-direction:column}}
  </style>
</head>
<body>
  <header>
    <h1>ccNexus License Admin</h1>
    <div class="notice">Basic Auth 登录后可生成卡密、查看设备、禁用授权</div>
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
    </section>
    <div>
      <section>
        <h2>卡密</h2>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>状态</th><th>套餐</th><th>天数</th><th>设备</th><th>客户/备注</th><th>创建时间</th><th>操作</th></tr></thead><tbody id="cards"></tbody></table></div>
      </section>
      <section style="margin-top:18px">
        <h2>设备激活</h2>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>卡ID</th><th>设备ID</th><th>状态</th><th>到期</th><th>最近校验</th><th>平台/版本</th><th>操作</th></tr></thead><tbody id="activations"></tbody></table></div>
      </section>
    </div>
  </main>
  <script>
    const apiOrigin = location.origin.replace('//'+location.username+(location.password?':'+location.password:'')+'@', '//');
    async function api(path, options={}) {
      const res = await fetch(apiOrigin + path, {credentials:'same-origin', headers:{'Content-Type':'application/json'}, ...options});
      const data = await res.json();
      if (!res.ok || data.success === false) throw new Error(data.error || '请求失败');
      return data.data;
    }
    function esc(v){return String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
    function dt(v){return v ? new Date(v).toLocaleString() : '-'}
    async function generateCards(){
      const payload = {plan:plan.value,days:Number(days.value||0),count:Number(count.value||1),maxDevices:Number(maxDevices.value||1),customer:customer.value,remark:remark.value};
      const data = await api('/api/admin/cards/generate',{method:'POST',body:JSON.stringify(payload)});
      generated.textContent = data.cards.map(c => c.cardKey).join('\n');
      await refreshAll();
    }
    async function refreshCards(){
      const rows = await api('/api/admin/cards');
      cards.innerHTML = rows.map(c => '<tr><td>'+c.id+'</td><td class="status-'+esc(c.status)+'">'+esc(c.status)+'</td><td>'+esc(c.plan)+'</td><td>'+c.days+'</td><td>'+c.activations+'/'+c.maxDevices+'</td><td>'+esc(c.customer)+'<br><span class="muted">'+esc(c.remark)+'</span></td><td>'+dt(c.createdAt)+'</td><td>'+(c.status==='active'?'<button class="danger" onclick="disableCard('+c.id+')">禁用</button>':'-')+'</td></tr>').join('');
    }
    async function refreshActivations(){
      const rows = await api('/api/admin/activations');
      activations.innerHTML = rows.map(a => '<tr><td>'+a.id+'</td><td>'+a.cardId+'</td><td class="mono">'+esc(a.deviceId)+'</td><td class="status-'+esc(a.status)+'">'+esc(a.status)+'</td><td>'+dt(a.expiresAt)+'</td><td>'+dt(a.lastCheckedAt)+'</td><td>'+esc(a.platform)+'<br><span class="muted">'+esc(a.appVersion)+'</span></td><td>'+(a.status==='active'?'<button class="danger" onclick="disableActivation('+a.id+')">禁用</button>':'-')+'</td></tr>').join('');
    }
    async function disableCard(id){if(confirm('禁用这张卡密？')){await api('/api/admin/cards/'+id+'/disable',{method:'POST'});await refreshAll();}}
    async function disableActivation(id){if(confirm('禁用这个设备激活？')){await api('/api/admin/activations/'+id+'/disable',{method:'POST'});await refreshAll();}}
    async function refreshAll(){await Promise.all([refreshCards(),refreshActivations()]);}
    async function copyGenerated(){await navigator.clipboard.writeText(generated.textContent || '');}
    refreshAll().catch(err => { generated.textContent = err.message; });
  </script>
</body>
</html>`
