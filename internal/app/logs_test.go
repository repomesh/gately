package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangwanjie/gately/internal/logbuf"
)

func TestTightenConfigDir(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, mode os.FileMode) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// 模拟旧版本在忽略创建权限的文件系统上留下的文件
	hist := write("history/frp/20261019T164942.550610455Z.json", 0o755)
	frpc := write("frp/frpc.toml", 0o755)
	audit := write("audit.log.1", 0o644)
	log := write("logs/ddns.log", 0o755)
	// bin/ 里是用户放的程序，必须保持可执行
	bin := write("bin/frpc", 0o755)
	if n := tightenConfigDir(dir); n == 0 {
		t.Fatal("应有改动")
	}
	for _, p := range []string{hist, frpc, audit, log} {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s 权限 %o", p, st.Mode().Perm())
		}
	}
	for _, d := range []string{"history", "history/frp", "frp", "logs"} {
		st, _ := os.Stat(filepath.Join(dir, d))
		if st.Mode().Perm() != 0o700 {
			t.Fatalf("%s 权限 %o", d, st.Mode().Perm())
		}
	}
	if st, _ := os.Stat(bin); st.Mode().Perm() != 0o755 {
		t.Fatalf("bin/ 不应被改动，实际 %o", st.Mode().Perm())
	}
	if n := tightenConfigDir(dir); n != 0 {
		t.Fatalf("再次执行不应改动，实际 %d", n)
	}
}

// TestLogsFilterAPI 验证日志接口的过滤参数：级别、来源、关键词、IP、站点、状态码，以及按请求语言匹配译文。
func TestLogsFilterAPI(t *testing.T) {
	a, srv, c := loginApp(t)
	pf := a.modules[0].logs
	l := pf.Logger("规则A")
	l.With(logbuf.Fields{logbuf.FieldIP: "10.0.0.5", logbuf.FieldHost: "a.example.com", logbuf.FieldStatus: "404"}).Infof("10.0.0.5 GET a.example.com/x 404 1ms")
	l.With(logbuf.Fields{logbuf.FieldIP: "10.0.0.6", logbuf.FieldHost: "b.example.com", logbuf.FieldStatus: "502"}).Warnf("10.0.0.6 GET b.example.com/y 502 9ms")
	pf.Logger("规则B").Errorf("连接 192.168.1.9:80 失败")

	get := func(query, lang string) (int, []logbuf.Entry) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/api/portforward/logs?"+query, nil)
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out []logbuf.Entry
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, out
	}
	// 只关心本测试写入的日志：按来源前缀区分
	msgs := func(es []logbuf.Entry) []string {
		var out []string
		for _, e := range es {
			if strings.HasPrefix(e.Source, "规则") {
				out = append(out, e.Message)
			}
		}
		return out
	}

	cases := []struct {
		query string
		want  string
	}{
		{"level=warn", "[10.0.0.6 GET b.example.com/y 502 9ms 连接 192.168.1.9:80 失败]"},
		{"source=" + url.QueryEscape("规则B"), "[连接 192.168.1.9:80 失败]"},
		{"q=" + url.QueryEscape("GET -b.example"), "[10.0.0.5 GET a.example.com/x 404 1ms]"},
		// exclude 与 q 同时生效；exclude 中的词带不带 - 都是排除，包含任意一个即排除
		{"q=GET&exclude=" + url.QueryEscape("b.example"), "[10.0.0.5 GET a.example.com/x 404 1ms]"},
		{"exclude=" + url.QueryEscape("-404 \"192.168.1.9:80 失败\""), "[10.0.0.6 GET b.example.com/y 502 9ms]"},
		{"ip=10.0.0.0/24", "[10.0.0.5 GET a.example.com/x 404 1ms 10.0.0.6 GET b.example.com/y 502 9ms]"},
		{"ip=192.168.1.9", "[连接 192.168.1.9:80 失败]"},
		{"host=b.example", "[10.0.0.6 GET b.example.com/y 502 9ms]"},
		{"status=4xx", "[10.0.0.5 GET a.example.com/x 404 1ms]"},
		{"status=500-599&limit=1", "[10.0.0.6 GET b.example.com/y 502 9ms]"},
		{"from=" + url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339)), "[]"},
	}
	for _, cs := range cases {
		code, es := get(cs.query, "zh-CN")
		if code != http.StatusOK {
			t.Fatalf("%s: 状态码 %d", cs.query, code)
		}
		if got := fmt.Sprint(msgs(es)); got != cs.want {
			t.Errorf("%s = %s，期望 %s", cs.query, got, cs.want)
		}
	}
	// 毫秒时间戳可以直接作为 from/to
	if code, es := get(fmt.Sprintf("from=%d", time.Now().Add(-time.Hour).UnixMilli()), "zh-CN"); code != http.StatusOK || len(msgs(es)) != 3 {
		t.Errorf("毫秒时间戳 from：状态码 %d，%d 条", code, len(msgs(es)))
	}
	now := time.Now()
	reversed := "from=" + url.QueryEscape(now.Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339))
	// 位数不足的纯数字（只填了年份）、from 晚于 to 都是无效参数
	for _, q := range []string{"status=abc", "level=fatal", "from=yesterday", "to=x", "from=2026", "to=17000000000", reversed} {
		if code, _ := get(q, ""); code != http.StatusBadRequest {
			t.Errorf("%s 应返回 400，得到 %d", q, code)
		}
	}

	// 系统日志里有登录记录，英文界面用译文关键词也能搜到
	req, _ := http.NewRequest("GET", srv.URL+"/api/logs?q="+url.QueryEscape("signed in from"), nil)
	req.Header.Set("Accept-Language", "en")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sys []logbuf.Entry
	_ = json.NewDecoder(resp.Body).Decode(&sys)
	if len(sys) == 0 || !strings.Contains(sys[len(sys)-1].Message, "signed in from") {
		t.Errorf("英文关键词应匹配登录日志的译文: %+v", sys)
	}
}
