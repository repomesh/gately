package guard

import (
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/wangwanjie/gately/internal/webprobe"
)

// statusClosed 是 Web 服务断开连接（return 444）时记录的状态码。
const statusClosed = 444

// compilePaths 编译路径规则：每行一条，以 ~ 开头为正则（按 Go 正则语法，不自动锚定），
// 其余为通配，* 匹配任意字符（含 /），整条路径匹配且不区分大小写。
func compilePaths(text string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, l := range splitLines(text) {
		var expr string
		if rest, ok := strings.CutPrefix(l, "~"); ok {
			expr = strings.TrimSpace(rest)
			if expr == "" {
				return nil, fmt.Errorf("路径规则 %q 缺少正则", l)
			}
		} else {
			if !strings.HasPrefix(l, "/") && !strings.HasPrefix(l, "*") {
				return nil, fmt.Errorf("路径规则 %q 应以 / 或 * 开头", l)
			}
			expr = "(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(l), `\*`, ".*") + "$"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("路径规则 %q 无效: %v", l, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// webMatcher 是编译后的 Web 路径探测规则。
type webMatcher struct {
	// listeners 为限定的监听 key，为空表示全部监听
	listeners   map[string]bool
	paths       []*regexp.Regexp
	ignores     []*regexp.Regexp
	anyStatus   bool
	unknownHost bool
}

func newWebMatcher(j Jail) (*webMatcher, error) {
	paths, err := compilePaths(j.Filter)
	if err != nil {
		return nil, err
	}
	ignores, err := compilePaths(j.Ignore)
	if err != nil {
		return nil, err
	}
	w := &webMatcher{paths: paths, ignores: ignores, anyStatus: j.AnyStatus, unknownHost: j.UnknownHost}
	if ls := splitLines(j.Target); len(ls) > 0 {
		w.listeners = map[string]bool{}
		for _, l := range ls {
			w.listeners[l] = true
		}
	}
	return w, nil
}

// matchPath 判断路径是否命中任一规则。同时比较原始路径与规范化后的路径，
// 防止用 //.env、/a/../.env 这类写法绕过。
func matchPath(res []*regexp.Regexp, p string) bool {
	if p == "" {
		p = "/"
	}
	clean := p
	if strings.HasPrefix(p, "/") {
		clean = path.Clean(p)
	}
	for _, re := range res {
		if re.MatchString(p) || (clean != p && re.MatchString(clean)) {
			return true
		}
	}
	return false
}

// pathMatched 判断路径是否命中规则：未填写路径规则时匹配全部路径，命中排除规则时不计数。
func (w *webMatcher) pathMatched(p string) bool {
	if len(w.paths) > 0 && !matchPath(w.paths, p) {
		return false
	}
	return !matchPath(w.ignores, p)
}

// match 判断一次请求是否计入规则。默认只统计 404 与 444（访问了不存在的内容或被断开连接），
// 避免把访问正常页面的用户和共用出口 IP 的用户算进去。
func (w *webMatcher) match(h webprobe.Hit) bool {
	if w.listeners != nil && !w.listeners[h.Listener] {
		return false
	}
	if w.unknownHost && !h.Unknown {
		return false
	}
	if !w.anyStatus && h.Status != http.StatusNotFound && h.Status != statusClosed {
		return false
	}
	return w.pathMatched(h.Path)
}

// refreshWebLocked 重建路径探测规则的快照，供每个请求无锁读取，调用方需持有 m.mu。
func (m *Module) refreshWebLocked() {
	var list []*jailRunner
	for _, r := range m.jails {
		// 停用的规则只是占位（m 为 nil），编译失败的规则没有 web
		if r.m != nil && r.jail.Source == SourceWeb && r.match != nil && r.match.web != nil {
			list = append(list, r)
		}
	}
	m.webJails.Store(&list)
}

// onWebHit 由 Web 服务在每个请求结束时同步调用，命中路径探测规则时计数。
func (m *Module) onWebHit(h webprobe.Hit) {
	p := m.webJails.Load()
	if p == nil || len(*p) == 0 || !h.Addr.IsValid() {
		return
	}
	a := h.Addr.Unmap()
	for _, r := range *p {
		if r.match.web.match(h) {
			r.record(a, fmt.Sprintf("%s %s%s %d", h.Method, h.Host, h.Path, h.Status))
		}
	}
}
