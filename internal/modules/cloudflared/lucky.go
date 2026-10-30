package cloudflared

import (
	"encoding/json"
	"fmt"

	"github.com/wangwanjie/gately/internal/crud"
	"github.com/wangwanjie/gately/internal/luckyimport"
)

// luckyInstance 是 Lucky lucky_cloudflared.lkcf 中的一个实例。
type luckyInstance struct {
	Key    string
	Remark string
	Enable bool
	// Type 为 tunnel（运行隧道）或 access（在本机访问经 Cloudflare Access 保护的 TCP 服务）
	Type   string
	Params struct {
		Token           string
		Protocol        string
		EdgeIpVersion   string
		HaConnections   int
		EdgeBindAddress string
		CFApiToken      string
	}
}

// ImportLucky 实现 luckyimport.Importer，迁移使用隧道令牌运行的隧道。
func (m *Module) ImportLucky(doc luckyimport.Document, opts luckyimport.Options) (*luckyimport.Report, error) {
	// frp 与存储管理同样使用 List 字段，按实例类型识别
	if !luckyimport.AllTypes(luckyimport.ListTypes(doc), "tunnel", "access") {
		return nil, nil
	}
	var src []luckyInstance
	if err := json.Unmarshal(doc["List"], &src); err != nil {
		return nil, err
	}
	rep := &luckyimport.Report{Module: ID, Title: m.Title(), Found: len(src)}
	var tunnels []Tunnel
	for _, li := range src {
		name := li.Remark
		if name == "" {
			name = li.Key
		}
		if li.Type == "access" {
			rep.Warnings = append(rep.Warnings, name+": 跳过，Cloudflare Access 访问端暂不支持，请在本机直接运行 cloudflared access")
			continue
		}
		p := li.Params
		if p.Token == "" {
			rep.Warnings = append(rep.Warnings, name+": 跳过，未填写隧道令牌（仅填写 API 令牌的隧道暂不支持）")
			continue
		}
		t := Tunnel{Name: name, Enable: li.Enable && opts.KeepEnabled, Mode: ModeToken, Token: p.Token, APIToken: p.CFApiToken, Protocol: p.Protocol, EdgeIPVersion: p.EdgeIpVersion}
		if p.HaConnections != 0 && p.HaConnections != 4 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: 边缘连接数 %d 暂不支持，使用默认值 4", name, p.HaConnections))
		}
		if p.EdgeBindAddress != "" {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: 边缘连接绑定地址 %s 暂不支持，已忽略", name, p.EdgeBindAddress))
		}
		if err := t.Validate(); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: 跳过，%v", name, err))
			continue
		}
		tunnels = append(tunnels, t)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing := map[string]bool{}
	for _, t := range m.cfg.Tunnels {
		existing[t.Name] = true
	}
	var fresh []Tunnel
	for _, t := range tunnels {
		if existing[t.Name] {
			rep.Warnings = append(rep.Warnings, luckyimport.Skipped(t.Name))
			continue
		}
		existing[t.Name] = true
		t.Key = crud.NewKey()
		fresh = append(fresh, t)
	}
	rep.Imported = len(fresh)
	if opts.DryRun {
		return rep, nil
	}
	old := m.cfg.Tunnels
	m.cfg.Tunnels = append(append([]Tunnel(nil), old...), fresh...)
	if err := m.saveLocked(); err != nil {
		m.cfg.Tunnels = old
		return nil, err
	}
	for _, t := range fresh {
		m.applyLocked(t.Key)
	}
	return rep, nil
}
