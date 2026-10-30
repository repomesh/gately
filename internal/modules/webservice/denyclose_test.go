package webservice

import (
	"bufio"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"github.com/wangwanjie/gately/internal/logbuf"
)

// TestDenyClose 验证开启“拒绝时断开连接”后，访问控制拒绝的请求不写任何响应直接断开、访问日志记为 444；
// 名单内的地址照常访问，未开启的站点仍返回 403。
func TestDenyClose(t *testing.T) {
	logs := logbuf.New(50, log.New(io.Discard, "", 0))
	l := Listener{Sites: []Site{
		// 测试客户端来自 127.0.0.1，不在白名单内
		{Enable: true, Domains: []string{"close.test"}, Type: TypeText, Text: "ok", AccessLog: true,
			IPMode: IPModeWhitelist, IPList: []string{"10.9.9.9"}, DenyClose: true},
		{Enable: true, Domains: []string{"allow.test"}, Type: TypeText, Text: "ok",
			IPMode: IPModeWhitelist, IPList: []string{"127.0.0.1"}, DenyClose: true},
		{Enable: true, Domains: []string{"forbid.test"}, Type: TypeText, Text: "ok",
			IPMode: IPModeWhitelist, IPList: []string{"10.9.9.9"}},
	}}
	addr, _ := startRunnerLogs(t, l, nil, nil, logs)

	if resp, body := get(t, noRedirect(), addr, "allow.test", "/", nil); resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("白名单内的地址应正常访问: %d %q", resp.StatusCode, body)
	}
	if resp, _ := get(t, noRedirect(), addr, "forbid.test", "/", nil); resp.StatusCode != 403 {
		t.Fatalf("未开启拒绝时断开连接应返回 403: %d", resp.StatusCode)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: close.test\r\n\r\n")
	b, err := io.ReadAll(bufio.NewReader(conn))
	if len(b) != 0 || err != nil {
		t.Fatalf("被拒绝的请求应直接断开连接，收到 %q（%v）", b, err)
	}
	got := logs.Query(0, 0, 0, &logbuf.Filter{Host: "close.test", Status: []logbuf.StatusRange{{Min: statusClosed, Max: statusClosed}}})
	if len(got) != 1 {
		t.Fatalf("应有 1 条 444 访问日志: %+v", got)
	}
}
