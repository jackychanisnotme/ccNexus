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
      <h1>AINexus License Admin</h1>
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
  <title>AINexus License Admin</title>
  <style>
    :root{color-scheme:light;--bg:#f4f6f8;--panel:#fff;--line:#d9dee7;--text:#172033;--muted:#657184;--accent:#1769aa;--danger:#b42318;--ok:#067647}
    *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    header{padding:16px 22px;border-bottom:1px solid var(--line);background:#fff;display:flex;align-items:center;justify-content:space-between;gap:16px;position:sticky;top:0;z-index:2}
    h1{margin:0;font-size:20px}main{padding:18px 22px 28px;max-width:1460px;margin:0 auto}
    section{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:16px}h2{font-size:15px;margin:0 0 12px}.stack{display:grid;gap:18px}
    label{display:block;font-size:12px;font-weight:700;color:var(--muted);margin:10px 0 5px}input,select,textarea{width:100%;border:1px solid #c8d0dc;border-radius:6px;padding:9px;background:#fff;color:var(--text)}
    input:focus,select:focus,textarea:focus{outline:2px solid rgba(23,105,170,.22);border-color:var(--accent)}textarea{min-height:72px;resize:vertical}.row{display:grid;grid-template-columns:1fr 1fr;gap:10px}
    .actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:14px}.toolbar{display:flex;align-items:center;gap:8px}.top-note{color:var(--muted)}
    .page-tabs{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:14px}.page-tabs button{background:#fff;color:var(--accent)}.page-tabs button.active{background:var(--accent);color:#fff}.admin-page[hidden]{display:none}
    button{border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:6px;padding:8px 12px;font-weight:700;cursor:pointer;white-space:nowrap}button:hover{filter:brightness(.96)}button:active{transform:translateY(1px)}
    button.secondary{background:#fff;color:var(--accent)}button.danger{border-color:var(--danger);background:var(--danger);color:#fff}.small-btn{padding:6px 9px;font-size:12px}
    table{width:100%;border-collapse:collapse;font-size:13px}th,td{border-bottom:1px solid #e7ebf0;padding:8px;text-align:left;vertical-align:top}th{font-size:12px;color:var(--muted);background:#fafbfc;position:sticky;top:0}
    .table-wrap{overflow:auto;max-height:480px;border:1px solid #e7ebf0;border-radius:6px}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}.muted{color:var(--muted)}.status-active{color:var(--ok);font-weight:700}.status-disabled,.status-expired{color:var(--danger);font-weight:700}
    .section-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.section-head h2{margin:0}.inline-check{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted);white-space:nowrap}.inline-check input{width:auto;margin:0}.permission-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:6px;margin-top:8px}.permission-grid label{margin:0;font-weight:500;color:var(--text)}
    .device-detail td{padding:0;background:#f8fafc}.device-detail[hidden]{display:none}.detail-inner{padding:12px 16px}.detail-inner table{background:#fff}.detail-inner th{position:static}.detail-label{font-size:12px;font-weight:700;color:var(--muted);margin-bottom:8px}
    dialog{width:min(460px,calc(100% - 32px));border:1px solid var(--line);border-radius:8px;padding:0;color:var(--text);box-shadow:0 18px 55px rgba(23,32,51,.18)}dialog::backdrop{background:rgba(23,32,51,.38)}.dialog-body{padding:18px}.dialog-body h2{font-size:16px}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:16px}
    #generated{white-space:pre-wrap;word-break:break-all;background:#f8fafc;border:1px solid #e7ebf0;border-radius:6px;padding:10px;margin-top:12px;max-height:180px;overflow:auto}.message{min-height:20px;margin-top:10px;color:var(--danger)}.empty{text-align:center;color:var(--muted);padding:20px!important}
    @media(max-width:980px){header{align-items:flex-start;flex-direction:column}.toolbar{width:100%;justify-content:space-between}.row{grid-template-columns:1fr}}
  </style>
</head>
<body>
  <header>
    <div>
      <h1>AINexus License Admin</h1>
      <div class="top-note">卡密、设备激活、后台账号和历史记录</div>
    </div>
    <div class="toolbar">
      <button class="secondary" onclick="refreshAll()">刷新</button>
      <button class="danger" onclick="logout()">退出账号</button>
    </div>
  </header>
  <main>
    <nav class="page-tabs" aria-label="后台模块">
      <button type="button" data-page-target="generate" onclick="showPage('generate')">生成卡密</button>
      <button type="button" data-page-target="cards" onclick="showPage('cards')">卡密</button>
      <button type="button" data-page-target="accounts" onclick="showPage('accounts')">后台账号</button>
      <button type="button" data-page-target="devices" onclick="showPage('devices')">设备授权</button>
      <button type="button" data-page-target="history" onclick="showPage('history')">历史记录</button>
    </nav>
    <section class="admin-page" data-page="generate">
      <h2>生成卡密</h2>
      <label>套餐</label>
      <select id="plan"><option value="monthly">月卡 30天</option><option value="quarterly">季卡 90天</option><option value="half_year">半年 180天</option><option value="yearly">年卡 365天</option><option value="custom">自定义</option></select>
      <div class="row"><div><label>自定义天数</label><input id="days" type="number" min="1" value="30"></div><div><label>生成数量</label><input id="count" type="number" min="1" value="1"></div></div>
      <div class="row"><div><label>允许设备数</label><input id="maxDevices" type="number" min="1" value="1"></div><div><label>客户</label><input id="customer" placeholder="客户名"></div></div>
      <label>归属账号</label><select id="ownerAccount"></select>
      <label>备注</label><textarea id="remark" placeholder="订单、渠道、说明"></textarea>
      <div class="actions"><button onclick="generateCards()">生成</button><button class="secondary" onclick="refreshAll()">刷新</button><button class="secondary" onclick="copyGenerated()">复制结果</button></div>
      <div id="generated" class="muted">尚未生成卡密</div>
      <div id="message" class="message"></div>
    </section>
    <div class="stack">
      <section class="admin-page" data-page="cards">
        <h2>卡密</h2>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>归属</th><th>状态</th><th>套餐</th><th>天数</th><th>设备</th><th>客户/备注</th><th>创建时间</th><th>操作</th></tr></thead><tbody id="cards"><tr><td colspan="9" class="empty">加载中</td></tr></tbody></table></div>
      </section>
      <section class="admin-page" data-page="accounts">
        <div class="section-head"><h2>后台账号</h2><button class="secondary small-btn" onclick="refreshAccounts()">刷新账号</button></div>
        <div class="row"><div><label>用户名</label><input id="accountUsername" autocomplete="off"></div><div><label>密码</label><input id="accountPassword" type="password" autocomplete="new-password"></div></div>
        <div class="row"><div><label>显示名</label><input id="accountDisplayName"></div><div><label id="accountLevelLabel">级别</label><select id="accountLevel"><option value="2">二级分销</option><option value="3">三级分销</option><option value="1">一级管理员</option></select></div></div>
        <label>父级账号</label><select id="accountParent"></select>
        <div class="permission-grid" id="accountPermissions"></div>
        <div class="actions"><button onclick="createAccount()">创建账号</button></div>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>账号</th><th id="accountLevelHeader">级别</th><th id="accountParentHeader">父级</th><th>状态</th><th>权限</th><th>操作</th></tr></thead><tbody id="accounts"><tr><td colspan="7" class="empty">加载中</td></tr></tbody></table></div>
      </section>
      <section class="admin-page" data-page="devices">
        <h2>设备授权</h2>
        <div class="table-wrap"><table><thead><tr><th>归属</th><th>设备ID</th><th>备注</th><th>状态</th><th>当前到期</th><th>最近校验</th><th>平台/版本</th><th>IP</th><th>兑换次数</th><th>操作</th></tr></thead><tbody id="devices"><tr><td colspan="10" class="empty">加载中</td></tr></tbody></table></div>
      </section>
      <section class="admin-page" data-page="history">
        <div class="section-head"><h2>历史记录</h2><label class="inline-check"><input id="showRefresh" type="checkbox" onchange="renderHistory()">显示自动刷新</label></div>
        <div class="table-wrap"><table><thead><tr><th>ID</th><th>动作</th><th>对象</th><th>详情</th><th>时间</th></tr></thead><tbody id="history"><tr><td colspan="5" class="empty">加载中</td></tr></tbody></table></div>
      </section>
    </div>
  </main>
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
  <script>
    let historyRows = [];
    let accountRows = [];
    let currentAccount = null;
    let editingDeviceId = '';
    const permissionCatalog = [
      ['cards:view','看卡密'],['cards:generate','生成卡密'],['cards:disable','禁用卡密'],['cards:delete','删除卡密'],
      ['devices:view','看设备'],['devices:remark','备注设备'],['devices:expiry','改到期'],['activations:disable','禁用授权'],
      ['devices:remote:view','远程查看'],['devices:remote:write','远程维护'],['devices:remote:secrets','查看密钥'],
      ['accounts:view','看账号'],['accounts:manage','管账号'],['history:view','看历史']
    ];
    const historyBody = document.getElementById('history');
    const showRefreshInput = document.getElementById('showRefresh');
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
    function actionName(value){return ({admin_login:'登录',admin_logout:'退出',generate_card:'生成卡密',activate:'兑换卡密',refresh:'自动校验',disable_card:'禁用卡密',delete_card:'删除卡密',disable_activation:'禁用授权明细',set_device_expiry:'修改设备到期',set_device_remark:'修改设备备注',create_admin_account:'创建账号',update_admin_account:'修改账号'}[value] || value)}
    function planName(value){return ({monthly:'月卡',quarterly:'季卡',half_year:'半年卡',yearly:'年卡',custom:'自定义'}[value] || value)}
    function isRootAccount(){return currentAccount && Number(currentAccount.level) === 1}
    function levelName(value){return ({1:'一级',2:'二级',3:'三级'}[Number(value)] || value)}
    function relationName(account){if(isRootAccount())return levelName(account.level);return ({self:'当前账号',downline:'下级账号'}[account.relationship] || '范围内账号')}
    function parentName(account){if(isRootAccount())return account.parentId || '-';return account.relationship === 'downline' ? '当前账号' : '-'}
    function toLocalInput(value){const date=new Date(value);date.setMinutes(date.getMinutes()-date.getTimezoneOffset());return date.toISOString().slice(0,19)}
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
      cards.innerHTML = rows.length ? rows.map(c => '<tr><td>'+c.id+'</td><td>'+esc(c.ownerUsername||c.ownerAccountId||'-')+'</td><td>'+statusCell(c.status)+'</td><td>'+esc(c.plan)+'</td><td>'+c.days+'</td><td>'+c.activations+'/'+c.maxDevices+'</td><td>'+esc(c.customer)+'<br><span class="muted">'+esc(c.remark)+'</span></td><td>'+dt(c.createdAt)+'</td><td><div class="actions">'+(c.status==='active'&&can('cards:disable')?'<button class="danger small-btn" onclick="disableCard('+c.id+')">禁用</button>':'')+(can('cards:delete')?'<button class="danger small-btn" onclick="deleteCard('+c.id+')">删除</button>':'')+'</div></td></tr>').join('') : '<tr><td colspan="9" class="empty">暂无卡密</td></tr>';
    }
    function licenseRows(device){
      return device.licenses.map(a => '<tr><td>'+a.cardId+'</td><td>'+statusCell(a.status)+'</td><td>'+esc(planName(a.plan))+' / '+a.days+'天</td><td>'+dt(a.activatedAt)+'</td><td>'+dt(a.expiresAt)+'</td><td>'+esc(a.customer)+'<br><span class="muted">'+esc(a.remark)+'</span></td><td>'+(a.status==='active'&&can('activations:disable')?'<button class="danger small-btn" onclick="disableActivation('+a.id+')">禁用此明细</button>':'-')+'</td></tr>').join('');
    }
    async function refreshDevices(){
      const rows = await api('/api/admin/devices');
      devices.innerHTML = rows.length ? rows.map((d,index) => '<tr><td>'+ownerLabel(d)+'</td><td class="mono">'+esc(d.deviceId)+'</td><td>'+esc(d.remark||'-')+'</td><td>'+statusCell(d.status)+'</td><td>'+dt(d.expiresAt)+'</td><td>'+dt(d.lastCheckedAt)+'</td><td>'+esc(d.platform)+'<br><span class="muted">'+esc(d.appVersion)+'</span></td><td class="mono">'+esc(d.ipAddress)+'</td><td>'+d.licenses.length+'</td><td><div class="actions"><button class="secondary small-btn" onclick="toggleDevice('+index+')">明细</button>'+(can('devices:remark')?'<button class="secondary small-btn" onclick="openRemark('+index+')">备注</button>':'')+(can('devices:expiry')?'<button class="small-btn" onclick="openExpiry('+index+')">修改到期</button>':'')+(d.status==='active'&&can('activations:disable')?'<button class="danger small-btn" onclick="disableActivation('+d.currentActivationId+')">禁用当前</button>':'')+'</div></td></tr><tr id="device-detail-'+index+'" class="device-detail" hidden><td colspan="10"><div class="detail-inner"><div id="remote-detail-'+index+'">'+(can('devices:remote:view')?'<div class="detail-label">远程端点维护</div><div class="muted">展开后加载远程状态</div>':'')+'</div><div class="detail-label">卡密兑换与失效明细</div><table><thead><tr><th>卡ID</th><th>状态</th><th>套餐</th><th>兑换时间</th><th>该次累计到期</th><th>客户/备注</th><th>操作</th></tr></thead><tbody>'+licenseRows(d)+'</tbody></table></div></td></tr>').join('') : '<tr><td colspan="10" class="empty">暂无授权设备</td></tr>';
      window.deviceRows = rows;
    }
    async function refreshHistory(){
      historyRows = await api('/api/admin/history');
      renderHistory();
    }
    function renderHistory(){const rows=showRefreshInput.checked?historyRows:historyRows.filter(h=>h.action!=='refresh');historyBody.innerHTML=rows.length?rows.map(h=>'<tr><td>'+h.id+'</td><td>'+esc(actionName(h.action))+'</td><td>'+esc(h.targetType)+' #'+h.targetId+'</td><td class="mono">'+esc(h.detail||'-')+'</td><td>'+dt(h.createdAt)+'</td></tr>').join(''):'<tr><td colspan="5" class="empty">暂无历史记录</td></tr>'}
    async function toggleDevice(index){const row=document.getElementById('device-detail-'+index);row.hidden=!row.hidden;if(!row.hidden&&can('devices:remote:view'))await loadRemoteDetail(index)}
    async function loadRemoteDetail(index){const box=document.getElementById('remote-detail-'+index);const device=window.deviceRows[index];if(!box||!device)return;box.innerHTML='<div class="detail-label">远程端点维护</div><div class="muted">加载中</div>';try{const data=await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote');box.innerHTML=renderRemoteDetail(index,device,data)}catch(err){box.innerHTML='<div class="detail-label">远程端点维护</div><div class="status-disabled">'+esc(err.message||err)+'</div>'}}
    function renderRemoteDetail(index,device,data){const state=(data&&data.state)||{};if(!state.supported)return '<div class="detail-label">远程端点维护</div><div class="muted">该客户端版本暂不支持远程维护，不影响正常授权和本地使用。</div>';const snap=state.snapshot||{};const endpoints=snap.endpoints||[];const pools=snap.tokenPools||[];const remoteStatus='<div class="detail-label">远程端点维护</div><div class="muted">状态：'+(state.enabled?'已启用':'已关闭')+' · 版本：'+esc(state.clientVersion||'-')+' · 心跳：'+dt(state.lastHeartbeatAt)+'</div>'+(can('devices:remote:write')?'<div class="actions"><button class="small-btn" onclick="remoteCreateEndpoint('+index+')">新增端点</button></div>':'');const endpointRows=endpoints.length?endpoints.map(ep=>'<tr><td>'+esc(ep.name)+'</td><td>'+esc(ep.enabled?'启用':'停用')+'</td><td class="mono">'+esc(ep.apiUrl||'-')+'</td><td class="mono">'+esc(ep.apiKeyMasked||'-')+'</td><td>'+esc(ep.authMode||'-')+'</td><td>'+remoteStats(ep.stats)+'</td><td><div class="actions">'+(can('devices:remote:write')?'<button class="secondary small-btn" onclick="remoteUpdateEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,&quot;apiUrl&quot;)">改URL</button><button class="secondary small-btn" onclick="remoteUpdateEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,&quot;apiKey&quot;)">改Key</button><button class="secondary small-btn" onclick="remoteToggleEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;,'+(!ep.enabled)+')">'+(ep.enabled?'停用':'启用')+'</button><button class="danger small-btn" onclick="remoteDeleteEndpoint('+index+',&quot;'+escAttr(ep.name)+'&quot;)">删除</button>':'')+(can('devices:remote:secrets')?'<button class="secondary small-btn" onclick="remoteRevealSecret('+index+',&quot;'+escAttr(ep.name)+'&quot;,0,&quot;apiKey&quot;)">查看Key</button>':'')+'</div></td></tr>').join(''):'<tr><td colspan="7" class="empty">暂无端点快照</td></tr>';const poolRows=pools.length?pools.map(pool=>'<tr><td>'+esc(pool.endpointName)+'</td><td colspan="6">'+renderRemoteCredentials(index,pool)+'</td></tr>').join(''):'<tr><td colspan="7" class="empty">暂无 Token Pool 快照</td></tr>';return remoteStatus+'<table><thead><tr><th>端点</th><th>状态</th><th>Base URL</th><th>API Key</th><th>模式</th><th>用量</th><th>操作</th></tr></thead><tbody>'+endpointRows+'</tbody></table><div class="detail-label" style="margin-top:12px">Codex Token Pool</div><table><thead><tr><th>端点</th><th colspan="6">账号/额度/维护</th></tr></thead><tbody>'+poolRows+'</tbody></table>'}
    function renderRemoteCredentials(index,pool){const creds=pool.credentials||[];if(!creds.length)return '<span class="muted">暂无账号</span>';return '<table><thead><tr><th>ID</th><th>账号</th><th>邮箱</th><th>状态</th><th>用量</th><th>额度</th><th>操作</th></tr></thead><tbody>'+creds.map(c=>'<tr><td>'+c.id+'</td><td class="mono">'+esc(c.accountIdMasked||'-')+'</td><td>'+esc(c.emailMasked||'-')+'</td><td>'+esc(c.status||'-')+(c.enabled?'':' / 停用')+'</td><td>'+remoteStats(c.usage)+'</td><td class="mono">'+esc(remoteQuota(c.quota))+'</td><td><div class="actions">'+(can('devices:remote:write')?'<button class="secondary small-btn" onclick="remoteCredentialEnabled('+index+','+c.id+','+(!c.enabled)+')">'+(c.enabled?'停用':'启用')+'</button><button class="secondary small-btn" onclick="remoteUpdateCredentialToken('+index+','+c.id+')">改Token</button><button class="danger small-btn" onclick="remoteDeleteCredential('+index+','+c.id+')">删除</button>':'')+(can('devices:remote:secrets')?'<button class="secondary small-btn" onclick="remoteRevealSecret('+index+',&quot;'+escAttr(pool.endpointName)+'&quot;,'+c.id+',&quot;accessToken&quot;)">查看Token</button>':'')+'</div></td></tr>').join('')+'</tbody></table>'}
    function remoteStats(stats){stats=stats||{};return '请求 '+(stats.requests||0)+' / Token '+((stats.inputTokens||0)+(stats.outputTokens||0))+' / 错误 '+(stats.errors||0)}
    function remoteQuota(q){if(!q)return '-';try{return JSON.stringify(q.data||q).slice(0,160)}catch(e){return '-'}}
    function escAttr(v){return String(v||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
    async function queueRemote(index,commandType,payload){const device=window.deviceRows[index];await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote/commands',{method:'POST',body:JSON.stringify({commandType,payload})});await loadRemoteDetail(index)}
    async function remoteUpdateEndpoint(index,name,field){const value=prompt(field==='apiUrl'?'新的 Base URL':'新的 API Key');if(value===null)return;const payload={endpointName:name};payload[field]=value;try{await queueRemote(index,'endpoint.update',payload)}catch(err){setError(err)}}
    async function remoteToggleEndpoint(index,name,enabled){try{await queueRemote(index,'endpoint.update',{endpointName:name,enabled})}catch(err){setError(err)}}
    async function remoteCreateEndpoint(index){const name=prompt('端点名称');if(name===null||!name.trim())return;const apiUrl=prompt('Base URL');if(apiUrl===null||!apiUrl.trim())return;const apiKey=prompt('API Key（Token Pool 模式可留空）')||'';const authMode=prompt('认证模式：api_key / token_pool / codex_token_pool','api_key')||'api_key';const transformer=prompt('转换器：openai / openai2 / gemini / Codex','openai')||'openai';const model=prompt('模型名','gpt-5')||'';try{await queueRemote(index,'endpoint.create',{name,apiUrl,apiKey,authMode,transformer,model,enabled:true})}catch(err){setError(err)}}
    async function remoteDeleteEndpoint(index,name){if(!confirm('删除远程端点 '+name+'？'))return;try{await queueRemote(index,'endpoint.delete',{endpointName:name})}catch(err){setError(err)}}
    async function remoteCredentialEnabled(index,id,enabled){try{await queueRemote(index,'credential.setEnabled',{credentialId:id,enabled})}catch(err){setError(err)}}
    async function remoteUpdateCredentialToken(index,id){const accessToken=prompt('新的 access token');if(accessToken===null)return;try{await queueRemote(index,'credential.updateToken',{credentialId:id,accessToken})}catch(err){setError(err)}}
    async function remoteDeleteCredential(index,id){if(!confirm('删除该远程 Token Pool 凭证？'))return;try{await queueRemote(index,'credential.delete',{credentialId:id})}catch(err){setError(err)}}
    async function remoteRevealSecret(index,endpointName,credentialId,field){const device=window.deviceRows[index];try{await api('/api/admin/devices/'+encodeURIComponent(device.deviceId)+'/remote/secrets/reveal',{method:'POST',body:JSON.stringify({endpointName,credentialId,field})});alert('已下发一次性查看命令，等待客户端回传。')}catch(err){setError(err)}}
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
