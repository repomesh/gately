package cloudflared

import (
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/wangwanjie/gately/internal/config"
	"github.com/wangwanjie/gately/internal/logbuf"
	"github.com/wangwanjie/gately/internal/luckyimport"
	"github.com/wangwanjie/gately/internal/module"
)

func TestImportLuckyCloudflared(t *testing.T) {
	st, _ := config.Open(t.TempDir())
	m := New()
	if err := m.Init(module.Env{Store: st, Logs: logbuf.New(20, log.New(io.Discard, "", 0))}); err != nil {
		t.Fatal(err)
	}
	doc := luckyimport.Document{"List": json.RawMessage(`[
	  {"Key":"a","Remark":"nas","Enable":true,"Type":"tunnel","Params":{"Token":"eyJ0","EdgeIpVersion":"4","HaConnections":4,"Protocol":"http2","EdgeBindAddress":"","NoTlsVerify":true,"CFApiToken":"cfapi"}},
	  {"Key":"b","Remark":"","Enable":true,"Type":"tunnel","Params":{"Token":"eyJ1","Protocol":"auto","HaConnections":2}},
	  {"Key":"c","Remark":"api","Enable":true,"Type":"tunnel","Params":{"Token":"","CFApiToken":"x"}},
	  {"Key":"d","Remark":"ssh","Enable":true,"Type":"access","Params":{"Hostname":"ssh.example.com"}}
	]`)}
	rep, err := m.ImportLucky(doc, luckyimport.Options{KeepEnabled: true})
	if err != nil || rep == nil {
		t.Fatalf("导入失败: %v %v", rep, err)
	}
	if rep.Found != 4 || rep.Imported != 2 || len(m.cfg.Tunnels) != 2 {
		t.Fatalf("统计错误: %+v %+v", rep, m.cfg.Tunnels)
	}
	tn := m.cfg.Tunnels[0]
	if tn.Name != "nas" || tn.Mode != ModeToken || tn.Token != "eyJ0" || tn.Protocol != "http2" || tn.EdgeIPVersion != "4" || tn.APIToken != "cfapi" || !tn.Enable {
		t.Fatalf("隧道映射错误: %+v", tn)
	}
	if m.cfg.Tunnels[1].Name != "b" {
		t.Fatal("备注为空时应使用 Key 作为名称")
	}
	joined := strings.Join(rep.Warnings, "\n")
	for _, want := range []string{"边缘连接数 2", "api: 跳过", "ssh: 跳过"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少提示 %q:\n%s", want, joined)
		}
	}
	if rep, _ := m.ImportLucky(doc, luckyimport.Options{}); rep.Imported != 0 {
		t.Fatal("重复导入应跳过同名隧道")
	}
}
