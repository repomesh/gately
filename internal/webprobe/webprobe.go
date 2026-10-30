// Package webprobe 把 Web 服务处理完的请求交给入侵防护，按路径探测规则计数封禁。
//
// 扫描器探测路径时请求量很大，这里不走事件总线（每个事件为每个订阅者开协程，还会经过通知中心），
// 而是在请求结束时同步调用入侵防护注册的处理函数，未注册时只有一次原子读取的开销。
package webprobe

import (
	"net/netip"
	"sync/atomic"
)

// Hit 是 Web 服务处理完的一次请求。
type Hit struct {
	// Addr 为客户端地址（已按可信代理、PROXY 协议还原）
	Addr netip.Addr
	// Listener 为监听的 key
	Listener string
	// Site 为处理请求的站点 key；没有任何站点处理（域名未匹配且没有默认站点）时为空
	Site string
	// Unknown 表示请求的域名没有匹配任何站点，由默认站点处理或直接返回 404
	Unknown bool
	Method  string
	Host    string
	// Path 为请求路径（已解码，未规范化）
	Path string
	// Status 为响应状态码，断开连接记为 444
	Status int
}

var handler atomic.Pointer[func(Hit)]

// SetHandler 注册处理函数，传 nil 取消注册。同一时间只有一个处理函数。
func SetHandler(f func(Hit)) {
	if f == nil {
		handler.Store(nil)
		return
	}
	handler.Store(&f)
}

// Report 在请求结束时调用，没有注册处理函数时直接返回。
func Report(h Hit) {
	if f := handler.Load(); f != nil {
		(*f)(h)
	}
}
