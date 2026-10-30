package webservice

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wangwanjie/gately/internal/webprobe"
)

// TestWebProbeReport 验证请求结束后把监听、站点、是否未知域名与状态码交给入侵防护：
// 匹配站点的 404、默认站点断开连接的 444、没有默认站点时监听层的 404 都要上报。
func TestWebProbeReport(t *testing.T) {
	var mu sync.Mutex
	var hits []webprobe.Hit
	webprobe.SetHandler(func(h webprobe.Hit) {
		mu.Lock()
		hits = append(hits, h)
		mu.Unlock()
	})
	t.Cleanup(func() { webprobe.SetHandler(nil) })
	// wait 等待第 n 条上报：站点的上报在响应写出后的 defer 中执行，客户端收到响应时可能还没上报
	wait := func(n int) webprobe.Hit {
		t.Helper()
		for i := 0; i < 200; i++ {
			mu.Lock()
			if len(hits) >= n {
				h := hits[n-1]
				mu.Unlock()
				return h
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("没有收到第 %d 条上报", n)
		return webprobe.Hit{}
	}

	withDefault := Listener{
		Key:     "l1",
		Sites:   []Site{{Key: "s1", Enable: true, Domains: []string{"app.test"}, Type: TypeText, Text: "nope", StatusCode: 404}},
		Default: Site{Key: "default", Enable: true, Type: TypeClose},
	}
	addr, _ := startRunner(t, withDefault, nil, nil)
	get(t, noRedirect(), addr, "app.test", "/.env", nil)
	if h := wait(1); h.Listener != "l1" || h.Site != "s1" || h.Unknown || h.Status != 404 || h.Path != "/.env" || !h.Addr.IsLoopback() {
		t.Fatalf("站点 404 上报不符: %+v", h)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET /wp-login.php HTTP/1.1\r\nHost: scan.test\r\n\r\n")
	_, _ = io.ReadAll(conn)
	conn.Close()
	if h := wait(2); h.Site != "default" || !h.Unknown || h.Status != statusClosed || h.Host != "scan.test" {
		t.Fatalf("默认站点 444 上报不符: %+v", h)
	}

	noDefault := Listener{Key: "l2", Sites: []Site{{Key: "s2", Enable: true, Domains: []string{"app.test"}, Type: TypeText, Text: "ok"}}}
	addr2, _ := startRunner(t, noDefault, nil, nil)
	get(t, noRedirect(), addr2, "unknown.test", "/phpinfo.php", nil)
	if h := wait(3); h.Listener != "l2" || h.Site != "" || !h.Unknown || h.Status != 404 || h.Path != "/phpinfo.php" {
		t.Fatalf("没有站点时的上报不符: %+v", h)
	}

	// 访问控制拒绝后断开连接的请求按 403 上报，不算作探测
	denied := Listener{Key: "l3", Sites: []Site{{Key: "s3", Enable: true, Domains: []string{"app.test"}, Type: TypeText, Text: "ok",
		IPMode: IPModeWhitelist, IPList: []string{"10.0.0.0/8"}, DenyClose: true}}}
	addr3, _ := startRunner(t, denied, nil, nil)
	req, _ := http.NewRequest("GET", "http://"+addr3+"/.env", nil)
	req.Host = "app.test"
	if resp, err := noRedirect().Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("拒绝访问时应断开连接，得到 %d", resp.StatusCode)
	}
	if h := wait(4); h.Listener != "l3" || h.Status != http.StatusForbidden {
		t.Fatalf("拒绝访问的上报不符: %+v", h)
	}

	// 重写规则改写路径后，上报的仍是访客请求的原始路径
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.html" {
			t.Errorf("后端应收到重写后的路径，实际 %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("spa"))
	}))
	t.Cleanup(backend.Close)
	spa := Listener{Key: "l4", Sites: []Site{{Key: "s4", Enable: true, Domains: []string{"spa.test"}, Targets: []string{backend.URL},
		Rewrites: []Rewrite{{Pattern: "^/.*", To: "/index.html"}}}}}
	addr4, _ := startRunner(t, spa, nil, nil)
	get(t, noRedirect(), addr4, "spa.test", "/.env", nil)
	if h := wait(5); h.Listener != "l4" || h.Path != "/.env" || h.Status != 200 {
		t.Fatalf("重写后的上报不符: %+v", h)
	}
}
