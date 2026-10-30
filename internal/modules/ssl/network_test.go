package ssl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// fakeZone 是一个同时充当递归与权威服务器的假 DNS：
// ns4 只有 IPv4 地址，ns6 只有 IPv6 地址，TXT 值可在测试中修改。
type fakeZone struct {
	zone string
	mu   sync.Mutex
	txt  string
}

func (z *fakeZone) setTXT(v string) {
	z.mu.Lock()
	z.txt = v
	z.mu.Unlock()
}

func (z *fakeZone) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(req)
	q := req.Question[0]
	name := strings.ToLower(q.Name)
	soa := &dns.SOA{Hdr: dns.RR_Header{Name: z.zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 60}, Ns: "ns4." + z.zone, Mbox: "admin." + z.zone, Minttl: 60}
	switch {
	case q.Qtype == dns.TypeSOA && name == z.zone:
		m.Answer = append(m.Answer, soa)
	case q.Qtype == dns.TypeNS && name == z.zone:
		for _, h := range []string{"ns4.", "ns6."} {
			m.Answer = append(m.Answer, &dns.NS{Hdr: dns.RR_Header{Name: z.zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 60}, Ns: h + z.zone})
		}
	case q.Qtype == dns.TypeA && name == "ns4."+z.zone:
		m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("127.0.0.1")})
	case q.Qtype == dns.TypeAAAA && name == "ns6."+z.zone:
		m.Answer = append(m.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: net.ParseIP("::1")})
	case q.Qtype == dns.TypeTXT && name == "_acme-challenge."+z.zone:
		z.mu.Lock()
		if z.txt != "" {
			m.Answer = append(m.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60}, Txt: []string{z.txt}})
		}
		z.mu.Unlock()
	case strings.HasSuffix(name, "."+z.zone):
		// 区域内不存在的名称：NOERROR + 权威部分带 SOA
		m.Ns = append(m.Ns, soa)
	default:
		m.Rcode = dns.RcodeRefused
	}
	_ = w.WriteMsg(m)
}

// startFakeDNS 在 addr（如 127.0.0.1:0）上启动 UDP DNS 服务，返回实际地址。
func startFakeDNS(t *testing.T, addr string, h dns.Handler) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Skipf("无法监听 %s: %v", addr, err)
	}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: h, NotifyStartedFunc: func() { close(started) }}
	go func() {
		_ = srv.ActivateAndServe()
	}()
	<-started
	t.Cleanup(func() {
		_ = srv.Shutdown()
	})
	return pc.LocalAddr().String()
}

// useAuthoritativePort 让权威查询发往测试服务的端口。
func useAuthoritativePort(t *testing.T, addr string) {
	t.Helper()
	_, port, _ := net.SplitHostPort(addr)
	old, oldTimeout := authoritativePort, dnsQueryTimeout
	authoritativePort, dnsQueryTimeout = port, 2*time.Second
	t.Cleanup(func() {
		authoritativePort, dnsQueryTimeout = old, oldTimeout
	})
}

// 限定 IPv4 时只查询有 IPv4 地址的权威服务器，跳过只有 IPv6 地址的 ns6（issue #35）
func TestNetPreCheckIPv4(t *testing.T) {
	z := &fakeZone{zone: "v4check.example."}
	addr := startFakeDNS(t, "127.0.0.1:0", z)
	useAuthoritativePort(t, addr)
	pc := netPreCheck{network: NetworkIPv4, nameservers: []string{addr}, authoritative: true}
	fqdn := "_acme-challenge.v4check.example."

	if ok, err := pc.check("", fqdn, "token", nil); ok || err != nil {
		t.Fatalf("TXT 尚未生效时应返回未通过且无错误: %v %v", ok, err)
	}
	z.setTXT("token")
	if ok, err := pc.check("", fqdn, "token", nil); !ok || err != nil {
		t.Fatalf("TXT 生效后应通过: %v %v", ok, err)
	}
	// 关闭权威检查时只查询递归 DNS
	pc.authoritative = false
	z.setTXT("")
	if ok, err := pc.check("", fqdn, "token", nil); !ok || err != nil {
		t.Fatalf("关闭权威检查后应直接通过: %v %v", ok, err)
	}
}

func TestNetPreCheckIPv6(t *testing.T) {
	z := &fakeZone{zone: "v6check.example.", txt: "token"}
	addr := startFakeDNS(t, "[::1]:0", z)
	useAuthoritativePort(t, addr)
	pc := netPreCheck{network: NetworkIPv6, nameservers: []string{addr}, authoritative: true}
	if ok, err := pc.check("", "_acme-challenge.v6check.example.", "token", nil); !ok || err != nil {
		t.Fatalf("IPv6 下应通过 ns6 完成检查: %v %v", ok, err)
	}
	if ok, _ := pc.check("", "_acme-challenge.v6check.example.", "other", nil); ok {
		t.Fatal("值不一致时不应通过")
	}
}

// 权威服务器都没有当前地址族的地址时给出明确错误
func TestNetPreCheckNoReachableNS(t *testing.T) {
	z := &fakeZone{zone: "nov6.example.", txt: "token"}
	addr := startFakeDNS(t, "127.0.0.1:0", z)
	useAuthoritativePort(t, addr)
	// 递归 DNS 用主机名填写时按 IPv6 解析，这里直接给出 IPv4 地址模拟只剩 IPv4 的权威服务器
	pc := netPreCheck{network: NetworkIPv6, nameservers: []string{addr}, authoritative: true}
	_, err := pc.check("", "_acme-challenge.nov6.example.", "token", nil)
	if err == nil {
		t.Fatal("IPv4 地址的 DNS 服务器在 IPv6 下应查询失败")
	}
	pc.nameservers = nil
	if _, err := pc.check("", "_acme-challenge.nov6.example.", "token", nil); err == nil || !strings.Contains(err.Error(), "IPv6") {
		t.Fatalf("没有 DNS 服务器时应提示地址族: %v", err)
	}
}

// 权威服务器只有 IPv6 地址时，IPv4 下给出明确错误
func TestNetPreCheckAllNSOtherFamily(t *testing.T) {
	z := &fakeZone{zone: "only6.example.", txt: "token"}
	h := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		// 去掉 ns4 的 A 记录：所有权威服务器都只有 IPv6 地址
		if req.Question[0].Qtype == dns.TypeA {
			m := new(dns.Msg)
			m.SetReply(req)
			_ = w.WriteMsg(m)
			return
		}
		z.ServeDNS(w, req)
	})
	addr := startFakeDNS(t, "127.0.0.1:0", h)
	useAuthoritativePort(t, addr)
	pc := netPreCheck{network: NetworkIPv4, nameservers: []string{addr}, authoritative: true}
	_, err := pc.check("", "_acme-challenge.only6.example.", "token", nil)
	if err == nil || !strings.Contains(err.Error(), "都没有 IPv4 地址") {
		t.Fatalf("应提示权威服务器没有 IPv4 地址: %v", err)
	}
}

func TestFilterNameservers(t *testing.T) {
	// 主机名按地址族解析为 IP，解析不到的丢弃
	old := lookupNetIP
	lookupNetIP = func(_ context.Context, network, host string) ([]netip.Addr, error) {
		switch {
		case host == "dns.example" && network == "ip4":
			return []netip.Addr{netip.MustParseAddr("9.9.9.9")}, nil
		case host == "dns.example" && network == "ip6":
			return []netip.Addr{netip.MustParseAddr("2620:fe::fe")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() {
		lookupNetIP = old
	})
	in := []string{"1.1.1.1:53", "[2606:4700:4700::1111]:53", "dns.example:5353", "[::ffff:8.8.8.8]:53", "missing.example:53"}
	if got := fmt.Sprint(filterNameservers(in, NetworkIPv4)); got != "[1.1.1.1:53 9.9.9.9:5353 [::ffff:8.8.8.8]:53]" {
		t.Errorf("IPv4 过滤结果错误: %s", got)
	}
	if got := fmt.Sprint(filterNameservers(in, NetworkIPv6)); got != "[[2606:4700:4700::1111]:53 [2620:fe::fe]:5353]" {
		t.Errorf("IPv6 过滤结果错误: %s", got)
	}
}

// 权威服务器地址查询失败时返回错误等待重试，不能跳过该服务器后判定通过
func TestNetPreCheckNSLookupError(t *testing.T) {
	z := &fakeZone{zone: "nserr.example.", txt: "token"}
	h := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		// ns6 的 AAAA 查询返回 SERVFAIL，模拟临时故障
		if req.Question[0].Qtype == dns.TypeAAAA && strings.EqualFold(req.Question[0].Name, "ns6.nserr.example.") {
			m := new(dns.Msg)
			m.SetRcode(req, dns.RcodeServerFailure)
			_ = w.WriteMsg(m)
			return
		}
		z.ServeDNS(w, req)
	})
	addr := startFakeDNS(t, "[::1]:0", h)
	useAuthoritativePort(t, addr)
	pc := netPreCheck{network: NetworkIPv6, nameservers: []string{addr}, authoritative: true}
	ok, err := pc.check("", "_acme-challenge.nserr.example.", "token", nil)
	if ok || err == nil || !strings.Contains(err.Error(), "ns6.nserr.example.") {
		t.Fatalf("权威服务器地址查询失败时应返回错误: %v %v", ok, err)
	}
}

// 经代理访问时，连代理不受地址族限制
func TestForceNetworkProxy(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	var mu sync.Mutex
	// 只监听 IPv4 的 HTTP 代理：直接应答被代理的请求
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte("proxied"))
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	proxyURL, _ := url.Parse("http://localhost:" + port)
	base := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := forceNetwork(base, NetworkIPv6).Get("http://ca.invalid/directory")
	if err != nil {
		t.Fatalf("限定 IPv6 时仍应能连接只有 IPv4 地址的代理: %v", err)
	}
	_ = resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("请求应经过代理: %d", hits)
	}
}

// forceNetwork 限定地址族后，localhost 只会连接对应地址
func TestForceNetwork(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	url := "http://localhost:" + port + "/"

	base := &http.Client{Timeout: 5 * time.Second}
	resp, err := forceNetwork(base, NetworkIPv4).Get(url)
	if err != nil {
		t.Fatalf("IPv4 应能连接 127.0.0.1: %v", err)
	}
	_ = resp.Body.Close()
	if resp, err := forceNetwork(base, NetworkIPv6).Get(url); err == nil {
		_ = resp.Body.Close()
		t.Fatal("限定 IPv6 时不应连接到只监听 IPv4 的服务")
	}
	if base.Transport != nil {
		t.Fatal("不应修改传入的客户端")
	}
}

func TestACMENetworkValidate(t *testing.T) {
	a := ACMEConfig{Domains: []string{"example.com"}, Email: "a@example.com", Challenge: ChallengeHTTP, Network: "udp"}
	if err := a.validate(Settings{}); err == nil {
		t.Fatal("未知网络类型应报错")
	}
	a.Network = NetworkIPv6
	if err := a.validate(Settings{}); err != nil {
		t.Fatalf("tcp6 应合法: %v", err)
	}
}
