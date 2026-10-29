package ssl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/miekg/dns"
)

// 本文件实现签发证书时限定 IPv4 / IPv6 出站（ACMEConfig.Network）。
// 主机有 IPv6 地址但 IPv6 不通时，系统会优先尝试 IPv6：HTTP 请求还能在超时后回退到 IPv4，
// lego 检查 TXT 生效时对权威服务器发出的 UDP 查询却只会用第一个地址，一直超时导致签发失败。

// networkName 返回网络类型的显示名称。
func networkName(network string) string {
	if network == NetworkIPv6 {
		return "IPv6"
	}
	return "IPv4"
}

// forceNetwork 返回只通过 network（tcp4/tcp6）建立连接的 HTTP 客户端，保留 c 的超时与 TLS 等设置；c 不会被修改。
func forceNetwork(c *http.Client, network string) *http.Client {
	out := &http.Client{}
	if c != nil {
		*out = *c
	}
	tr, ok := out.Transport.(*http.Transport)
	if !ok || tr == nil {
		tr = http.DefaultTransport.(*http.Transport)
	}
	tr = tr.Clone()
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, network, addr)
	}
	out.Transport = tr
	return out
}

// filterNameservers 保留与 network 同一地址族的 DNS 服务器（host:port）；主机名在查询时按 network 解析，一并保留。
func filterNameservers(servers []string, network string) []string {
	var out []string
	for _, s := range servers {
		host, _, err := net.SplitHostPort(s)
		if err != nil {
			host = s
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			out = append(out, s)
			continue
		}
		if ip.Unmap().Is4() == (network == NetworkIPv4) {
			out = append(out, s)
		}
	}
	return out
}

// netPreCheck 在限定网络下检查 TXT 是否生效，规则与 lego 默认检查一致：
// 先查递归 DNS（跟随 CNAME），再要求全部权威服务器都已返回验证值（关闭权威检查时直接通过）。
type netPreCheck struct {
	network string
	// nameservers 为已按地址族过滤的递归 DNS（host:port）
	nameservers []string
	// authoritative 为是否查询权威服务器
	authoritative bool
}

// authoritativePort 是权威服务器的 DNS 端口，测试中替换。
var authoritativePort = "53"

// dnsQueryTimeout 是单次 DNS 查询超时。
var dnsQueryTimeout = 10 * time.Second

// exchange 用限定网络向 server 发出一次查询，响应被截断时改用 TCP 重试。
func (p netPreCheck) exchange(m *dns.Msg, server string) (*dns.Msg, error) {
	udp, tcp := "udp4", "tcp4"
	if p.network == NetworkIPv6 {
		udp, tcp = "udp6", "tcp6"
	}
	r, _, err := (&dns.Client{Net: udp, Timeout: dnsQueryTimeout}).Exchange(m, server)
	if err == nil && r != nil && r.Truncated {
		r, _, err = (&dns.Client{Net: tcp, Timeout: dnsQueryTimeout}).Exchange(m, server)
	}
	if err != nil {
		return nil, fmt.Errorf("查询 %s 失败: %w", server, err)
	}
	return r, nil
}

// query 依次向 servers 查询，返回第一个成功的响应。
func (p netPreCheck) query(name string, qtype uint16, servers []string, recursive bool) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.SetEdns0(4096, false)
	m.RecursionDesired = recursive
	var errs []error
	for _, s := range servers {
		r, err := p.exchange(m, s)
		if err == nil {
			return r, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, errors.New("没有可用的 DNS 服务器")
	}
	return nil, errors.Join(errs...)
}

// nsAddrs 通过递归 DNS 解析权威服务器在当前地址族下的地址。
func (p netPreCheck) nsAddrs(host string) []string {
	qtype := dns.TypeA
	if p.network == NetworkIPv6 {
		qtype = dns.TypeAAAA
	}
	r, err := p.query(host, qtype, p.nameservers, true)
	if err != nil || r.Rcode != dns.RcodeSuccess {
		return nil
	}
	var out []string
	for _, rr := range r.Answer {
		switch v := rr.(type) {
		case *dns.A:
			out = append(out, net.JoinHostPort(v.A.String(), authoritativePort))
		case *dns.AAAA:
			out = append(out, net.JoinHostPort(v.AAAA.String(), authoritativePort))
		}
	}
	return out
}

// hasTXT 判断响应中是否有值为 value 的 TXT 记录。
func hasTXT(r *dns.Msg, value string) bool {
	for _, rr := range r.Answer {
		if txt, ok := rr.(*dns.TXT); ok && strings.Join(txt.Txt, "") == value {
			return true
		}
	}
	return false
}

// check 实现 dns01.WrapPreCheckFunc，替换 lego 默认的生效检查。
func (p netPreCheck) check(_, fqdn, value string, _ dns01.PreCheckFunc) (bool, error) {
	if len(p.nameservers) == 0 {
		return false, fmt.Errorf("检查 TXT 生效所用的 DNS 服务器中没有 %s 地址，请在「检查用 DNS」中填写 %s 的 DNS 服务器", networkName(p.network), networkName(p.network))
	}
	r, err := p.query(fqdn, dns.TypeTXT, p.nameservers, true)
	if err != nil {
		return false, fmt.Errorf("递归 DNS: %w", err)
	}
	// 与 lego 一致：验证记录是 CNAME 时检查 CNAME 指向的记录
	for _, rr := range r.Answer {
		if cn, ok := rr.(*dns.CNAME); ok && strings.EqualFold(cn.Hdr.Name, fqdn) {
			fqdn = cn.Target
		}
	}
	if !p.authoritative {
		return true, nil
	}
	zone, err := dns01.FindZoneByFqdnCustom(fqdn, p.nameservers)
	if err != nil {
		return false, fmt.Errorf("查询 %s 的权威区域失败: %w", fqdn, err)
	}
	r, err = p.query(zone, dns.TypeNS, p.nameservers, true)
	if err != nil {
		return false, fmt.Errorf("查询 %s 的权威服务器失败: %w", zone, err)
	}
	var hosts []string
	for _, rr := range r.Answer {
		if ns, ok := rr.(*dns.NS); ok {
			hosts = append(hosts, strings.ToLower(ns.Ns))
		}
	}
	if len(hosts) == 0 {
		return false, fmt.Errorf("没有查到 %s 的权威服务器", zone)
	}
	checked := 0
	for _, host := range hosts {
		addrs := p.nsAddrs(host)
		// 没有当前地址族地址的权威服务器无法访问，跳过
		if len(addrs) == 0 {
			continue
		}
		checked++
		r, err := p.query(fqdn, dns.TypeTXT, addrs, false)
		if err != nil {
			return false, fmt.Errorf("权威服务器 %s: %w", host, err)
		}
		if r.Rcode != dns.RcodeSuccess {
			return false, fmt.Errorf("权威服务器 %s 对 %s 返回 %s", host, fqdn, dns.RcodeToString[r.Rcode])
		}
		if !hasTXT(r, value) {
			return false, nil
		}
	}
	if checked == 0 {
		return false, fmt.Errorf("%s 的权威服务器都没有 %s 地址，可勾选「只查询上面的 DNS，不查询权威服务器」或改用其他网络", zone, networkName(p.network))
	}
	return true, nil
}
