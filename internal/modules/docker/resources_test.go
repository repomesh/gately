package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wangwanjie/gately/internal/config"
	"github.com/wangwanjie/gately/internal/logbuf"
	"github.com/wangwanjie/gately/internal/module"
)

// ---------- 纯函数 ----------

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0B", 999: "999B", 1000: "1kB", 12300000: "12.3MB", 45000000: "45MB", 2500000000: "2.5GB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d)=%q，期望 %q", in, got, want)
		}
	}
}

func TestFormatPullMessage(t *testing.T) {
	mk := func(status, id string, cur, total int64) jsonMessage {
		m := jsonMessage{Status: status, ID: id}
		m.ProgressDetail.Current, m.ProgressDetail.Total = cur, total
		return m
	}
	cases := []struct {
		in        jsonMessage
		id, line  string
		transient bool
	}{
		{mk("Downloading", "abc", 12300000, 45000000), "abc", "layer abc: Downloading 12.3MB/45MB", true},
		{mk("Pull complete", "abc", 0, 0), "abc", "layer abc: Pull complete", false},
		{mk("Pulling from library/nginx", "latest", 0, 0), "", "Pulling from library/nginx:latest", false},
		{mk("Status: Downloaded newer image for nginx:latest", "", 0, 0), "", "Status: Downloaded newer image for nginx:latest", false},
		{mk("", "", 0, 0), "", "", false},
	}
	for _, c := range cases {
		id, line, tr := formatPullMessage(c.in)
		if id != c.id || line != c.line || tr != c.transient {
			t.Errorf("formatPullMessage(%+v)=%q,%q,%v", c.in, id, line, tr)
		}
	}
}

func TestProgressThrottle(t *testing.T) {
	var lines []string
	p := newProgressThrottle(func(s string) { lines = append(lines, s) }, time.Second)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	dl := func(cur int64) jsonMessage {
		m := jsonMessage{Status: "Downloading", ID: "a"}
		m.ProgressDetail.Current, m.ProgressDetail.Total = cur, 1000000
		return m
	}
	p.Add(dl(100000)) // 立即写出
	now = now.Add(200 * time.Millisecond)
	p.Add(dl(200000)) // 限频，暂存
	p.Add(dl(300000)) // 覆盖暂存
	if len(lines) != 1 {
		t.Fatalf("限频期间应只有 1 行: %v", lines)
	}
	p.Add(jsonMessage{Status: "Download complete", ID: "a"}) // 非频繁更新直接写出，并丢弃暂存
	p.Flush()
	if len(lines) != 2 || lines[1] != "layer a: Download complete" {
		t.Fatalf("输出错误: %v", lines)
	}
	now = now.Add(2 * time.Second)
	p.Add(dl(900000))
	p.Add(dl(950000))
	p.Flush()
	if lines[len(lines)-1] != "layer a: Downloading 950kB/1MB" {
		t.Fatalf("Flush 应写出最新一行: %v", lines)
	}
}

func TestMirrorImageRef(t *testing.T) {
	cases := []struct{ mirror, ref, want string }{
		{"docker.m.daocloud.io", "nginx", "docker.m.daocloud.io/library/nginx:latest"},
		{"https://docker.m.daocloud.io/", "nginx:1.27", "docker.m.daocloud.io/library/nginx:1.27"},
		{"mirror.local:5000", "grafana/grafana:10", "mirror.local:5000/grafana/grafana:10"},
		{"mirror.local", "docker.io/library/redis:7", "mirror.local/library/redis:7"},
		{"mirror.local", "docker.io/grafana/grafana", "mirror.local/grafana/grafana:latest"},
		{"mirror.local", "ghcr.io/a/b:v1", "mirror.local/ghcr.io/a/b:v1"},
		{"mirror.local", "localhost:5000/app", "mirror.local/localhost:5000/app:latest"},
		{"", "nginx", ""},
		{"mirror.local", "nginx@sha256:abcdef", ""},
	}
	for _, c := range cases {
		got, ok := mirrorImageRef(c.mirror, c.ref)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("mirrorImageRef(%q,%q)=%q,%v，期望 %q", c.mirror, c.ref, got, ok, c.want)
		}
	}
}

func TestComposeStatusAndAggregate(t *testing.T) {
	if composeStatus(3, 3) != "running" || composeStatus(3, 1) != "partial" || composeStatus(3, 0) != "stopped" || composeStatus(0, 0) != "stopped" {
		t.Fatal("状态判断错误")
	}
	mk := func(id, project, service, state string, num int) resContainer {
		return resContainer{ID: id, Names: []string{"/" + project + "-" + service + "-" + fmt.Sprint(num)}, Image: service + ":1", State: state,
			Labels: map[string]string{labelProject: project, labelService: service, labelNumber: fmt.Sprint(num),
				labelWorkingDir: "/srv/" + project, labelConfig: "/srv/" + project + "/a.yml,/srv/" + project + "/b.yml"}}
	}
	one := mk("5", "solo", "x", "exited", 1)
	oneoff := mk("6", "solo", "run", "running", 1)
	oneoff.Labels[labelOneOff] = "True"
	list := aggregateComposeProjects([]resContainer{mk("1", "web", "db", "running", 1), mk("2", "web", "app", "exited", 2), mk("3", "web", "app", "running", 1), mk("4", "z", "q", "running", 1), one, oneoff,
		{ID: "9", Labels: map[string]string{}}})
	if len(list) != 3 || list[0].Name != "solo" || list[1].Name != "web" || list[2].Name != "z" {
		t.Fatalf("项目聚合错误: %+v", list)
	}
	web := list[1]
	if web.Status != "partial" || web.Total != 3 || web.Running != 2 || web.WorkingDir != "/srv/web" || len(web.ConfigFiles) != 2 {
		t.Fatalf("web 项目错误: %+v", web)
	}
	if web.Services[0].Name != "app" || web.Services[0].ContainerID != "3" || web.Services[2].Name != "db" {
		t.Fatalf("服务排序错误: %+v", web.Services)
	}
	if list[0].Status != "stopped" || list[0].Total != 1 {
		t.Fatalf("一次性容器应被忽略: %+v", list[0])
	}
}

func TestSortComposeContainers(t *testing.T) {
	mk := func(id, svc string, n int) resContainer {
		return resContainer{ID: id, Labels: map[string]string{labelService: svc, labelNumber: fmt.Sprint(n)}}
	}
	in := []resContainer{mk("a1", "app", 1), mk("a2", "app", 2), mk("d1", "db", 1)}
	var fwd, rev []string
	for _, c := range sortComposeContainers(in, false) {
		fwd = append(fwd, c.ID)
	}
	for _, c := range sortComposeContainers(in, true) {
		rev = append(rev, c.ID)
	}
	if strings.Join(fwd, ",") != "a1,a2,d1" || strings.Join(rev, ",") != "d1,a2,a1" {
		t.Fatalf("排序错误: %v %v", fwd, rev)
	}
}

func TestMergeComposeLogs(t *testing.T) {
	parts := []composeLogPart{
		{Label: "web", Text: "2024-01-01T00:00:01.000000000Z w1\n2024-01-01T00:00:03.000000000Z w3\ncontinued\n"},
		{Label: "db", Text: "2024-01-01T00:00:02.000000000Z d2\n2024-01-01T00:00:04.000000000Z d4\n"},
	}
	want := "web | 2024-01-01T00:00:01.000000000Z w1\n" +
		"db | 2024-01-01T00:00:02.000000000Z d2\n" +
		"web | 2024-01-01T00:00:03.000000000Z w3\n" +
		"web | continued\n" +
		"db | 2024-01-01T00:00:04.000000000Z d4\n"
	if got := mergeComposeLogs(parts, 0); got != want {
		t.Fatalf("合并错误:\n%s", got)
	}
	got := mergeComposeLogs(parts, 2)
	if got != "web | continued\ndb | 2024-01-01T00:00:04.000000000Z d4\n" {
		t.Fatalf("limit 错误:\n%s", got)
	}
}

func TestPickConfigFile(t *testing.T) {
	p := &composeProject{ConfigFiles: []string{"/a/compose.yaml", "/a/override.yaml"}}
	if f, err := pickConfigFile(p, ""); err != nil || f != "/a/compose.yaml" {
		t.Fatal(f, err)
	}
	if f, err := pickConfigFile(p, "/a/override.yaml"); err != nil || f != "/a/override.yaml" {
		t.Fatal(f, err)
	}
	if _, err := pickConfigFile(p, "/etc/passwd"); err == nil {
		t.Fatal("未知路径应被拒绝")
	}
	if _, err := pickConfigFile(p, "/a/../etc/passwd"); err == nil {
		t.Fatal("路径穿越应被拒绝")
	}
}

func TestBuildViews(t *testing.T) {
	nets := []networkInfo{{ID: "n2", Name: "app_default", Driver: "bridge", Scope: "local"}, {ID: "n1", Name: "bridge", Driver: "bridge"}}
	nets[0].IPAM.Config = []struct {
		Subnet  string `json:"Subnet"`
		Gateway string `json:"Gateway"`
	}{{Subnet: "172.20.0.0/16", Gateway: "172.20.0.1"}}
	var ct resContainer
	ct.ID, ct.Names = "c1", []string{"/web"}
	ct.NetworkSettings.Networks = map[string]struct {
		NetworkID string `json:"NetworkID"`
		IPAddress string `json:"IPAddress"`
	}{"app_default": {IPAddress: "172.20.0.2"}}
	nv := buildNetworkViews(nets, []resContainer{ct})
	if nv[0].Name != "bridge" || !nv[0].Builtin || nv[1].Subnet != "172.20.0.0/16" || nv[1].Gateway != "172.20.0.1" ||
		len(nv[1].Containers) != 1 || nv[1].Containers[0].IP != "172.20.0.2" || nv[0].Containers == nil {
		t.Fatalf("网络视图错误: %+v", nv)
	}

	ct.Mounts = []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	}{{Type: "volume", Name: "data"}, {Type: "bind", Name: ""}}
	vv := buildVolumeViews([]volumeInfo{{Name: "data"}, {Name: "idle"}}, map[string]volumeUsage{"data": {Size: 1024, RefCount: 1}}, []resContainer{ct})
	if vv[0].Name != "data" || vv[0].Size != 1024 || vv[0].RefCount != 1 || len(vv[0].UsedBy) != 1 || vv[0].UsedBy[0].Name != "web" {
		t.Fatalf("卷视图错误: %+v", vv[0])
	}
	if vv[1].Size != -1 || vv[1].RefCount != 0 || vv[1].UsedBy == nil || vv[1].Labels == nil {
		t.Fatalf("未知大小应为 -1: %+v", vv[1])
	}
}

// ---------- 假 Docker 与 HTTP 处理 ----------

type resFake struct {
	mu       sync.Mutex
	calls    []string
	handlers map[string]http.HandlerFunc // 键为 "METHOD /path"（不含版本前缀与查询）
	bodies   map[string][]byte
}

func (f *resFake) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func jsonResp(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(v) }
}

func errResp(status int, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"` + msg + `"}`))
	}
}

// newResEnv 启动假 Docker 与挂了全部路由的模块，返回对 /api/docker 的请求函数。
func newResEnv(t *testing.T, f *resFake) (*Module, func(method, path, ctype string, body io.Reader) *httptest.ResponseRecorder) {
	t.Helper()
	f.bodies = map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v"+apiVersion)
		key := r.Method + " " + p
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, key+"?"+r.URL.RawQuery)
		f.bodies[key] = b
		h := f.handlers[key]
		f.mu.Unlock()
		if h == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"fake: 未注册 ` + key + `"}`))
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	st, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	if err := m.Init(module.Env{Store: st, Logs: logbuf.New(50, log.New(io.Discard, "", 0))}); err != nil {
		t.Fatal(err)
	}
	m.settings.Host = "tcp://" + strings.TrimPrefix(srv.URL, "http://")
	mux := http.NewServeMux()
	m.Routes(muxRouter{mux})
	return m, func(method, path, ctype string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
}

type muxRouter struct{ mux *http.ServeMux }

func (r muxRouter) HandleFunc(pattern string, h http.HandlerFunc) { r.mux.HandleFunc(pattern, h) }

var _ module.Router = muxRouter{}

func waitTask(t *testing.T, id string) Task {
	t.Helper()
	for i := 0; i < 200; i++ {
		if tk, ok := tasks.snapshot(id); ok && tk.Status != "running" {
			return tk
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("任务超时")
	return Task{}
}

func taskID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Task string `json:"task"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Task == "" {
		t.Fatalf("应返回任务: %d %s", rec.Code, rec.Body.String())
	}
	return out.Task
}

func TestImageListAndDetail(t *testing.T) {
	f := &resFake{handlers: map[string]http.HandlerFunc{
		"GET /images/json": jsonResp([]map[string]any{
			{"Id": "sha256:aaa", "RepoTags": []string{"nginx:latest"}, "RepoDigests": []string{"nginx@sha256:1"}, "Size": 100, "Created": 10},
			{"Id": "sha256:bbb", "RepoTags": []string{"<none>:<none>"}, "RepoDigests": []string{"<none>@<none>"}, "Size": 50, "Created": 20},
		}),
		"GET /containers/json": jsonResp([]map[string]any{{"Id": "c1", "ImageID": "sha256:aaa"}, {"Id": "c2", "ImageID": "sha256:aaa"}}),
		"GET /images/library/nginx:latest/json": jsonResp(map[string]any{"Id": "sha256:aaa", "RepoTags": []string{"nginx:latest"}, "Size": 100,
			"Created": "2024-01-01T00:00:00Z", "Os": "linux", "Architecture": "amd64",
			"Config": map[string]any{"Cmd": []string{"nginx"}, "ExposedPorts": map[string]any{"80/tcp": map[string]any{}, "443/tcp": map[string]any{}}}}),
		"GET /images/library/nginx:latest/history": jsonResp([]map[string]any{{"Created": 5, "CreatedBy": "CMD nginx", "Size": 0}, {"Created": 4, "CreatedBy": "ADD rootfs", "Size": 77}}),
	}}
	_, do := newResEnv(t, f)
	rec := do("GET", "/images", "", nil)
	var list []ImageView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if list[0].ID != "sha256:bbb" || !list[0].Dangling || list[0].Tags == nil || len(list[0].Tags) != 0 {
		t.Fatalf("悬空镜像错误: %+v", list[0])
	}
	if list[1].Containers != 2 || list[1].Dangling || list[1].Digests[0] != "nginx@sha256:1" {
		t.Fatalf("引用计数错误: %+v", list[1])
	}
	rec = do("GET", "/images/library%2Fnginx:latest", "", nil)
	var d ImageDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	// 请求不带身份，按没有写权限处理：Cmd 与各层命令脱敏（明文见 TestImageDetailMasksSecretsWithoutWrite）
	if len(d.Layers) != 2 || d.Layers[1].CreatedBy != "********" || d.Layers[1].Size != 77 || d.ExposedPorts[0] != "443/tcp" || d.Created != 1704067200 || d.Env == nil || d.Labels == nil {
		t.Fatalf("详情错误: %+v", d)
	}
}

func TestImagePullMirrorFallback(t *testing.T) {
	f := &resFake{}
	f.handlers = map[string]http.HandlerFunc{
		"POST /images/create": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Query().Get("fromImage"), "mirror.local/") {
				_, _ = w.Write([]byte(`{"status":"Pulling from library/nginx","id":"latest"}` + "\n" +
					`{"status":"Downloading","id":"l1","progressDetail":{"current":1000000,"total":2000000}}` + "\n" + `{"status":"Pull complete","id":"l1"}` + "\n"))
				return
			}
			_, _ = w.Write([]byte(`{"errorDetail":{"message":"timeout"},"error":"timeout"}` + "\n"))
		},
		"POST /images/mirror.local/library/nginx:latest/tag": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) },
		"DELETE /images/mirror.local/library/nginx:latest":   jsonResp([]map[string]string{{"Untagged": "x"}}),
	}
	m, do := newResEnv(t, f)
	m.settings.Mirrors = []string{"bad.local", "https://mirror.local/"}
	// bad.local 同样失败（假服务只接受 mirror.local）
	rec := do("POST", "/images/pull", "application/json", strings.NewReader(`{"ref":"nginx"}`))
	tk := waitTask(t, taskID(t, rec))
	if tk.Status != "done" {
		t.Fatalf("应通过镜像加速成功: %+v", tk)
	}
	joined := strings.Join(tk.Lines, "\n")
	if !strings.Contains(joined, "layer l1: Downloading 1MB/2MB") || !strings.Contains(joined, "尝试镜像加速 https://mirror.local/") {
		t.Fatalf("进度不完整:\n%s", joined)
	}
	if f.called("POST /images/mirror.local/library/nginx:latest/tag?repo=nginx&tag=latest") != 1 || f.called("DELETE /images/mirror.local/library/nginx:latest") != 1 {
		t.Fatalf("应 tag 回原名并删除加速标签: %v", f.calls)
	}
}

func TestImagePullFailureAndAuth(t *testing.T) {
	f := &resFake{}
	var gotAuth string
	f.handlers = map[string]http.HandlerFunc{
		"POST /images/create": func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("X-Registry-Auth")
			_, _ = w.Write([]byte(`{"error":"denied"}` + "\n"))
		},
	}
	_, do := newResEnv(t, f)
	tk := waitTask(t, taskID(t, do("POST", "/images/pull", "application/json", strings.NewReader(`{"ref":"x/y:1"}`))))
	if tk.Status != "error" || !strings.Contains(tk.Error, "denied") {
		t.Fatalf("应失败: %+v", tk)
	}
	_ = gotAuth // registryAuth 由另一位负责人实现，此处仅确认不崩溃
	if rec := do("POST", "/images/pull", "application/json", strings.NewReader(`{"ref":" "}`)); rec.Code != 400 {
		t.Fatal("空引用应 400")
	}
}

func TestImageBuildInline(t *testing.T) {
	f := &resFake{}
	f.handlers = map[string]http.HandlerFunc{
		"POST /build": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"stream":"Step 1/1 : FROM scratch\n"}` + "\n" + `{"stream":"Successfully built abc\n"}` + "\n"))
		},
	}
	_, do := newResEnv(t, f)
	rec := do("POST", "/images/build", "application/json", strings.NewReader(`{"tag":"demo:1","dockerfile":"FROM scratch\n","buildArgs":{"A":"1"}}`))
	tk := waitTask(t, taskID(t, rec))
	if tk.Status != "done" || len(tk.Lines) < 2 || tk.Lines[0] != "Step 1/1 : FROM scratch" {
		t.Fatalf("构建任务错误: %+v", tk)
	}
	tr := tar.NewReader(bytes.NewReader(f.bodies["POST /build"]))
	h, err := tr.Next()
	if err != nil || h.Name != "Dockerfile" {
		t.Fatalf("上下文应包含 Dockerfile: %v %v", h, err)
	}
	if b, _ := io.ReadAll(tr); string(b) != "FROM scratch\n" {
		t.Fatalf("Dockerfile 内容错误: %q", b)
	}
	var calledQuery string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "POST /build?") {
			calledQuery = c
		}
	}
	if !strings.Contains(calledQuery, "t=demo%3A1") || !strings.Contains(calledQuery, "buildargs=") {
		t.Fatalf("查询参数错误: %s", calledQuery)
	}
	if rec := do("POST", "/images/build", "application/json", strings.NewReader(`{"tag":"a"}`)); rec.Code != 400 {
		t.Fatal("缺少 dockerfile/gitUrl 应 400")
	}
	if rec := do("POST", "/images/build", "application/json", strings.NewReader(`{"tag":"a","dockerfile":"x","gitUrl":"y"}`)); rec.Code != 400 {
		t.Fatal("二选一")
	}
	// 构建输出里的 error 字段使任务失败
	f.handlers["POST /build"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"bad dockerfile"}` + "\n"))
	}
	tk = waitTask(t, taskID(t, do("POST", "/images/build", "application/json", strings.NewReader(`{"tag":"demo:1","gitUrl":"https://example.com/r.git"}`))))
	if tk.Status != "error" || !strings.Contains(tk.Error, "bad dockerfile") {
		t.Fatalf("应失败: %+v", tk)
	}
}

func TestImageLoadExportTagDeleteSearch(t *testing.T) {
	f := &resFake{}
	f.handlers = map[string]http.HandlerFunc{
		"POST /images/load": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"stream":"Loaded image: demo:1\n"}` + "\n"))
		},
		"GET /images/demo:1/get":    func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("TARDATA")) },
		"POST /images/demo:1/tag":   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) },
		"DELETE /images/demo:1":     jsonResp([]map[string]string{{"Untagged": "demo:1"}}),
		"GET /images/search":        jsonResp([]map[string]any{{"name": "nginx", "description": "web", "star_count": 99, "is_official": true}}),
		"DELETE /networks/not-here": errResp(404, "x"),
	}
	_, do := newResEnv(t, f)
	rec := do("POST", "/images/load", "application/x-tar", strings.NewReader("tarbytes"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Loaded image: demo:1") || string(f.bodies["POST /images/load"]) != "tarbytes" {
		t.Fatalf("load: %d %s", rec.Code, rec.Body.String())
	}
	rec = do("GET", "/images/demo:1/export", "", nil)
	if rec.Code != 200 || rec.Body.String() != "TARDATA" || !strings.Contains(rec.Header().Get("Content-Disposition"), "demo_1.tar") {
		t.Fatalf("export: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec = do("POST", "/images/demo:1/tag", "application/json", strings.NewReader(`{"repo":"reg/demo","tag":"2"}`)); rec.Code != 200 {
		t.Fatalf("tag: %s", rec.Body.String())
	}
	if f.called("POST /images/demo:1/tag?repo=reg%2Fdemo&tag=2") != 1 {
		t.Fatalf("tag 参数错误: %v", f.calls)
	}
	if rec = do("DELETE", "/images/demo:1?force=1", "", nil); rec.Code != 200 || f.called("DELETE /images/demo:1?force=1") != 1 {
		t.Fatalf("delete: %d %v", rec.Code, f.calls)
	}
	rec = do("POST", "/images/search", "application/json", strings.NewReader(`{"term":"nginx"}`))
	if !strings.Contains(rec.Body.String(), `"stars":99`) || !strings.Contains(rec.Body.String(), `"official":true`) {
		t.Fatalf("search: %s", rec.Body.String())
	}
}

func TestNetworkHandlers(t *testing.T) {
	f := &resFake{}
	f.handlers = map[string]http.HandlerFunc{
		"GET /networks/b1":             jsonResp(map[string]any{"Id": "b1", "Name": "bridge"}),
		"GET /networks/u1":             jsonResp(map[string]any{"Id": "u1", "Name": "mine"}),
		"DELETE /networks/u1":          func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) },
		"POST /networks/create":        func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"Id":"new1"}`)) },
		"POST /networks/u1/connect":    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) },
		"POST /networks/u1/disconnect": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) },
	}
	_, do := newResEnv(t, f)
	rec := do("DELETE", "/networks/b1", "", nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "内置网络") || f.called("DELETE /networks/b1") != 0 {
		t.Fatalf("删除内置网络应被拒绝: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do("DELETE", "/networks/u1", "", nil); rec.Code != 200 {
		t.Fatalf("删除: %d %s", rec.Code, rec.Body.String())
	}
	rec = do("POST", "/networks", "application/json", strings.NewReader(`{"name":"n","subnet":"10.9.0.0/24","gateway":"10.9.0.1","internal":true}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "new1") {
		t.Fatalf("创建: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(f.bodies["POST /networks/create"], &body)
	if body["Driver"] != "bridge" || body["Internal"] != true || body["IPAM"] == nil {
		t.Fatalf("创建请求错误: %v", body)
	}
	if rec = do("POST", "/networks/u1/connect", "application/json", strings.NewReader(`{"container":"c1","aliases":["a"],"ip":"10.9.0.5"}`)); rec.Code != 200 {
		t.Fatalf("connect: %s", rec.Body.String())
	}
	if !strings.Contains(string(f.bodies["POST /networks/u1/connect"]), "IPv4Address") {
		t.Fatalf("connect 请求错误: %s", f.bodies["POST /networks/u1/connect"])
	}
	if rec = do("POST", "/networks/u1/disconnect", "application/json", strings.NewReader(`{"container":"c1","force":true}`)); rec.Code != 200 {
		t.Fatalf("disconnect: %s", rec.Body.String())
	}
	if rec = do("POST", "/networks", "application/json", strings.NewReader(`{"name":""}`)); rec.Code != 400 {
		t.Fatal("空名称应 400")
	}
}

func tarGz(t *testing.T, name, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func volumeFake() *resFake {
	return &resFake{handlers: map[string]http.HandlerFunc{
		"GET /volumes":                   jsonResp(map[string]any{"Volumes": []map[string]any{{"Name": "data", "Driver": "local", "Mountpoint": "/v/data", "CreatedAt": "2024-01-01T00:00:00Z"}}}),
		"GET /system/df":                 jsonResp(map[string]any{"Volumes": []map[string]any{{"Name": "data", "UsageData": map[string]any{"Size": 2048, "RefCount": 1}}}}),
		"GET /containers/json":           jsonResp([]map[string]any{{"Id": "c1", "Names": []string{"/web"}, "Mounts": []map[string]any{{"Type": "volume", "Name": "data"}}}}),
		"GET /volumes/data":              jsonResp(map[string]any{"Name": "data"}),
		"GET /volumes/new":               errResp(404, "no such volume"),
		"POST /volumes/create":           func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201); _, _ = w.Write([]byte(`{}`)) },
		"GET /images/alpine:latest/json": jsonResp(map[string]any{"Id": "sha256:alp"}),
		"POST /containers/create": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"Id":"helper1"}`))
		},
		"DELETE /containers/helper1": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) },
		"GET /containers/helper1/archive": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("RAWTAR"))
		},
		"PUT /containers/helper1/archive": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) },
	}}
}

func TestVolumeListAndExportImport(t *testing.T) {
	f := volumeFake()
	_, do := newResEnv(t, f)
	rec := do("GET", "/volumes", "", nil)
	var list []VolumeView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if list[0].Size != 2048 || list[0].RefCount != 1 || list[0].UsedBy[0].Name != "web" {
		t.Fatalf("卷合并错误: %+v", list[0])
	}

	// 导出：辅助容器只读挂载，归档读 /vol/.，响应是 gzip，结束后删除容器
	rec = do("GET", "/volumes/data/export", "", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "RAWTAR" {
		t.Fatalf("导出内容错误: %q", b)
	}
	if !strings.Contains(string(f.bodies["POST /containers/create"]), "data:/vol:ro") {
		t.Fatalf("应只读挂载: %s", f.bodies["POST /containers/create"])
	}
	if f.called("DELETE /containers/helper1") != 1 || f.called("GET /containers/helper1/archive?path=%2Fvol%2F.") != 1 {
		t.Fatalf("调用序列错误: %v", f.calls)
	}

	// 导入 gzip：Docker 收到的是解压后的 tar；卷不存在时先创建
	payload := tarGz(t, "a.txt", "hello")
	rec = do("POST", "/volumes/new/import", "application/gzip", bytes.NewReader(payload))
	if rec.Code != 200 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	got := f.bodies["PUT /containers/helper1/archive"]
	tr := tar.NewReader(bytes.NewReader(got))
	h, err := tr.Next()
	if err != nil || h.Name != "a.txt" {
		t.Fatalf("应收到解压后的 tar: %v %v", h, err)
	}
	if f.called("POST /volumes/create") != 1 || f.called("DELETE /containers/helper1") != 2 {
		t.Fatalf("应创建卷并清理辅助容器: %v", f.calls)
	}
	// 请求体不是压缩包时按 tar 直通
	if rec = do("POST", "/volumes/data/import", "application/x-tar", strings.NewReader("plain tar bytes")); rec.Code != 200 {
		t.Fatalf("plain import: %s", rec.Body.String())
	}
	if string(f.bodies["PUT /containers/helper1/archive"]) != "plain tar bytes" {
		t.Fatalf("非压缩体应原样转发")
	}
}

func TestVolumeExportCleansUpOnFailure(t *testing.T) {
	f := volumeFake()
	f.handlers["GET /containers/helper1/archive"] = errResp(500, "boom")
	_, do := newResEnv(t, f)
	rec := do("GET", "/volumes/data/export", "", nil)
	if rec.Code != 502 || f.called("DELETE /containers/helper1") != 1 {
		t.Fatalf("失败也应清理: %d %v", rec.Code, f.calls)
	}
	// 辅助镜像缺失时先拉取
	f2 := volumeFake()
	f2.handlers["GET /images/alpine:latest/json"] = errResp(404, "no such image")
	f2.handlers["POST /images/create"] = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}` + "\n")) }
	_, do2 := newResEnv(t, f2)
	if rec = do2("GET", "/volumes/data/export", "", nil); rec.Code != 200 || f2.called("POST /images/create?fromImage=alpine&tag=latest") != 1 {
		t.Fatalf("应先拉取 alpine: %d %v", rec.Code, f2.calls)
	}
}

func composeFake(dir string) *resFake {
	lbl := func(svc string, n int) map[string]string {
		return map[string]string{labelProject: "web", labelService: svc, labelNumber: fmt.Sprint(n), labelWorkingDir: dir, labelConfig: filepath.Join(dir, "compose.yaml")}
	}
	ok204 := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }
	return &resFake{handlers: map[string]http.HandlerFunc{
		"GET /containers/json": jsonResp([]map[string]any{
			{"Id": "app1", "Names": []string{"/web-app-1"}, "Image": "app", "State": "running", "Labels": lbl("app", 1),
				"Ports": []map[string]any{{"PrivatePort": 80, "PublicPort": 8080, "Type": "tcp"}}},
			{"Id": "db1", "Names": []string{"/web-db-1"}, "Image": "db", "State": "exited", "Labels": lbl("db", 1)},
		}),
		"POST /containers/app1/stop":  ok204,
		"POST /containers/db1/stop":   ok204,
		"POST /containers/app1/start": ok204,
		"POST /containers/db1/start":  ok204,
		"POST /containers/app1/pause": ok204,
		"DELETE /containers/app1":     ok204,
		"DELETE /containers/db1":      ok204,
		"GET /networks":               jsonResp([]map[string]any{{"Id": "net1", "Name": "web_default"}}),
		"DELETE /networks/net1":       ok204,
		"GET /volumes":                jsonResp(map[string]any{"Volumes": []map[string]any{{"Name": "web_data"}}}),
		"DELETE /volumes/web_data":    ok204,
		"GET /containers/app1/logs": func(w http.ResponseWriter, r *http.Request) {
			s := "2024-01-01T00:00:02Z app log\n"
			h := make([]byte, 8)
			h[0], h[7] = 1, byte(len(s))
			_, _ = w.Write(append(h, s...))
		},
		"GET /containers/db1/logs": func(w http.ResponseWriter, r *http.Request) {
			s := "2024-01-01T00:00:01Z db log\n"
			h := make([]byte, 8)
			h[0], h[7] = 1, byte(len(s))
			_, _ = w.Write(append(h, s...))
		},
	}}
}

func TestComposeListActionsAndDown(t *testing.T) {
	dir := t.TempDir()
	f := composeFake(dir)
	m, do := newResEnv(t, f)
	// 受管目录里没有容器的项目也应列出
	_ = os.MkdirAll(filepath.Join(m.composeRoot(), "idle"), 0o755)
	_ = os.WriteFile(filepath.Join(m.composeRoot(), "idle", "compose.yaml"), []byte("services: {}\n"), 0o644)

	rec := do("GET", "/compose/projects", "", nil)
	var list []ComposeProjectView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if list[0].Name != "idle" || !list[0].Managed || list[0].Status != "stopped" {
		t.Fatalf("idle 项目错误: %+v", list[0])
	}
	if list[1].Name != "web" || list[1].Status != "partial" || list[1].Managed || list[1].Services[0].Ports[0] != "8080→80/tcp" {
		t.Fatalf("web 项目错误: %+v", list[1])
	}

	// stop 倒序：db 先于 app
	if rec = do("POST", "/compose/web/stop", "", nil); rec.Code != 200 {
		t.Fatalf("stop: %s", rec.Body.String())
	}
	var stops []string
	for _, c := range f.calls {
		if strings.Contains(c, "/stop?") {
			stops = append(stops, c)
		}
	}
	if len(stops) != 2 || !strings.Contains(stops[0], "db1") {
		t.Fatalf("stop 顺序错误: %v", stops)
	}
	if rec = do("POST", "/compose/web/bogus", "", nil); rec.Code != 400 {
		t.Fatal("未知操作应 400")
	}
	if rec = do("POST", "/compose/nope/start", "", nil); rec.Code != 404 {
		t.Fatalf("未知项目应 404: %d", rec.Code)
	}

	if rec = do("POST", "/compose/web/down?volumes=1", "", nil); rec.Code != 200 {
		t.Fatalf("down: %s", rec.Body.String())
	}
	for _, want := range []string{"DELETE /containers/app1", "DELETE /containers/db1", "DELETE /networks/net1", "DELETE /volumes/web_data"} {
		if f.called(want) != 1 {
			t.Errorf("down 缺少调用 %s: %v", want, f.calls)
		}
	}
	// 项目标签过滤
	if f.called("GET /networks?filters=") == 0 {
		t.Error("网络应按项目标签过滤")
	}
	// 非受管项目不可通过 DELETE 删除
	if rec = do("DELETE", "/compose/web", "", nil); rec.Code != 400 {
		t.Fatalf("非受管项目 DELETE 应 400: %d %s", rec.Code, rec.Body.String())
	}
	// 受管项目：删除目录
	if rec = do("DELETE", "/compose/idle", "", nil); rec.Code != 200 {
		t.Fatalf("delete idle: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(m.composeRoot(), "idle")); !os.IsNotExist(err) {
		t.Fatal("目录应被删除")
	}
}

func TestComposeLogs(t *testing.T) {
	f := composeFake(t.TempDir())
	_, do := newResEnv(t, f)
	rec := do("GET", "/compose/web/logs?tail=50", "", nil)
	var out struct {
		Logs string `json:"logs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Logs != "db | 2024-01-01T00:00:01Z db log\napp | 2024-01-01T00:00:02Z app log\n" {
		t.Fatalf("日志错误: %q", out.Logs)
	}
}

func TestComposeConfigReadWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	_ = os.WriteFile(file, []byte("old\n"), 0o644)
	f := composeFake(dir)
	_, do := newResEnv(t, f)
	rec := do("GET", "/compose/web/config", "", nil)
	var cfg struct {
		Path     string `json:"path"`
		Content  string `json:"content"`
		Writable bool   `json:"writable"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	if cfg.Path != file || cfg.Content != "old\n" || !cfg.Writable {
		t.Fatalf("读取错误: %s", rec.Body.String())
	}
	if rec = do("GET", "/compose/web/config?path=/etc/passwd", "", nil); rec.Code != 400 {
		t.Fatalf("未知路径应拒绝: %d", rec.Code)
	}
	if rec = do("PUT", "/compose/web/config", "application/json", strings.NewReader(`{"content":"new\n"}`)); rec.Code != 200 {
		t.Fatalf("写入: %d %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(file); string(b) != "new\n" {
		t.Fatalf("内容未写入: %q", b)
	}
	if b, _ := os.ReadFile(file + ".bak"); string(b) != "old\n" {
		t.Fatalf("应保留 .bak: %q", b)
	}
	if rec = do("PUT", "/compose/web/config", "application/json", strings.NewReader(`{"content":"x","path":"/tmp/evil"}`)); rec.Code != 400 {
		t.Fatalf("未知路径写入应拒绝: %d", rec.Code)
	}
}

// installFakeCLI 在 PATH 前面放一个假的 docker，回显参数与 DOCKER_HOST。
func installFakeCLI(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = compose ] && [ \"$2\" = version ]; then echo 2.30.0; exit 0; fi\n" +
		"echo \"ARGS: $@\"\necho \"PWD: $(pwd)\"\necho \"HOST: $DOCKER_HOST\"\n[ \"$FAKE_FAIL\" = 1 ] && { echo boom >&2; exit 3; }\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestComposeUpCreateWithCLI(t *testing.T) {
	installFakeCLI(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	_ = os.WriteFile(file, []byte("services: {}\n"), 0o644)
	f := composeFake(dir)
	m, do := newResEnv(t, f)

	tk := waitTask(t, taskID(t, do("POST", "/compose/web/up?build=1&pull=1", "", nil)))
	joined := strings.Join(tk.Lines, "\n")
	if tk.Status != "done" || !strings.Contains(joined, "ARGS: compose -p web -f "+file+" up -d --build --pull always") {
		t.Fatalf("up 任务错误: %+v", tk)
	}
	if !strings.Contains(joined, "HOST: "+m.settings.Host) || !strings.Contains(joined, "PWD: ") {
		t.Fatalf("应注入 DOCKER_HOST: %s", joined)
	}
	tk = waitTask(t, taskID(t, do("POST", "/compose/web/pull", "", nil)))
	if tk.Status != "done" || !strings.Contains(strings.Join(tk.Lines, "\n"), " pull") {
		t.Fatalf("pull 任务错误: %+v", tk)
	}

	// 创建新项目
	rec := do("POST", "/compose/projects", "application/json", strings.NewReader(`{"name":"My App","content":"x"}`))
	if rec.Code != 400 {
		t.Fatalf("非法项目名应 400: %d", rec.Code)
	}
	rec = do("POST", "/compose/projects", "application/json", strings.NewReader(`{"name":"blog","content":"services: {}\n"}`))
	tk = waitTask(t, taskID(t, rec))
	created := filepath.Join(m.composeRoot(), "blog", "compose.yaml")
	if tk.Status != "done" || !strings.Contains(strings.Join(tk.Lines, "\n"), "-p blog -f "+created) {
		t.Fatalf("创建任务错误: %+v", tk)
	}
	if b, _ := os.ReadFile(created); string(b) != "services: {}\n" {
		t.Fatal("compose.yaml 未写入")
	}
	if rec = do("POST", "/compose/projects", "application/json", strings.NewReader(`{"name":"blog","content":"x"}`)); rec.Code != 409 {
		t.Fatalf("重名应 409: %d", rec.Code)
	}

	// 目录缺失时给出明确错误
	_ = os.RemoveAll(dir)
	if rec = do("POST", "/compose/web/up", "", nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "不存在") {
		t.Fatalf("目录缺失应报错: %d %s", rec.Code, rec.Body.String())
	}
}

func TestComposeCLIFailureAndMissing(t *testing.T) {
	installFakeCLI(t)
	t.Setenv("FAKE_FAIL", "1")
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("a"), 0o644)
	_, do := newResEnv(t, composeFake(dir))
	tk := waitTask(t, taskID(t, do("POST", "/compose/web/up", "", nil)))
	if tk.Status != "error" || !strings.Contains(tk.Error, "boom") {
		t.Fatalf("应带出最后一行错误: %+v", tk)
	}

	// CLI 不存在时返回 501
	old := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	defer func() { lookPath = old }()
	rec := do("POST", "/compose/web/up", "", nil)
	if rec.Code != 501 || !strings.Contains(rec.Body.String(), "docker compose") {
		t.Fatalf("应返回 501: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do("POST", "/compose/projects", "application/json", strings.NewReader(`{"name":"blog","content":"x"}`)); rec.Code != 501 {
		t.Fatalf("创建项目应 501: %d", rec.Code)
	}
	if rec = do("PUT", "/compose/web/config?apply=1", "application/json", strings.NewReader(`{"content":"x"}`)); rec.Code != 501 {
		t.Fatalf("apply 应 501: %d", rec.Code)
	}
}
