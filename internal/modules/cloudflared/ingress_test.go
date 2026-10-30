package cloudflared

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wangwanjie/gately/internal/config"
	"github.com/wangwanjie/gately/internal/logbuf"
	"github.com/wangwanjie/gately/internal/module"
)

func TestParseToken(t *testing.T) {
	raw := `{"a":"acc","t":"5f0c-tid","s":"c2VjcmV0"}`
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		info, err := parseToken(" " + enc.EncodeToString([]byte(raw)) + "\n")
		if err != nil || info.AccountTag != "acc" || info.TunnelID != "5f0c-tid" {
			t.Errorf("解析失败: %+v %v", info, err)
		}
	}
	for _, bad := range []string{"", "eyJ0", base64.StdEncoding.EncodeToString([]byte(`{"a":"acc"}`))} {
		if _, err := parseToken(bad); err == nil {
			t.Errorf("%q 应解析失败", bad)
		}
	}
}

func TestValidateService(t *testing.T) {
	good := []string{"http://localhost:8080", "https://192.168.1.2", "http://nas:5000/", "ssh://localhost:22", "tcp://[::1]:3389",
		"rdp://pc:3389", "smb://nas:445", "unix:/run/app.sock", "http_status:404", "hello_world", "bastion", "wss://a:1"}
	for _, s := range good {
		if err := validateService(s); err != nil {
			t.Errorf("%s 应通过: %v", s, err)
		}
	}
	bad := []string{"localhost:8080", "http://localhost:8080/app", "ssh://localhost", "ftp://a:21", "http_status:99", "unix:", "nothing"}
	for _, s := range bad {
		if err := validateService(s); err == nil {
			t.Errorf("%s 应被拒绝", s)
		}
	}
}

func TestNormalizeRules(t *testing.T) {
	rules := []IngressRule{
		{Hostname: " App.Example.com ", Service: " http://localhost:80 ", OriginRequest: map[string]any{}},
		{Hostname: "*.example.com", Path: "^/api", Service: "http://localhost:81"},
		{Path: "\\.png$", Service: "http_status:404"},
	}
	if err := normalizeRules(rules); err != nil {
		t.Fatal(err)
	}
	if rules[0].Hostname != "app.example.com" || rules[0].Service != "http://localhost:80" || rules[0].OriginRequest != nil {
		t.Fatalf("规范化错误: %+v", rules[0])
	}
	bad := [][]IngressRule{
		{{Service: "http://a:1"}},
		{{Hostname: "bad host", Service: "http://a:1"}},
		{{Hostname: "a.example.com", Path: "(", Service: "http://a:1"}},
		{{Hostname: "a.example.com"}},
		{{Hostname: "a.example.com", Service: "a:1"}},
	}
	for i, rs := range bad {
		if err := normalizeRules(rs); err == nil {
			t.Errorf("用例 %d 应被拒绝: %+v", i, rs)
		}
	}
}

// fakeCF 模拟 Cloudflare API：一个隧道远程配置与 example.com 一个域名的 DNS 记录。
type fakeCF struct {
	t       *testing.T
	mu      sync.Mutex
	version int
	config  map[string]any
	records []cfDNSRecord
	puts    int
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	if r.Header.Get("Authorization") != "Bearer cf-api" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
		return
	}
	switch {
	case r.URL.Path == "/accounts/acc/cfd_tunnel/tid/configurations" && r.Method == http.MethodGet:
		var cfg any
		if f.config != nil {
			cfg = f.config
		}
		reply(map[string]any{"tunnel_id": "tid", "version": f.version, "source": "cloudflare", "config": cfg})
	case r.URL.Path == "/accounts/acc/cfd_tunnel/tid/configurations" && r.Method == http.MethodPut:
		var body struct {
			Config map[string]any `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("请求体错误: %v", err)
		}
		f.config, f.version = body.Config, f.version+1
		f.puts++
		reply(map[string]any{"tunnel_id": "tid", "version": f.version, "source": "cloudflare", "config": f.config})
	case r.URL.Path == "/zones" && r.Method == http.MethodGet:
		if r.URL.Query().Get("name") == "example.com" {
			reply([]map[string]string{{"id": "z1", "name": "example.com"}})
		} else {
			reply([]any{})
		}
	case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
		out := []cfDNSRecord{}
		for _, rec := range f.records {
			if rec.Name == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		reply(out)
	case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
		var rec cfDNSRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		f.records = append(f.records, rec)
		reply(rec)
	default:
		f.t.Errorf("未预期的请求 %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

func newIngressModule(t *testing.T, cf *fakeCF) (*Module, *http.ServeMux) {
	t.Helper()
	srv := httptest.NewServer(cf)
	t.Cleanup(srv.Close)
	old := cfEndpoint
	cfEndpoint = srv.URL
	t.Cleanup(func() { cfEndpoint = old })
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	if err := m.Init(module.Env{Store: store, Logs: logbuf.New(100, log.New(io.Discard, "", 0))}); err != nil {
		t.Fatal(err)
	}
	token := base64.StdEncoding.EncodeToString([]byte(`{"a":"acc","t":"tid","s":"x"}`))
	m.cfg.Tunnels = []Tunnel{
		{Key: "k", Name: "nas", Mode: ModeToken, Token: token, APIToken: "cf-api"},
		{Key: "noapi", Name: "b", Mode: ModeToken, Token: token},
		{Key: "quick", Name: "q", Mode: ModeQuick, Service: "http://localhost:1"},
	}
	mux := http.NewServeMux()
	m.Routes(testRouter{mux})
	return m, mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("响应不是 JSON: %d %s", rec.Code, rec.Body.String())
		}
	}
	return rec.Code
}

// TestIngressReadWrite 验证读取远程路由规则、保存时保留其他字段与兜底规则的回源设置、版本冲突与 DNS 同步。
func TestIngressReadWrite(t *testing.T) {
	cf := &fakeCF{t: t, version: 3, config: map[string]any{
		"ingress": []any{
			map[string]any{"hostname": "old.example.com", "service": "http://localhost:80", "originRequest": map[string]any{"noTLSVerify": true}},
			map[string]any{"service": "http_status:404", "originRequest": map[string]any{"connectTimeout": 10}},
		},
		"warp-routing": map[string]any{"enabled": true},
	}, records: []cfDNSRecord{
		{Type: "CNAME", Name: "old.example.com", Content: "tid.cfargotunnel.com"},
		{Type: "A", Name: "busy.example.com", Content: "1.2.3.4"},
	}}
	_, mux := newIngressModule(t, cf)

	var v ingressView
	if code := doJSON(t, mux, "GET", "/api/cloudflared/tunnels/k/ingress", nil, &v); code != 200 {
		t.Fatalf("读取失败: %d", code)
	}
	if v.TunnelID != "tid" || v.Version != 3 || v.Source != "cloudflare" || len(v.Rules) != 1 || v.CatchAll != "http_status:404" {
		t.Fatalf("读取结果错误: %+v", v)
	}
	if v.Rules[0].OriginRequest["noTLSVerify"] != true {
		t.Fatalf("回源设置丢失: %+v", v.Rules[0])
	}

	rules := append(v.Rules,
		IngressRule{Hostname: "New.Example.com", Service: "ssh://localhost:22"},
		IngressRule{Hostname: "busy.example.com", Service: "http://localhost:81"},
		IngressRule{Hostname: "x.other.net", Service: "http://localhost:82"},
		IngressRule{Hostname: "new.example.com", Path: "^/a", Service: "http://localhost:83"},
	)
	upd := ingressUpdate{Version: 3, Rules: rules, CatchAll: "http_status:503", SyncDNS: true}
	var saved ingressView
	if code := doJSON(t, mux, "PUT", "/api/cloudflared/tunnels/k/ingress", upd, &saved); code != 200 {
		t.Fatalf("保存失败: %d %+v", code, saved)
	}
	if saved.Version != 4 || len(saved.Rules) != 5 || saved.CatchAll != "http_status:503" || saved.Rules[1].Hostname != "new.example.com" {
		t.Fatalf("保存结果错误: %+v", saved)
	}
	if cf.config["warp-routing"] == nil {
		t.Fatal("ingress 以外的字段应保留")
	}
	ing := cf.config["ingress"].([]any)
	last := ing[len(ing)-1].(map[string]any)
	if last["service"] != "http_status:503" || last["originRequest"].(map[string]any)["connectTimeout"] != float64(10) || last["hostname"] != nil {
		t.Fatalf("兜底规则错误: %+v", last)
	}
	want := map[string]string{"old.example.com": dnsUnchanged, "new.example.com": dnsCreated, "busy.example.com": dnsConflict, "x.other.net": dnsFailed}
	if len(saved.DNS) != len(want) {
		t.Fatalf("同一主机名只应同步一次: %+v", saved.DNS)
	}
	for _, d := range saved.DNS {
		if want[d.Hostname] != d.Status {
			t.Errorf("%s 的同步结果为 %s，期望 %s（%+v）", d.Hostname, d.Status, want[d.Hostname], d)
		}
		if d.Hostname == "busy.example.com" && d.Existing != "A 1.2.3.4" {
			t.Errorf("冲突应返回已有记录: %+v", d)
		}
	}
	created := cf.records[len(cf.records)-1]
	if created.Name != "new.example.com" || created.Type != "CNAME" || created.Content != "tid.cfargotunnel.com" || !created.Proxied {
		t.Fatalf("创建的记录错误: %+v", created)
	}
	if len(cf.records) != 3 {
		t.Fatalf("冲突的记录不应被修改: %+v", cf.records)
	}

	// 版本已被其他地方修改
	var e map[string]string
	if code := doJSON(t, mux, "PUT", "/api/cloudflared/tunnels/k/ingress", ingressUpdate{Version: 3, Rules: rules}, &e); code != http.StatusConflict {
		t.Fatalf("版本过期应返回 409: %d %v", code, e)
	}
	// 规则无效时不访问 Cloudflare
	puts := cf.puts
	if code := doJSON(t, mux, "PUT", "/api/cloudflared/tunnels/k/ingress", ingressUpdate{Version: 4, Rules: []IngressRule{{Service: "http://a:1"}}}, &e); code != 400 || cf.puts != puts {
		t.Fatalf("无效规则应返回 400: %d %v", code, e)
	}
	// 不同步 DNS、兜底为空时使用默认值
	var cleared ingressView
	if code := doJSON(t, mux, "PUT", "/api/cloudflared/tunnels/k/ingress", ingressUpdate{Version: 4, Rules: []IngressRule{}}, &cleared); code != 200 || cleared.DNS != nil || cleared.CatchAll != defaultCatchAll || len(cleared.Rules) != 0 {
		t.Fatalf("清空规则失败: %d %+v", code, cleared)
	}
}

// TestIngressFreshTunnel 验证新建隧道（远程配置为空）时返回空规则与默认兜底。
func TestIngressFreshTunnel(t *testing.T) {
	cf := &fakeCF{t: t}
	_, mux := newIngressModule(t, cf)
	var v ingressView
	if code := doJSON(t, mux, "GET", "/api/cloudflared/tunnels/k/ingress", nil, &v); code != 200 || v.Rules == nil || len(v.Rules) != 0 || v.CatchAll != defaultCatchAll {
		t.Fatalf("空配置读取错误: %d %+v", code, v)
	}
}

func TestIngressErrors(t *testing.T) {
	cf := &fakeCF{t: t}
	m, mux := newIngressModule(t, cf)
	cases := map[string]int{"missing": 404, "noapi": 400, "quick": 400}
	for key, want := range cases {
		var e map[string]string
		if code := doJSON(t, mux, "GET", "/api/cloudflared/tunnels/"+key+"/ingress", nil, &e); code != want {
			t.Errorf("%s 应返回 %d，实际 %d %v", key, want, code, e)
		}
	}
	// API 令牌错误时返回 Cloudflare 的错误信息
	m.cfg.Tunnels[0].APIToken = "wrong"
	var e map[string]string
	if code := doJSON(t, mux, "GET", "/api/cloudflared/tunnels/k/ingress", nil, &e); code != http.StatusBadGateway || !strings.Contains(e["error"], "10000 Authentication error") {
		t.Fatalf("应返回认证错误: %d %v", code, e)
	}
}

// TestAPITokenSecret 验证 API 令牌在列表中脱敏、编辑时保留原值、临时隧道不保存 API 令牌。
func TestAPITokenSecret(t *testing.T) {
	cf := &fakeCF{t: t}
	m, mux := newIngressModule(t, cf)
	var list []map[string]any
	doJSON(t, mux, "GET", "/api/cloudflared/tunnels", nil, &list)
	if list[0]["apiToken"] == "cf-api" || list[0]["tunnelId"] != "tid" {
		t.Fatalf("API 令牌应脱敏并返回隧道 ID: %v", list[0])
	}
	body := map[string]any{"name": "nas", "enable": false, "mode": "token", "token": list[0]["token"], "apiToken": list[0]["apiToken"], "service": "", "protocol": "auto", "edgeIpVersion": "auto", "remark": ""}
	var out map[string]any
	if code := doJSON(t, mux, "PUT", "/api/cloudflared/tunnels/k", body, &out); code != 200 {
		t.Fatalf("保存失败: %d %v", code, out)
	}
	if m.cfg.Tunnels[0].APIToken != "cf-api" {
		t.Fatalf("占位符应保留原 API 令牌: %q", m.cfg.Tunnels[0].APIToken)
	}
	var sec map[string]string
	doJSON(t, mux, "GET", "/api/cloudflared/tunnels/k/secrets", nil, &sec)
	if sec["apiToken"] != "cf-api" {
		t.Fatalf("应能查看 API 令牌明文: %v", sec)
	}
	q := Tunnel{Name: "q", Mode: ModeQuick, Service: "http://localhost:1", APIToken: "x"}
	if err := q.Validate(); err != nil || q.APIToken != "" {
		t.Fatalf("临时隧道应清空 API 令牌: %v %+v", err, q)
	}
}
