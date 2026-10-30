package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// ---------- 纯函数 ----------

func TestShellSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		bad  bool
	}{
		{in: `nginx -g 'daemon off;'`, want: []string{"nginx", "-g", "daemon off;"}},
		{in: `a "b c" d\ e`, want: []string{"a", "b c", "d e"}},
		{in: `echo ""`, want: []string{"echo", ""}},
		{in: `sh -c "echo \"hi\" \$HOME"`, want: []string{"sh", "-c", `echo "hi" $HOME`}},
		{in: `'it'\''s'`, want: []string{"it's"}},
		{in: `"a\nb"`, want: []string{`a\nb`}},
		{in: "  \t ", want: nil},
		{in: "a \\\n b", want: []string{"a", "b"}},
		{in: "a\nb", want: []string{"a", "b"}},
		{in: `a'b c'd`, want: []string{"ab cd"}},
		{in: `a 'b`, bad: true},
		{in: `"unclosed`, bad: true},
	}
	for _, c := range cases {
		got, err := shellSplit(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("shellSplit(%q) 应报错", c.in)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("shellSplit(%q)=%q,%v 期望 %q", c.in, got, err, c.want)
		}
	}
	// 拼接后再拆分应还原
	for _, args := range [][]string{{"nginx", "-g", "daemon off;"}, {"echo", ""}, {"it's", `a"b`, "$HOME", "x y"}} {
		back, err := shellSplit(shellJoin(args))
		if err != nil || !reflect.DeepEqual(back, args) {
			t.Errorf("往返失败 %q -> %q -> %q (%v)", args, shellJoin(args), back, err)
		}
	}
	if shellJoin([]string{"nginx", "-g", "daemon off;"}) != `nginx -g 'daemon off;'` {
		t.Errorf("shellJoin 结果不符: %s", shellJoin([]string{"nginx", "-g", "daemon off;"}))
	}
}

// ctrInfo 把普通 map 转成 Inspect 的原始 JSON 形式。
func ctrInfo(t *testing.T, m map[string]any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ctrSampleInfo(t *testing.T) map[string]json.RawMessage {
	id := "abc123def456" + strings.Repeat("0", 52)
	return ctrInfo(t, map[string]any{
		"Id": id, "Name": "/web", "Image": "sha256:img",
		"Config": map[string]any{
			"Image": "nginx:1.27", "Hostname": id[:12],
			"Cmd": []string{"nginx", "-g", "daemon off;"}, "Entrypoint": []string{"/docker-entrypoint.sh"},
			"Env": []string{"PATH=/usr/bin", "A=1"}, "User": "", "WorkingDir": "/app",
			"Labels": map[string]string{"maintainer": "nginx", "env": "prod", "com.docker.compose.project": "blog", "com.docker.compose.service": "web"},
			"Tty":    true, "OpenStdin": false,
			"Healthcheck": map[string]any{"Test": []string{"CMD", "true"}},
		},
		"HostConfig": map[string]any{
			"PortBindings": map[string]any{
				"80/tcp":  []map[string]string{{"HostIp": "", "HostPort": "8080"}},
				"53/udp":  []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "5353"}},
				"443/tcp": []map[string]string{{"HostIp": "", "HostPort": ""}},
			},
			"Binds":         []string{"/data:/data:ro,z", "/etc/tz:/etc/tz"},
			"RestartPolicy": map[string]any{"Name": "on-failure", "MaximumRetryCount": 3},
			"Privileged":    true, "CapAdd": []string{"NET_ADMIN"}, "Dns": []string{"1.1.1.1"},
			"Memory": 268435456 + 123, "NanoCpus": 1500000000, "NetworkMode": "web_default",
			"Devices":   []map[string]string{{"PathOnHost": "/dev/ttyUSB0", "PathInContainer": "/dev/ttyUSB0", "CgroupPermissions": "rwm"}},
			"LogConfig": map[string]any{"Type": "json-file"},
			"Mounts":    []map[string]any{{"Type": "tmpfs", "Target": "/run"}},
		},
		"NetworkSettings": map[string]any{"Networks": map[string]any{
			"web_default": map[string]any{"Aliases": []string{"web", id[:12]}, "IPAMConfig": map[string]any{"IPv4Address": "172.20.0.5"}, "IPAddress": "172.20.0.5", "Gateway": "172.20.0.1", "MacAddress": "02:42"},
			"backend":     map[string]any{"Aliases": []string{"api"}, "IPAddress": "172.30.0.2"},
		}},
		"Mounts": []map[string]any{
			{"Type": "bind", "Source": "/data", "Destination": "/data", "RW": false},
			{"Type": "bind", "Source": "/etc/tz", "Destination": "/etc/tz", "RW": true},
			{"Type": "volume", "Name": "f00d", "Destination": "/var/cache", "RW": true},
		},
		"State": map[string]any{"Running": true, "Status": "running", "StartedAt": "2026-01-02T03:04:05Z", "ExitCode": 0, "Health": map[string]any{"Status": "healthy"}},
	})
}

func TestSpecFromInspect(t *testing.T) {
	info := ctrSampleInfo(t)
	img := &imageDefaults{
		Cmd: strList{"nginx", "-g", "daemon off;"}, Entrypoint: strList{"/docker-entrypoint.sh"},
		Env: []string{"PATH=/usr/bin"}, Labels: map[string]string{"maintainer": "nginx"}, WorkingDir: "/app",
	}
	s := specFromInspect(info, img)
	if s.Name != "web" || s.Image != "nginx:1.27" || s.Hostname != "" {
		t.Errorf("基本字段错误: %+v", s)
	}
	if s.Command != "" || s.Entrypoint != "" || s.Workdir != "" {
		t.Errorf("等于镜像默认值的命令、入口点、目录应留空: %q %q %q", s.Command, s.Entrypoint, s.Workdir)
	}
	if !reflect.DeepEqual(s.Env, []string{"A=1"}) {
		t.Errorf("镜像继承的环境变量应去掉: %v", s.Env)
	}
	if _, ok := s.Labels["maintainer"]; ok || s.Labels["env"] != "prod" {
		t.Errorf("标签处理错误: %v", s.Labels)
	}
	wantPorts := []SpecPort{{Host: 5353, HostIP: "127.0.0.1", Container: 53, Proto: "udp"}, {Host: 8080, Container: 80, Proto: "tcp"}, {Host: 0, Container: 443, Proto: "tcp"}}
	if !reflect.DeepEqual(s.Ports, wantPorts) {
		t.Errorf("端口错误: %+v", s.Ports)
	}
	wantVols := []SpecVolume{{Type: "bind", Source: "/data", Target: "/data", ReadOnly: true}, {Type: "bind", Source: "/etc/tz", Target: "/etc/tz"}, {Type: "volume", Source: "f00d", Target: "/var/cache"}}
	if !reflect.DeepEqual(s.Volumes, wantVols) {
		t.Errorf("挂载错误: %+v", s.Volumes)
	}
	if s.Restart != "on-failure" || s.RestartMaxRetries != 3 || !s.Privileged || !reflect.DeepEqual(s.CapAdd, []string{"NET_ADMIN"}) {
		t.Errorf("运行选项错误: %+v", s)
	}
	if s.MemoryMB != 256 || s.CPUs != 1.5 || !s.TTY || s.StdinOpen {
		t.Errorf("资源或终端选项错误: %+v", s)
	}
	if s.Network != "web_default" || !reflect.DeepEqual(s.Networks, []string{"backend"}) || s.IP != "172.20.0.5" || !reflect.DeepEqual(s.Aliases, []string{"web"}) {
		t.Errorf("网络错误: %q %v %q %v", s.Network, s.Networks, s.IP, s.Aliases)
	}
	if len(s.Devices) != 1 || s.Devices[0].Host != "/dev/ttyUSB0" {
		t.Errorf("设备错误: %+v", s.Devices)
	}

	d := detailFromInspect(info, img, "Up 1 hour")
	if d.State != "running" || d.Status != "Up 1 hour" || d.HealthStatus != "healthy" || d.Started != "2026-01-02T03:04:05Z" ||
		d.ComposeProject != "blog" || d.ComposeService != "web" || len(d.Mounts) != 3 || len(d.NetworkDetails) != 2 || d.NetworkDetails[0].Name != "backend" {
		t.Errorf("详情错误: %+v", d)
	}
	b, _ := json.Marshal(d)
	for _, key := range []string{`"networkDetails"`, `"composeProject"`, `"imageId"`, `"restartMaxRetries"`, `"name":"web"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("详情 JSON 缺少 %s: %s", key, b)
		}
	}
}

// 编辑重建：spec 覆盖已表达的字段，其余（健康检查、日志驱动、tmpfs）原样保留。
func TestApplySpecKeepsUnknownFields(t *testing.T) {
	info := ctrSampleInfo(t)
	img := &imageDefaults{Cmd: strList{"nginx", "-g", "daemon off;"}, Entrypoint: strList{"/docker-entrypoint.sh"}, Env: []string{"PATH=/usr/bin"}}
	spec := specFromInspect(info, img)
	spec.Env = append(spec.Env, "B=2")
	spec.Ports = spec.Ports[:2]
	body, _, err := recreateBody(info)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := applySpec(spec, body, info)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["Healthcheck"] == nil {
		t.Error("健康检查应保留")
	}
	hc := got["HostConfig"].(map[string]any)
	if hc["LogConfig"] == nil {
		t.Error("日志配置应保留")
	}
	if ms, _ := hc["Mounts"].([]any); len(ms) != 1 {
		t.Errorf("tmpfs 挂载应保留: %v", hc["Mounts"])
	}
	binds := hc["Binds"].([]any)
	if len(binds) != 3 || binds[0] != "/data:/data:ro,z" {
		t.Errorf("与旧配置一致的挂载应沿用原条目（含 :z）: %v", binds)
	}
	pb := hc["PortBindings"].(map[string]any)
	if _, ok := pb["443/tcp"]; ok || len(pb) != 2 {
		t.Errorf("端口映射应按 spec 更新: %v", pb)
	}
	if hc["Memory"].(float64) != 268435456+123 || hc["NanoCpus"].(float64) != 1.5e9 {
		t.Errorf("换算后一致的资源限制应沿用精确值: %v %v", hc["Memory"], hc["NanoCpus"])
	}
	if _, ok := got["Cmd"]; ok {
		t.Error("命令留空应使用镜像默认")
	}
	if !reflect.DeepEqual(got["Env"], []any{"A=1", "B=2"}) {
		t.Errorf("环境变量错误: %v", got["Env"])
	}
	eps := got["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)
	ep := eps["web_default"].(map[string]any)
	if len(eps) != 1 || ep["IPAMConfig"].(map[string]any)["IPv4Address"] != "172.20.0.5" {
		t.Errorf("主网络配置错误: %v", eps)
	}
	if len(extra) != 1 || extra[0].name != "backend" || len(extra[0].endpoint["Aliases"].([]string)) != 1 {
		t.Errorf("额外网络应沿用旧端点配置: %+v", extra)
	}
}

func TestApplySpecCreate(t *testing.T) {
	spec := ContainerSpec{
		Image: "alpine", Name: "t1", Command: `sh -c 'sleep 10'`, Env: []string{"X=1"},
		Ports:   []SpecPort{{Host: 8080, Container: 80}, {Container: 53, Proto: "udp", HostIP: "127.0.0.1"}},
		Volumes: []SpecVolume{{Type: "bind", Source: "/d", Target: "/d", ReadOnly: true}, {Type: "volume", Source: "v", Target: "/v"}, {Type: "volume", Target: "/anon"}},
		Network: "web", Networks: []string{"x", "web"}, IP: "10.0.0.5", Aliases: []string{"a"},
		Restart: "unless-stopped", CapAdd: []string{"SYS_TIME"}, MemoryMB: 128, CPUs: 0.5,
		Devices: []SpecDevice{{Host: "/dev/fuse"}},
	}
	body := map[string]any{"HostConfig": map[string]any{}}
	extra, err := applySpec(spec, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	hc := got["HostConfig"].(map[string]any)
	if !reflect.DeepEqual(got["Cmd"], []any{"sh", "-c", "sleep 10"}) {
		t.Errorf("Cmd 错误: %v", got["Cmd"])
	}
	if !reflect.DeepEqual(hc["Binds"], []any{"/d:/d:ro", "v:/v"}) {
		t.Errorf("Binds 错误: %v", hc["Binds"])
	}
	if _, ok := got["Volumes"].(map[string]any)["/anon"]; !ok {
		t.Errorf("匿名卷应写入 Config.Volumes: %v", got["Volumes"])
	}
	pb := hc["PortBindings"].(map[string]any)
	if pb["80/tcp"].([]any)[0].(map[string]any)["HostPort"] != "8080" || pb["53/udp"].([]any)[0].(map[string]any)["HostIp"] != "127.0.0.1" {
		t.Errorf("PortBindings 错误: %v", pb)
	}
	if _, ok := got["ExposedPorts"].(map[string]any)["80/tcp"]; !ok {
		t.Errorf("ExposedPorts 错误: %v", got["ExposedPorts"])
	}
	if hc["Memory"].(float64) != 128<<20 || hc["NanoCpus"].(float64) != 5e8 {
		t.Errorf("资源限制错误: %v %v", hc["Memory"], hc["NanoCpus"])
	}
	if hc["NetworkMode"] != "web" || hc["RestartPolicy"].(map[string]any)["Name"] != "unless-stopped" {
		t.Errorf("网络或重启策略错误: %v", hc)
	}
	dev := hc["Devices"].([]any)[0].(map[string]any)
	if dev["PathInContainer"] != "/dev/fuse" || dev["CgroupPermissions"] != "rwm" {
		t.Errorf("设备默认值错误: %v", dev)
	}
	eps := got["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)
	if len(eps) != 1 || eps["web"].(map[string]any)["Aliases"].([]any)[0] != "a" {
		t.Errorf("EndpointsConfig 错误: %v", eps)
	}
	if len(extra) != 1 || extra[0].name != "x" {
		t.Errorf("额外网络应去掉主网络并保留 x: %+v", extra)
	}
}

func TestApplySpecErrors(t *testing.T) {
	base := ContainerSpec{Image: "alpine"}
	cases := []struct {
		name string
		mod  func(*ContainerSpec)
		want string
	}{
		{"无镜像", func(s *ContainerSpec) { s.Image = " " }, "镜像"},
		{"非法名称", func(s *ContainerSpec) { s.Name = "-bad" }, "名称"},
		{"host 网络不能映射端口", func(s *ContainerSpec) { s.Network = "host"; s.Ports = []SpecPort{{Host: 1, Container: 1}} }, "端口映射"},
		{"none 网络不能设别名", func(s *ContainerSpec) { s.Network = "none"; s.Aliases = []string{"a"} }, "别名"},
		{"host 网络不能加其它网络", func(s *ContainerSpec) { s.Network = "host"; s.Networks = []string{"x"} }, "其它网络"},
		{"container 模式不能映射端口", func(s *ContainerSpec) { s.Network = "container:abc"; s.Ports = []SpecPort{{Container: 80}} }, "端口映射"},
		{"重启策略", func(s *ContainerSpec) { s.Restart = "sometimes" }, "重启策略"},
		{"协议", func(s *ContainerSpec) { s.Ports = []SpecPort{{Container: 80, Proto: "icmp"}} }, "协议"},
		{"端口范围", func(s *ContainerSpec) { s.Ports = []SpecPort{{Container: 70000}} }, "端口"},
		{"端口重复", func(s *ContainerSpec) { s.Ports = []SpecPort{{Host: 80, Container: 80}, {Host: 80, Container: 81}} }, "重复"},
		{"挂载目标相对路径", func(s *ContainerSpec) { s.Volumes = []SpecVolume{{Type: "volume", Source: "v", Target: "rel"}} }, "绝对路径"},
		{"bind 相对源", func(s *ContainerSpec) { s.Volumes = []SpecVolume{{Type: "bind", Source: "rel", Target: "/x"}} }, "绝对路径"},
		{"挂载类型", func(s *ContainerSpec) { s.Volumes = []SpecVolume{{Type: "nfs", Source: "/a", Target: "/x"}} }, "挂载类型"},
		{"命令引号", func(s *ContainerSpec) { s.Command = "a 'b" }, "命令格式"},
		{"固定 IP", func(s *ContainerSpec) { s.IP = "not-ip" }, "IP"},
		{"负内存", func(s *ContainerSpec) { s.MemoryMB = -1 }, "不能为负数"},
	}
	for _, c := range cases {
		s := base
		c.mod(&s)
		_, err := applySpec(s, map[string]any{"HostConfig": map[string]any{}}, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: 期望包含 %q 的错误，得到 %v", c.name, c.want, err)
		}
	}
}

func TestRegistryAuth(t *testing.T) {
	m := newModule(t, "")
	m.settings.Registries = []Registry{
		{Server: "https://index.docker.io/v1/", Username: "hubuser", Password: "hubpass"},
		{Server: "ghcr.io", Username: "gh", Password: "tok"},
		{Server: "registry.local:5000", Username: "u", Password: "p"},
	}
	decode := func(s string) map[string]string {
		b, err := base64.URLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("不是 base64url: %v", err)
		}
		var out map[string]string
		_ = json.Unmarshal(b, &out)
		return out
	}
	if got := decode(m.registryAuth("nginx:1.27")); got["username"] != "hubuser" || got["password"] != "hubpass" || got["serveraddress"] == "" {
		t.Errorf("Docker Hub 认证错误: %v", got)
	}
	if got := decode(m.registryAuth("library/nginx")); got["username"] != "hubuser" {
		t.Errorf("带命名空间的 Hub 镜像应匹配: %v", got)
	}
	if got := decode(m.registryAuth("ghcr.io/org/app:v1")); got["username"] != "gh" || got["serveraddress"] != "ghcr.io" {
		t.Errorf("ghcr 认证错误: %v", got)
	}
	if got := decode(m.registryAuth("registry.local:5000/team/app")); got["username"] != "u" {
		t.Errorf("带端口的仓库认证错误: %v", got)
	}
	if m.registryAuth("quay.io/x/y") != "" || m.registryAuth("localhost:5000/x") != "" {
		t.Error("未配置的仓库不应带认证")
	}
}

func TestRegistryHost(t *testing.T) {
	cases := map[string]string{
		"nginx":                              "docker.io",
		"nginx:1.27":                         "docker.io",
		"library/nginx":                      "docker.io",
		"myuser/app:tag":                     "docker.io",
		"docker.io/library/nginx":            "docker.io",
		"index.docker.io/library/nginx":      "docker.io",
		"ghcr.io/a/b:c":                      "ghcr.io",
		"GHCR.io/a/b":                        "ghcr.io",
		"localhost:5000/x":                   "localhost:5000",
		"localhost/x":                        "localhost",
		"registry.local:5000/app@sha256:abc": "registry.local:5000",
	}
	for in, want := range cases {
		if got := registryHost(in); got != want {
			t.Errorf("registryHost(%q)=%q 期望 %q", in, got, want)
		}
	}
	if normalizeRegistry("https://Registry.Example.com/v2/") != "registry.example.com" {
		t.Error("normalizeRegistry 应去掉协议与路径并转小写")
	}
}

func TestComputeStats(t *testing.T) {
	// cgroup v1：total_inactive_file；在线 CPU 数取自 percpu_usage
	v1 := rawStats{}
	v1.CPUStats.CPUUsage.TotalUsage = 2_000_000
	v1.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 1}
	v1.CPUStats.SystemCPUUsage = 20_000_000
	v1.PreCPUStats.CPUUsage.TotalUsage = 1_000_000
	v1.PreCPUStats.SystemCPUUsage = 10_000_000
	v1.MemoryStats.Usage = 100 << 20
	v1.MemoryStats.Limit = 400 << 20
	v1.MemoryStats.Stats = map[string]uint64{"total_inactive_file": 20 << 20, "cache": 30 << 20}
	v1.Networks = map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	}{"eth0": {RxBytes: 10, TxBytes: 5}, "eth1": {RxBytes: 1, TxBytes: 2}}
	v1.BlkioStats.IOServiceBytesRecursive = []struct {
		Op    string `json:"op"`
		Value uint64 `json:"value"`
	}{{"Read", 100}, {"Write", 50}, {"read", 1}, {"Total", 999}}
	v1.PidsStats.Current = 7
	got := computeStats(v1)
	if got.CPUPercent != 20 {
		t.Errorf("CPU 百分比 = %v，期望 20（增量比 0.1 * 2 核 * 100）", got.CPUPercent)
	}
	if got.MemUsage != 80<<20 || got.MemPercent != 20 || got.MemLimit != 400<<20 {
		t.Errorf("v1 内存应扣除 total_inactive_file: %+v", got)
	}
	if got.NetRx != 11 || got.NetTx != 7 || got.BlockRead != 101 || got.BlockWrite != 50 || got.Pids != 7 {
		t.Errorf("网络、块 IO 或进程数错误: %+v", got)
	}

	// cgroup v2：inactive_file
	v2 := rawStats{}
	v2.CPUStats.OnlineCPUs = 4
	v2.CPUStats.CPUUsage.TotalUsage = 500
	v2.CPUStats.SystemCPUUsage = 1000
	v2.MemoryStats.Usage = 50
	v2.MemoryStats.Limit = 100
	v2.MemoryStats.Stats = map[string]uint64{"inactive_file": 10, "file": 40}
	got = computeStats(v2)
	if got.MemUsage != 40 || got.MemPercent != 40 {
		t.Errorf("v2 内存应扣除 inactive_file: %+v", got)
	}
	// 没有 precpu 增量时 CPU 为 0，且缓存大于用量时不下溢
	v2.MemoryStats.Stats = map[string]uint64{"inactive_file": 500}
	v2.PreCPUStats = v2.CPUStats
	got = computeStats(v2)
	if got.CPUPercent != 0 || got.MemUsage != 50 {
		t.Errorf("边界情况错误: %+v", got)
	}
}

func TestDiskUsageFrom(t *testing.T) {
	var df dfResponse
	raw := `{"LayersSize":1000,"Images":[{"Size":600,"Containers":1},{"Size":500,"Containers":0},{"Size":-1,"Containers":0}],
	"Containers":[{"SizeRw":10,"State":"running"},{"SizeRw":-1,"State":"exited"},{"SizeRw":5,"State":"exited"}],
	"Volumes":[{"UsageData":{"Size":100,"RefCount":1}},{"UsageData":{"Size":-1,"RefCount":0}},{"UsageData":{"Size":50,"RefCount":0}},{"UsageData":null}],
	"BuildCache":[{"Size":30,"InUse":false},{"Size":20,"InUse":true}]}`
	if err := json.Unmarshal([]byte(raw), &df); err != nil {
		t.Fatal(err)
	}
	u := diskUsageFrom(df)
	want := DiskUsage{
		Images:     UsageItem{Count: 3, Size: 1000, Reclaimable: 500},
		Containers: UsageItem{Count: 3, Size: 15, Reclaimable: 5},
		Volumes:    UsageItem{Count: 4, Size: 150, Reclaimable: 50},
		BuildCache: UsageItem{Count: 2, Size: 50, Reclaimable: 30},
	}
	if u != want {
		t.Errorf("磁盘占用 = %+v 期望 %+v", u, want)
	}
}

func TestPruneHelpers(t *testing.T) {
	if p, ok := pruneEndpoint("images", false); !ok || !strings.Contains(p, "dangling") || !strings.Contains(p, "true") {
		t.Errorf("images 默认只清理悬空镜像: %s", p)
	}
	if p, _ := pruneEndpoint("images", true); !strings.Contains(p, "false") {
		t.Errorf("images all=true 应清理全部未使用镜像: %s", p)
	}
	if p, _ := pruneEndpoint("buildcache", true); !strings.Contains(p, "all=1") {
		t.Errorf("buildcache all: %s", p)
	}
	if _, ok := pruneEndpoint("system", false); ok {
		t.Error("未知目标应被拒绝")
	}
	n, space := parsePruneResult(map[string]json.RawMessage{
		"ImagesDeleted": json.RawMessage(`[{"Untagged":"a"},{"Deleted":"b"}]`), "SpaceReclaimed": json.RawMessage(`1234`)})
	if n != 2 || space != 1234 {
		t.Errorf("解析清理结果错误: %d %d", n, space)
	}
	n, _ = parsePruneResult(map[string]json.RawMessage{"ContainersDeleted": json.RawMessage(`null`)})
	if n != 0 {
		t.Errorf("null 应计为 0: %d", n)
	}
}

func TestMergeSettings(t *testing.T) {
	old := Settings{Registries: []Registry{{Server: "ghcr.io", Username: "u", Password: "secret"}, {Server: "docker.io", Username: "h", Password: "hp"}}}
	in := Settings{
		Host:    " ",
		Mirrors: []string{" a.com ", "", "a.com", "b.com"},
		Registries: []Registry{
			{Server: "ghcr.io", Username: "u2", Password: secretMask},
			{Server: "", Username: "skip"},
			{Server: "https://index.docker.io/v1/", Username: "h", Password: secretMask},
			{Server: "GHCR.io", Username: "u3", Password: "new"},
			{Server: "new.io", Username: "n", Password: secretMask},
		},
		AutoCheckHours: -5,
	}
	got := mergeSettings(old, in)
	if !reflect.DeepEqual(got.Mirrors, []string{"a.com", "b.com"}) || got.Host != "" || got.AutoCheckHours != 0 {
		t.Errorf("基本字段错误: %+v", got)
	}
	want := []Registry{{Server: "GHCR.io", Username: "u3", Password: "new"}, {Server: "https://index.docker.io/v1/", Username: "h", Password: "hp"}, {Server: "new.io", Username: "n", Password: ""}}
	if !reflect.DeepEqual(got.Registries, want) {
		t.Errorf("仓库合并错误: %+v", got.Registries)
	}
	masked := maskSettings(got)
	if masked.Registries[0].Password != secretMask || masked.Registries[2].Password != "" {
		t.Errorf("掩码错误: %+v", masked.Registries)
	}
	// 旧版本占位符同样保留原密码
	got = mergeSettings(old, Settings{Registries: []Registry{{Server: "ghcr.io", Username: "u", Password: legacySecretMask}}})
	if got.Registries[0].Password != "secret" {
		t.Errorf("旧占位符应保留原密码: %+v", got.Registries)
	}
}

// TestSettingsSecrets 验证查看仓库密码明文接口按 registry:<仓库地址> 返回明文。
func TestSettingsSecrets(t *testing.T) {
	m := newModule(t, "")
	m.settings.Registries = []Registry{{Server: "ghcr.io", Username: "u", Password: "pw"}}
	mux := http.NewServeMux()
	m.Routes(ctrRouter{mux})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/settings/secrets", nil))
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got["registry:ghcr.io"] != "pw" {
		t.Fatalf("应返回仓库密码明文: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPathSafety(t *testing.T) {
	for _, bad := range []string{"", "relative/path", "a\x00b"} {
		if _, err := cleanContainerPath(bad); err == nil {
			t.Errorf("cleanContainerPath(%q) 应报错", bad)
		}
	}
	for in, want := range map[string]string{"/": "/", "/etc/../root/./x/": "/root/x", "//a///b": "/a/b", "/../..": "/"} {
		if got, err := cleanContainerPath(in); err != nil || got != want {
			t.Errorf("cleanContainerPath(%q)=%q,%v 期望 %q", in, got, err, want)
		}
	}
	tarCases := map[string]struct {
		rel string
		ok  bool
	}{
		"etc/hosts":     {"etc/hosts", true},
		"./etc/":        {"etc", true},
		"/abs/x":        {"abs/x", true},
		"./":            {"", true},
		"../evil":       {"", false},
		"a/../../b":     {"", false},
		"a/b/../c":      {"", false},
		"..":            {"", false},
		"normal/..name": {"normal/..name", true},
	}
	for in, want := range tarCases {
		rel, ok := safeTarRel(in)
		if ok != want.ok || rel != want.rel {
			t.Errorf("safeTarRel(%q)=%q,%v 期望 %q,%v", in, rel, ok, want.rel, want.ok)
		}
	}
	for in, want := range map[string]string{"a.txt": "a.txt", `C:\dir\b.txt`: "b.txt", "../../c": "c", "d/e/f.sh": "f.sh"} {
		if got, err := uploadFileName(in); err != nil || got != want {
			t.Errorf("uploadFileName(%q)=%q,%v 期望 %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/..", "/"} {
		if _, err := uploadFileName(bad); err == nil {
			t.Errorf("uploadFileName(%q) 应报错", bad)
		}
	}
}

func ctrTar(t *testing.T, entries []tar.Header, contents map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		h := h
		body := contents[h.Name]
		h.Size = int64(len(body))
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(body))
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

func TestEntriesFromTar(t *testing.T) {
	data := ctrTar(t, []tar.Header{
		{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "etc/hosts", Mode: 0o644},
		{Name: "etc/sub/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "etc/sub/deep", Mode: 0o644},
		{Name: "etc/link", Typeflag: tar.TypeSymlink, Linkname: "hosts"},
		{Name: "etc/../../evil", Mode: 0o644},
		{Name: "../evil2", Mode: 0o644},
		{Name: "etc/Zeta", Mode: 0o600},
	}, map[string]string{"etc/hosts": "x", "etc/Zeta": "yy"})
	list := entriesFromTar(bytes.NewReader(data), "/etc", "/etc")
	var names []string
	for _, e := range list {
		names = append(names, e.Name)
	}
	if !reflect.DeepEqual(names, []string{"sub", "hosts", "link", "Zeta"}) {
		t.Errorf("条目应只含第一层、目录在前且过滤穿越路径: %v", names)
	}
	if list[1].Path != "/etc/hosts" || list[1].Size != 1 || list[2].Link != "hosts" || !list[0].Dir {
		t.Errorf("条目字段错误: %+v", list)
	}
	// 根目录的 tar 没有顶层目录名
	rootTar := ctrTar(t, []tar.Header{{Name: "bin/", Typeflag: tar.TypeDir}, {Name: "bin/sh"}, {Name: "tmp/", Typeflag: tar.TypeDir}}, nil)
	list = entriesFromTar(bytes.NewReader(rootTar), "/", "/")
	if len(list) != 2 || list[0].Name != "bin" || list[0].Path != "/bin" {
		t.Errorf("根目录列表错误: %+v", list)
	}
}

func TestParseListOutput(t *testing.T) {
	out := "F|directory|4096|755|1700000000|./sub\n" +
		"F|regular file|12|644|1700000100|./a b|c.txt\n" +
		"F|symbolic link|5|777|1700000200|./lnk\n" +
		"F|symbolic link|3|777|1700000300|./dlnk\n" +
		"L|./lnk|a.txt\nL|./dlnk|sub\nD|./dlnk\n" +
		"garbage line\n"
	list := parseListOutput("/srv", []byte(out))
	if len(list) != 4 {
		t.Fatalf("条目数 = %d: %+v", len(list), list)
	}
	// 目录（含指向目录的链接）在前
	if list[0].Name != "dlnk" || !list[0].Dir || list[0].Link != "sub" || list[1].Name != "sub" || !list[1].Dir {
		t.Errorf("排序或链接目录错误: %+v", list[:2])
	}
	f := list[2]
	if f.Name != "a b|c.txt" || f.Dir || f.Size != 12 || f.Mode != "-rw-r--r--" || f.Path != "/srv/a b|c.txt" || f.Modified != "2023-11-14T22:15:00Z" {
		t.Errorf("文件字段错误: %+v", f)
	}
	if list[3].Link != "a.txt" {
		t.Errorf("链接目标错误: %+v", list[3])
	}
}

func TestDemuxSplit(t *testing.T) {
	frame := func(stream byte, s string) []byte {
		h := make([]byte, 8)
		h[0] = stream
		binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
		return append(h, s...)
	}
	data := append(append(frame(1, "out1\n"), frame(2, "err\n")...), frame(1, "out2")...)
	so, se := demuxSplit(data)
	if string(so) != "out1\nout2" || string(se) != "err\n" {
		t.Errorf("拆分错误: %q %q", so, se)
	}
	so, se = demuxSplit([]byte("plain text without frames"))
	if string(so) != "plain text without frames" || se != nil {
		t.Errorf("非多路复用输出应原样作为 stdout: %q %q", so, se)
	}
	so, _ = demuxSplit(append(frame(1, "ok"), 0x01, 0x02))
	if !strings.Contains(string(so), "ok") || len(so) <= 2 {
		t.Errorf("尾部残缺应原样返回: %q", so)
	}
	if so, se := demuxSplit(nil); len(so) != 0 || len(se) != 0 {
		t.Error("空输入应返回空")
	}
}

func TestTrimPartialRune(t *testing.T) {
	full := []byte("你好")
	if got := trimPartialRune(full[:4]); string(got) != "你" {
		t.Errorf("应去掉被截断的字符: %q", got)
	}
	if got := trimPartialRune(full); string(got) != "你好" {
		t.Errorf("完整字符串不应变化: %q", got)
	}
}

func TestComposeCLICache(t *testing.T) {
	calls := 0
	old := composeProbe
	composeProbe = func() (bool, string) {
		calls++
		return true, "2.29.0"
	}
	defer func() { composeProbe = old }()
	m := newModule(t, "")
	for i := 0; i < 3; i++ {
		if ok, v := m.composeCLI(); !ok || v != "2.29.0" {
			t.Fatalf("composeCLI = %v %q", ok, v)
		}
	}
	if calls != 1 {
		t.Errorf("1 分钟内应使用缓存，探测 %d 次", calls)
	}
	m.composeCache.at = time.Now().Add(-2 * time.Minute)
	m.composeCLI()
	if calls != 2 {
		t.Errorf("缓存过期后应重新探测，探测 %d 次", calls)
	}
}

// ---------- 接口（假 Docker 服务） ----------

// ctrFake 是按“方法 路径”匹配的假 Docker Engine（TCP）。
type ctrFake struct {
	mu     sync.Mutex
	calls  []string
	bodies map[string][]byte
	routes map[string]http.HandlerFunc
}

func (f *ctrFake) called(prefix string) int {
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

func (f *ctrFake) body(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func newCtrFake(t *testing.T, routes map[string]http.HandlerFunc) (*ctrFake, string) {
	t.Helper()
	f := &ctrFake{bodies: map[string][]byte{}, routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v"+apiVersion)
		key := r.Method + " " + p
		h, ok := f.routes[key]
		if ok && !strings.HasSuffix(key, "/start") {
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.bodies[key] = b
			f.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		f.mu.Lock()
		f.calls = append(f.calls, key)
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no route ` + key + `"}`))
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, "tcp://" + strings.TrimPrefix(srv.URL, "http://")
}

func ctrJSON(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func ctrStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

type ctrRouter struct{ mux *http.ServeMux }

func (r ctrRouter) HandleFunc(pattern string, h http.HandlerFunc) { r.mux.HandleFunc(pattern, h) }

// ctrAPI 返回已注册全部路由的模块处理器。
func ctrAPI(t *testing.T, host string) (*Module, func(method, path, ctype string, body io.Reader) *httptest.ResponseRecorder) {
	t.Helper()
	m := newModule(t, host)
	mux := http.NewServeMux()
	m.Routes(ctrRouter{mux})
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

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func TestContainerCreateAPI(t *testing.T) {
	newID := strings.Repeat("a", 64)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /images/nginx:1/json":             ctrStatus(404),
		"POST /images/create":                  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}` + "\n")) },
		"POST /containers/create":              ctrJSON(map[string]string{"Id": newID}),
		"POST /networks/extra/connect":         ctrStatus(200),
		"POST /containers/" + newID + "/start": ctrStatus(204),
	})
	_, api := ctrAPI(t, host)
	spec := map[string]any{
		"name": "web", "image": "nginx:1", "pull": true, "autoStart": true, "command": "nginx -g 'daemon off;'",
		"ports": []map[string]any{{"host": 8080, "container": 80, "proto": "tcp"}}, "network": "bridge", "networks": []string{"extra"},
		"restart": "always", "cpus": 1.5, "memoryMB": 64, "unknownFromDetail": "ignored",
	}
	rec := api("POST", "/containers", "application/json", jsonBody(spec))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), newID) {
		t.Fatalf("创建失败: %d %s", rec.Code, rec.Body)
	}
	if f.called("POST /images/create") != 1 || f.called("POST /networks/extra/connect") != 1 || f.called("POST /containers/"+newID+"/start") != 1 {
		t.Errorf("调用序列错误: %v", f.calls)
	}
	var body map[string]any
	_ = json.Unmarshal(f.body("POST /containers/create"), &body)
	hc := body["HostConfig"].(map[string]any)
	if body["Image"] != "nginx:1" || hc["NanoCpus"].(float64) != 1.5e9 || hc["Memory"].(float64) != 64<<20 ||
		hc["PortBindings"].(map[string]any)["80/tcp"].([]any)[0].(map[string]any)["HostPort"] != "8080" {
		t.Errorf("创建请求体错误: %s", f.body("POST /containers/create"))
	}

	// host 网络配端口映射给出明确的 400
	rec = api("POST", "/containers", "application/json", jsonBody(map[string]any{"image": "x", "network": "host", "ports": []map[string]any{{"container": 80}}}))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "端口映射") {
		t.Errorf("host 网络端口映射应返回 400: %d %s", rec.Code, rec.Body)
	}
	if api("POST", "/containers", "text/plain", jsonBody(spec)).Code != 400 {
		t.Error("Content-Type 非 JSON 应拒绝")
	}
}

func TestContainerCreateCleansUpOnStartFailure(t *testing.T) {
	newID := strings.Repeat("b", 64)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"POST /containers/create": ctrJSON(map[string]string{"Id": newID}),
		"POST /containers/" + newID + "/start": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"message":"port is already allocated"}`))
		},
		"DELETE /containers/" + newID: ctrStatus(204),
	})
	_, api := ctrAPI(t, host)
	rec := api("POST", "/containers", "application/json", jsonBody(map[string]any{"image": "x", "autoStart": true}))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "port is already allocated") {
		t.Errorf("应返回启动失败原因: %d %s", rec.Code, rec.Body)
	}
	if f.called("DELETE /containers/"+newID) != 1 {
		t.Error("启动失败后应删除半成品容器")
	}
}

func TestContainerEditAPI(t *testing.T) {
	info := ctrSampleInfo(t)
	var oldID string
	_ = json.Unmarshal(info["Id"], &oldID)
	newID := strings.Repeat("c", 64)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/" + oldID + "/json":    ctrJSON(info),
		"POST /containers/" + oldID + "/stop":   ctrStatus(204),
		"POST /containers/" + oldID + "/rename": ctrStatus(204),
		"POST /containers/create":               ctrJSON(map[string]string{"Id": newID}),
		"POST /networks/backend/connect":        ctrStatus(200),
		"POST /containers/" + newID + "/start":  ctrStatus(204),
		"DELETE /containers/" + oldID:           ctrStatus(204),
	})
	_, api := ctrAPI(t, host)
	spec := specFromInspect(info, nil)
	spec.Env = []string{"ONLY=1"}
	rec := api("POST", "/containers/"+oldID+"/edit", "application/json", jsonBody(spec))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), newID) {
		t.Fatalf("编辑失败: %d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(f.body("POST /containers/create"), &body)
	if !reflect.DeepEqual(body["Env"], []any{"ONLY=1"}) || body["Healthcheck"] == nil {
		t.Errorf("编辑应覆盖环境变量并保留健康检查: %s", f.body("POST /containers/create"))
	}
	seq := strings.Join(f.calls, ";")
	for _, want := range []string{"/stop", "/rename", "containers/create", "networks/backend/connect", "/start", "DELETE /containers/" + oldID} {
		if !strings.Contains(seq, want) {
			t.Errorf("缺少步骤 %s: %s", want, seq)
		}
	}
}

// 按容器名编辑时，旧容器的停止、改名与删除都应使用完整 ID：改名备份后原名归新容器，
// 按名称删除会误删刚创建的新容器。
func TestContainerEditByName(t *testing.T) {
	info := ctrSampleInfo(t)
	var oldID string
	_ = json.Unmarshal(info["Id"], &oldID)
	newID := strings.Repeat("f", 64)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/web/json":              ctrJSON(info),
		"GET /containers/" + oldID + "/json":    ctrJSON(info),
		"POST /containers/" + oldID + "/stop":   ctrStatus(204),
		"POST /containers/" + oldID + "/rename": ctrStatus(204),
		"POST /containers/create":               ctrJSON(map[string]string{"Id": newID}),
		"POST /networks/backend/connect":        ctrStatus(200),
		"POST /containers/" + newID + "/start":  ctrStatus(204),
		"DELETE /containers/" + oldID:           ctrStatus(204),
	})
	_, api := ctrAPI(t, host)
	rec := api("POST", "/containers/web/edit", "application/json", jsonBody(specFromInspect(info, nil)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), newID) {
		t.Fatalf("按名称编辑失败: %d %s", rec.Code, rec.Body)
	}
	if f.called("DELETE /containers/"+oldID) != 1 || f.called("DELETE /containers/web") != 0 {
		t.Errorf("应按完整 ID 删除旧容器，不能按名称删除: %v", f.calls)
	}
	if f.called("DELETE /containers/"+newID) != 0 {
		t.Errorf("新容器不应被删除: %v", f.calls)
	}
}

// 按容器名编辑失败时，恢复旧容器也应使用完整 ID（原名已不再指向旧容器）。
func TestContainerEditByNameRestoresOld(t *testing.T) {
	info := ctrSampleInfo(t)
	var oldID string
	_ = json.Unmarshal(info["Id"], &oldID)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/web/json":              ctrJSON(info),
		"GET /containers/" + oldID + "/json":    ctrJSON(info),
		"POST /containers/" + oldID + "/stop":   ctrStatus(204),
		"POST /containers/" + oldID + "/rename": ctrStatus(204),
		"POST /containers/create":               ctrStatus(500),
		"POST /containers/" + oldID + "/start":  ctrStatus(204),
	})
	_, api := ctrAPI(t, host)
	rec := api("POST", "/containers/web/edit", "application/json", jsonBody(specFromInspect(info, nil)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("创建失败应返回 502: %d %s", rec.Code, rec.Body)
	}
	// 一次改名备份、一次恢复原名，且旧容器被重新启动
	if f.called("POST /containers/"+oldID+"/rename") != 2 || f.called("POST /containers/"+oldID+"/start") != 1 || f.called("POST /containers/web/") != 0 {
		t.Errorf("应按完整 ID 恢复旧容器: %v", f.calls)
	}
}

func TestContainerDetailAndListAPI(t *testing.T) {
	info := ctrSampleInfo(t)
	var id string
	_ = json.Unmarshal(info["Id"], &id)
	_, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/" + id + "/json": ctrJSON(info),
		"GET /images/sha256:img/json": ctrJSON(map[string]any{"Config": map[string]any{
			"Cmd": []string{"nginx", "-g", "daemon off;"}, "Env": []string{"PATH=/usr/bin"}}}),
		"GET /containers/json": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("filters"), id) {
				_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": id, "Status": "Up 2 hours"}})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": id, "Names": []string{"/web"}, "Image": "nginx:1.27", "ImageID": "sha256:img", "State": "running", "Status": "Up",
					"Labels":          map[string]string{"com.docker.compose.project": "blog"},
					"Mounts":          []map[string]any{{"Type": "bind"}, {"Type": "volume"}},
					"NetworkSettings": map[string]any{"Networks": map[string]any{"web_default": map[string]any{}, "backend": map[string]any{}}}},
				{"Id": "stopped1", "Names": []string{"/old"}, "State": "exited", "Status": "Exited"},
			})
		},
	})
	_, api := ctrAPI(t, host)

	rec := api("GET", "/containers/"+id, "", nil)
	if rec.Code != 200 {
		t.Fatalf("详情失败: %d %s", rec.Code, rec.Body)
	}
	var d map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d["status"] != "Up 2 hours" || d["command"] != "" || d["healthStatus"] != "healthy" || d["composeService"] != "web" {
		t.Errorf("详情字段错误: %v", d)
	}
	// 请求上下文中没有身份，按无写权限处理，环境变量值脱敏（明文与鉴权见 container_secrets_test.go）
	if env := d["env"].([]any); len(env) != 1 || env[0] != "A=********" {
		t.Errorf("env 应去掉镜像默认值并脱敏: %v", env)
	}

	rec = api("GET", "/containers", "", nil)
	var list []ContainerView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("列表错误: %v %s", err, rec.Body)
	}
	var web, old ContainerView
	for _, v := range list {
		if v.Name == "web" {
			web = v
		} else {
			old = v
		}
	}
	if web.ImageID != "sha256:img" || web.Mounts != 2 || !reflect.DeepEqual(web.Networks, []string{"backend", "web_default"}) || web.Started != "2026-01-02T03:04:05Z" {
		t.Errorf("新增字段错误: %+v", web)
	}
	if old.Started != "" || old.Networks == nil {
		t.Errorf("已停止容器 started 应为空且 networks 不为 nil: %+v", old)
	}
}

func TestContainerActionsAPI(t *testing.T) {
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/c1/json":    ctrJSON(map[string]string{"Id": "c1"}),
		"POST /containers/c1/pause":  ctrStatus(204),
		"POST /containers/c1/kill":   ctrStatus(204),
		"POST /containers/c1/rename": ctrStatus(204),
		"DELETE /containers/c1":      ctrStatus(204),
		"POST /commit":               ctrJSON(map[string]string{"Id": "sha256:newimg"}),
		"GET /containers/c1/top":     ctrJSON(map[string]any{"Titles": []string{"PID", "CMD"}, "Processes": [][]string{{"1", "sh"}}}),
		"GET /containers/c1/stats":   ctrJSON(map[string]any{"memory_stats": map[string]any{"usage": 100, "limit": 200}, "pids_stats": map[string]any{"current": 3}}),
		"GET /containers/c1/logs":    func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("tty log")) },
	})
	_, api := ctrAPI(t, host)
	for _, a := range []string{"pause", "kill"} {
		if rec := api("POST", "/containers/c1/"+a, "application/json", nil); rec.Code != 200 {
			t.Errorf("%s 失败: %d %s", a, rec.Code, rec.Body)
		}
	}
	if api("POST", "/containers/c1/explode", "application/json", nil).Code != 400 {
		t.Error("未知动作应返回 400")
	}
	if rec := api("DELETE", "/containers/c1?volumes=1", "", nil); rec.Code != 200 {
		t.Errorf("删除失败: %d %s", rec.Code, rec.Body)
	}
	if rec := api("POST", "/containers/c1/rename", "application/json", jsonBody(map[string]string{"name": "bad name"})); rec.Code != 400 {
		t.Errorf("非法名称应返回 400: %d", rec.Code)
	}
	if rec := api("POST", "/containers/c1/rename", "application/json", jsonBody(map[string]string{"name": "new-name"})); rec.Code != 200 {
		t.Errorf("重命名失败: %d %s", rec.Code, rec.Body)
	}
	rec := api("POST", "/containers/c1/commit", "application/json", jsonBody(map[string]any{"repo": "me/app", "tag": "v1", "pause": false}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "sha256:newimg") {
		t.Errorf("提交失败: %d %s", rec.Code, rec.Body)
	}
	rec = api("GET", "/containers/c1/processes", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"titles":["PID","CMD"]`) {
		t.Errorf("进程错误: %d %s", rec.Code, rec.Body)
	}
	rec = api("GET", "/containers/c1/stats", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"memPercent":50`) || !strings.Contains(rec.Body.String(), `"pids":3`) {
		t.Errorf("统计错误: %d %s", rec.Code, rec.Body)
	}
	if rec := api("GET", "/containers/c1/logs?timestamps=0", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "tty log") {
		t.Errorf("日志错误: %d %s", rec.Code, rec.Body)
	}
	seq := strings.Join(f.calls, ";")
	if !strings.Contains(seq, "DELETE /containers/c1") {
		t.Errorf("缺少删除调用: %s", seq)
	}
}

func TestSettingsAPI(t *testing.T) {
	_, host := newCtrFake(t, nil)
	m, api := ctrAPI(t, host)
	put := func(v any) *httptest.ResponseRecorder {
		return api("PUT", "/settings", "application/json", jsonBody(v))
	}
	rec := put(map[string]any{"host": host, "mirrors": []string{"m1", "m1"}, "autoCheckHours": 0,
		"registries": []map[string]string{{"server": "ghcr.io", "username": "u", "password": "p1"}}})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "p1") {
		t.Fatalf("保存失败或泄露密码: %d %s", rec.Code, rec.Body)
	}
	rec = api("GET", "/settings", "", nil)
	if !strings.Contains(rec.Body.String(), `"password":"`+secretMask+`"`) || strings.Contains(rec.Body.String(), "p1") {
		t.Errorf("GET 应掩码密码: %s", rec.Body)
	}
	rec = put(map[string]any{"host": host, "mirrors": []string{"m1"}, "registries": []map[string]string{{"server": "ghcr.io", "username": "u2", "password": secretMask}}})
	if rec.Code != 200 {
		t.Fatalf("保存失败: %s", rec.Body)
	}
	m.mu.Lock()
	got := m.settings.Registries[0]
	m.mu.Unlock()
	if got.Password != "p1" || got.Username != "u2" || len(m.settings.Mirrors) != 1 {
		t.Errorf("掩码密码应保留旧值: %+v", got)
	}
	if put(map[string]any{"host": "ftp://x"}).Code != 400 {
		t.Error("非法地址应拒绝")
	}
}

func TestInfoDiskPruneAPI(t *testing.T) {
	old := composeProbe
	composeProbe = func() (bool, string) { return true, "2.30.1" }
	defer func() { composeProbe = old }()
	_, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /info": ctrJSON(map[string]any{"ServerVersion": "27.0.1", "OSType": "linux", "Architecture": "x86_64", "KernelVersion": "6.1", "Name": "nas",
			"NCPU": 4, "MemTotal": 8 << 30, "Driver": "overlay2", "DockerRootDir": "/var/lib/docker", "Containers": 5, "ContainersRunning": 3,
			"ContainersPaused": 1, "ContainersStopped": 1, "Images": 9,
			"RegistryConfig": map[string]any{"Mirrors": []string{"https://mirror.example.com/"}}}),
		"GET /version":       ctrJSON(map[string]string{"ApiVersion": "1.46"}),
		"GET /system/df":     ctrJSON(map[string]any{"LayersSize": 10, "Images": []map[string]any{{"Size": 10, "Containers": 0}}}),
		"POST /images/prune": ctrJSON(map[string]any{"ImagesDeleted": []map[string]string{{"Deleted": "a"}}, "SpaceReclaimed": 4096}),
	})
	_, api := ctrAPI(t, host)
	rec := api("GET", "/info", "", nil)
	var info map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	cs := info["containers"].(map[string]any)
	if info["version"] != "27.0.1" || info["apiVersion"] != "1.46" || info["composeCli"] != true || info["composeCliVersion"] != "2.30.1" ||
		cs["running"].(float64) != 3 || cs["stopped"].(float64) != 1 || info["storageDriver"] != "overlay2" || info["cpus"].(float64) != 4 {
		t.Errorf("info 错误: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"daemonMirrors":["https://mirror.example.com/"]`) {
		t.Errorf("info 缺少守护进程镜像加速: %s", rec.Body)
	}
	rec = api("GET", "/disk-usage", "", nil)
	if !strings.Contains(rec.Body.String(), `"images":{"count":1,"size":10,"reclaimable":10}`) {
		t.Errorf("disk-usage 错误: %s", rec.Body)
	}
	rec = api("POST", "/prune", "application/json", jsonBody(map[string]any{"target": "images"}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":1`) || !strings.Contains(rec.Body.String(), `"spaceReclaimed":4096`) {
		t.Errorf("prune 错误: %d %s", rec.Code, rec.Body)
	}
	if api("POST", "/prune", "application/json", jsonBody(map[string]any{"target": "everything"})).Code != 400 {
		t.Error("未知清理目标应返回 400")
	}
}

func TestCheckAllAndBatchUpgradeTasks(t *testing.T) {
	info := ctrSampleInfo(t)
	var id string
	_ = json.Unmarshal(info["Id"], &id)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/json": ctrJSON([]map[string]any{
			{"Id": id, "Names": []string{"/web"}, "Image": "nginx:1.27", "State": "running"},
			{"Id": "off", "Names": []string{"/off"}, "Image": "x", "State": "exited"},
		}),
		"GET /containers/" + id + "/json": ctrJSON(info),
		"POST /images/create":             func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}` + "\n")) },
		"GET /images/nginx:1.27/json":     ctrJSON(map[string]string{"Id": "sha256:newer"}),
	})
	m, api := ctrAPI(t, host)
	for _, path := range []string{"/containers/check-all", "/images/upgrade-check"} {
		rec := api("POST", path, "application/json", nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		task := ctrWaitTask(t, out["task"])
		if task.Status != "done" || len(task.Lines) < 2 {
			t.Fatalf("任务结果错误: %+v", task)
		}
	}
	m.mu.Lock()
	u, ok := m.updates[id]
	_, offOK := m.updates["off"]
	m.mu.Unlock()
	if !ok || !u.HasUpdate || offOK {
		t.Errorf("应只记录运行中容器的更新结果: %+v %v", u, offOK)
	}
	if f.called("POST /images/create") != 2 {
		t.Errorf("每轮检查应只拉取一次: %v", f.calls)
	}

	// 批量升级：未知容器记为失败，任务以 error 结束
	rec := api("POST", "/containers/upgrade-batch", "application/json", jsonBody(map[string]any{"ids": []string{"missing"}}))
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	task := ctrWaitTask(t, out["task"])
	if task.Status != "error" || !strings.Contains(task.Error, "1 个容器升级失败") {
		t.Errorf("批量升级失败应体现在任务状态: %+v", task)
	}
	if api("POST", "/containers/upgrade-batch", "application/json", jsonBody(map[string]any{"ids": []string{}})).Code != 400 {
		t.Error("空 ids 应返回 400")
	}
}

func ctrWaitTask(t *testing.T, id string) Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := tasks.snapshot(id); ok && task.Status != "running" {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务 %s 未结束", id)
	return Task{}
}

func TestAutoCheckStartStop(t *testing.T) {
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/json": ctrJSON([]map[string]any{}),
	})
	old := autoCheckUnit
	autoCheckUnit = 20 * time.Millisecond
	defer func() { autoCheckUnit = old }()
	m := newModule(t, host)
	m.settings.AutoCheckHours = 1
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.called("GET /containers/json") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.called("GET /containers/json") == 0 {
		t.Fatal("定时检查应触发")
	}
	_ = m.Stop()
	n := f.called("GET /containers/json")
	time.Sleep(100 * time.Millisecond)
	if f.called("GET /containers/json") != n {
		t.Error("Stop 之后不应再检查")
	}
}

// ---------- 容器文件 ----------

func ctrStatHeader(t *testing.T, name string, size int64, mode os.FileMode, link string) string {
	b, _ := json.Marshal(map[string]any{"name": name, "size": size, "mode": uint32(mode), "mtime": "2026-01-01T00:00:00Z", "linkTarget": link})
	return base64.StdEncoding.EncodeToString(b)
}

// ctrFilesFake 模拟一个已停止的容器：/etc 目录、/etc/hosts 文件、/etc/alias 指向 hosts 的链接。
func ctrFilesFake(t *testing.T) (*ctrFake, string) {
	var putTar []byte
	var mu sync.Mutex
	hosts := "127.0.0.1 localhost\n"
	var f *ctrFake
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/c1/json": ctrJSON(map[string]any{"Id": "c1", "State": map[string]any{"Running": false}}),
		"HEAD /containers/c1/archive": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("path") {
			case "/etc":
				w.Header().Set("X-Docker-Container-Path-Stat", ctrStatHeader(t, "etc", 4096, os.ModeDir|0o755, ""))
			case "/etc/hosts":
				w.Header().Set("X-Docker-Container-Path-Stat", ctrStatHeader(t, "hosts", int64(len(hosts)), 0o640, ""))
			case "/etc/alias":
				w.Header().Set("X-Docker-Container-Path-Stat", ctrStatHeader(t, "alias", 5, os.ModeSymlink|0o777, "hosts"))
			case "/bin.dat":
				w.Header().Set("X-Docker-Container-Path-Stat", ctrStatHeader(t, "bin.dat", 3, 0o644, ""))
			default:
				w.WriteHeader(404)
			}
		},
		"GET /containers/c1/archive": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("path") {
			case "/etc":
				_, _ = w.Write(ctrTar(t, []tar.Header{{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755}, {Name: "etc/hosts", Mode: 0o644}, {Name: "etc/sub/", Typeflag: tar.TypeDir}, {Name: "etc/sub/x"}}, map[string]string{"etc/hosts": hosts}))
			case "/etc/hosts":
				_, _ = w.Write(ctrTar(t, []tar.Header{{Name: "hosts", Mode: 0o640, Uid: 1000, Gid: 1001}}, map[string]string{"hosts": hosts}))
			case "/bin.dat":
				_, _ = w.Write(ctrTar(t, []tar.Header{{Name: "bin.dat", Mode: 0o644}}, map[string]string{"bin.dat": "a\x00b"}))
			default:
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"message":"no such path"}`))
			}
		},
		"PUT /containers/c1/archive": func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			putTar = append([]byte{}, b...)
			mu.Unlock()
			f.mu.Lock()
			f.bodies["PUT path"] = []byte(r.URL.Query().Get("path"))
			f.mu.Unlock()
			w.WriteHeader(200)
		},
	})
	_ = putTar
	t.Cleanup(func() {})
	return f, host
}

func TestContainerFilesAPI(t *testing.T) {
	f, host := ctrFilesFake(t)
	_, api := ctrAPI(t, host)

	// 容器未运行：通过归档接口只列第一层，目录在前
	rec := api("GET", "/containers/c1/files/list?path=/etc", "", nil)
	var list []FileEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || len(list) != 2 || list[0].Name != "sub" || !list[0].Dir || list[1].Path != "/etc/hosts" {
		t.Fatalf("列表错误: %d %s", rec.Code, rec.Body)
	}
	if api("GET", "/containers/c1/files/list?path=relative", "", nil).Code != 400 {
		t.Error("相对路径应拒绝")
	}
	if api("GET", "/containers/c1/files/list?path=/nope", "", nil).Code != 404 {
		t.Error("不存在的目录应返回 404")
	}

	// 读取（含符号链接跟随）
	rec = api("GET", "/containers/c1/files/read?path=/etc/alias", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"content":"127.0.0.1 localhost\n"`) || !strings.Contains(rec.Body.String(), `"truncated":false`) {
		t.Errorf("读取错误: %d %s", rec.Code, rec.Body)
	}
	if rec = api("GET", "/containers/c1/files/read?path=/bin.dat", "", nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "二进制") {
		t.Errorf("二进制文件应返回 400: %d %s", rec.Code, rec.Body)
	}
	if api("GET", "/containers/c1/files/read?path=/etc", "", nil).Code != 400 {
		t.Error("读取目录应返回 400")
	}

	// 下载：文件直出，目录打包为 tar.gz
	rec = api("GET", "/containers/c1/files/download?path=/etc/hosts", "", nil)
	if rec.Code != 200 || rec.Body.String() != "127.0.0.1 localhost\n" || !strings.Contains(rec.Header().Get("Content-Disposition"), "hosts") {
		t.Errorf("文件下载错误: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	rec = api("GET", "/containers/c1/files/download?path=/etc", "", nil)
	zr, err := gzip.NewReader(rec.Body)
	if err != nil || !strings.Contains(rec.Header().Get("Content-Disposition"), "etc.tar.gz") {
		t.Fatalf("目录下载应为 gzip: %v %v", err, rec.Header())
	}
	hdr, _ := tar.NewReader(zr).Next()
	if hdr == nil || hdr.Name != "etc/" {
		t.Errorf("tar.gz 内容错误: %+v", hdr)
	}

	// 写入：保留已有文件权限与属主
	rec = api("POST", "/containers/c1/files/write", "application/json", jsonBody(map[string]string{"path": "/etc/hosts", "content": "new content"}))
	if rec.Code != 200 {
		t.Fatalf("写入失败: %d %s", rec.Code, rec.Body)
	}
	if string(f.body("PUT path")) != "/etc" {
		t.Errorf("应解压到父目录: %s", f.body("PUT path"))
	}
	var gotTar []byte
	gotTar = f.body("PUT /containers/c1/archive")
	tr := tar.NewReader(bytes.NewReader(gotTar))
	h, err := tr.Next()
	if err != nil || h.Name != "hosts" || h.Mode != 0o640 || h.Uid != 1000 || h.Gid != 1001 {
		t.Fatalf("写入的 tar 头错误: %+v %v", h, err)
	}
	if c, _ := io.ReadAll(tr); string(c) != "new content" {
		t.Errorf("写入内容错误: %q", c)
	}
	// 新文件默认 0644
	api("POST", "/containers/c1/files/write", "application/json", jsonBody(map[string]string{"path": "/etc/new.conf", "content": "x"}))
	h, _ = tar.NewReader(bytes.NewReader(f.body("PUT /containers/c1/archive"))).Next()
	if h == nil || h.Name != "new.conf" || h.Mode != 0o644 || h.Uid != 0 {
		t.Errorf("新文件头错误: %+v", h)
	}

	// 新建目录
	if rec = api("POST", "/containers/c1/files/mkdir", "application/json", jsonBody(map[string]string{"path": "/etc/hosts"})); rec.Code != 409 {
		t.Errorf("已存在应返回 409: %d", rec.Code)
	}
	rec = api("POST", "/containers/c1/files/mkdir", "application/json", jsonBody(map[string]string{"path": "/etc/newdir"}))
	h, _ = tar.NewReader(bytes.NewReader(f.body("PUT /containers/c1/archive"))).Next()
	if rec.Code != 200 || h == nil || h.Typeflag != tar.TypeDir || h.Name != "newdir/" {
		t.Errorf("mkdir 错误: %d %+v", rec.Code, h)
	}

	// 上传（含路径穿越的文件名只取最后一段）
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("path", "/etc")
	fw, _ := mw.CreateFormFile("file", "../../evil.txt")
	_, _ = fw.Write([]byte("uploaded"))
	_ = mw.Close()
	rec = api("POST", "/containers/c1/files/upload", mw.FormDataContentType(), &mp)
	h, _ = tar.NewReader(bytes.NewReader(f.body("PUT /containers/c1/archive"))).Next()
	if rec.Code != 200 || h == nil || h.Name != "evil.txt" || h.Size != 8 {
		t.Errorf("上传错误: %d %s %+v", rec.Code, rec.Body, h)
	}

	// 重命名与删除需要运行中的容器
	if rec = api("POST", "/containers/c1/files/rename", "application/json", jsonBody(map[string]string{"from": "/a", "to": "/b"})); rec.Code != 409 || !strings.Contains(rec.Body.String(), "未运行") {
		t.Errorf("未运行时重命名应返回 409: %d %s", rec.Code, rec.Body)
	}
	if rec = api("DELETE", "/containers/c1/files?path=/etc/hosts", "", nil); rec.Code != 409 {
		t.Errorf("未运行时删除应返回 409: %d", rec.Code)
	}
	if api("DELETE", "/containers/c1/files?path=/", "", nil).Code != 400 {
		t.Error("不能删除根目录")
	}
}

// ---------- exec 与终端 ----------

// ctrExecFake 模拟运行中的容器：exec 创建、劫持式启动（把输入转大写回显）、调整大小与非交互 exec。
func ctrExecFake(t *testing.T) (*ctrFake, string) {
	frame := func(stream byte, s string) []byte {
		h := make([]byte, 8)
		h[0] = stream
		binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
		return append(h, s...)
	}
	return newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/c1/json": ctrJSON(map[string]any{"Id": "c1", "Name": "/c1", "State": map[string]any{"Running": true}}),
		"POST /containers/c1/exec": func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			id := "e-tty"
			if req["Tty"] == false {
				id = "e-plain"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": id})
		},
		"POST /exec/e-tty/start": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Upgrade") != "tcp" {
				w.WriteHeader(400)
				return
			}
			conn, brw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			// 先丢弃请求体（JSON），之后才是终端数据
			_, _ = io.CopyN(io.Discard, brw, r.ContentLength)
			_, _ = brw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = brw.Flush()
			buf := make([]byte, 1024)
			for {
				n, err := brw.Read(buf)
				if n > 0 {
					_, _ = conn.Write(bytes.ToUpper(buf[:n]))
				}
				if err != nil {
					return
				}
			}
		},
		"POST /exec/e-tty/resize": ctrStatus(200),
		"POST /exec/e-plain/start": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(append(frame(1, "out"), frame(2, "err")...))
		},
		"GET /exec/e-plain/json": ctrJSON(map[string]any{"Running": false, "ExitCode": 3}),
	})
}

func TestExecRunAndHijack(t *testing.T) {
	_, host := ctrExecFake(t)
	c, err := NewClient(host)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	so, se, code, err := c.execRun(ctx, "c1", []string{"sh", "-c", "x"}, "")
	if err != nil || string(so) != "out" || string(se) != "err" || code != 3 {
		t.Errorf("execRun = %q %q %d %v", so, se, code, err)
	}
	if _, err := c.execOK(ctx, "c1", []string{"false"}); err == nil || !strings.Contains(err.Error(), "err") {
		t.Errorf("退出码非 0 应把 stderr 作为错误: %v", err)
	}

	conn, br, err := c.hijack(ctx, http.MethodPost, "/exec/e-tty/start", map[string]any{"Detach": false, "Tty": true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("hello"))
	buf := make([]byte, 5)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "HELLO" {
		t.Errorf("劫持连接回显错误: %q %v", buf, err)
	}
	if _, _, err := c.hijack(ctx, http.MethodPost, "/exec/none/start", nil); err == nil {
		t.Error("握手失败应返回错误")
	}
}

func TestExecWebSocket(t *testing.T) {
	f, host := ctrExecFake(t)
	m := newModule(t, host)
	mux := http.NewServeMux()
	m.Routes(ctrRouter{mux})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/containers/c1/exec?cmd=/bin/sh&cols=100&rows=30", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"t":"i","d":"ls\n"}`))
	typ, data, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "LS\n" {
		t.Fatalf("终端输出错误: %v %q %v", typ, data, err)
	}
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"t":"r","c":120,"r":40}`))
	deadline := time.Now().Add(3 * time.Second)
	for f.called("POST /exec/e-tty/resize") < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.called("POST /exec/e-tty/resize") < 2 {
		t.Errorf("应在启动与收到 resize 消息时各调整一次大小: %v", f.calls)
	}
	_ = ws.Close(websocket.StatusNormalClosure, "")
}

func TestExecWebSocketRejectsStopped(t *testing.T) {
	_, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/c1/json": ctrJSON(map[string]any{"Id": "c1", "Name": "/c1", "State": map[string]any{"Running": false}}),
	})
	m := newModule(t, host)
	mux := http.NewServeMux()
	m.Routes(ctrRouter{mux})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/containers/c1/exec", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	typ, data, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageText || !strings.Contains(string(data), `"d":"容器未运行`) {
		t.Errorf("容器未运行应发送文本提示: %v %q %v", typ, data, err)
	}
}
