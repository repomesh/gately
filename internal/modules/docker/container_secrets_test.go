package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/wangwanjie/gately/internal/apitoken"
	"github.com/wangwanjie/gately/internal/auth"
)

// secretInfo 返回带有密钥环境变量与启动参数的容器 inspect 结果。
func secretInfo(t *testing.T, id string) map[string]json.RawMessage {
	t.Helper()
	return ctrInfo(t, map[string]any{
		"Id": id, "Name": "/db", "Image": "sha256:pg", "Path": "/entry.sh", "Args": []string{"postgres", "--password=s3cret"},
		"Config": map[string]any{
			"Image": "postgres:16", "Cmd": []string{"postgres", "--password=s3cret"}, "Entrypoint": []string{"/entry.sh", "--token=t0k"},
			"Env": []string{"PATH=/usr/bin", "DB_PASSWORD=s3cret", "EMPTY=", "FLAG"},
		},
		"State": map[string]any{"Status": "running"},
	})
}

// tokenRequester 返回以 API 令牌权限 scopes 访问模块路由的请求函数，scopes 为 nil 表示请求不带身份。
func tokenRequester(t *testing.T, host string) func(scopes []string, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	m := newModule(t, host)
	mux := http.NewServeMux()
	m.Routes(ctrRouter{mux})
	return func(scopes []string, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if scopes != nil {
			req = req.WithContext(auth.WithScopeCheck(context.Background(), func(need string) bool {
				return apitoken.Match(scopes, need)
			}))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
}

func TestContainerDetailMasksSecretsWithoutWrite(t *testing.T) {
	id := strings.Repeat("d", 64)
	_, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/" + id + "/json": ctrJSON(secretInfo(t, id)),
		"GET /images/sha256:pg/json":      ctrJSON(map[string]any{"Config": map[string]any{"Env": []string{"PATH=/usr/bin"}}}),
		"GET /containers/json":            ctrJSON([]map[string]any{}),
		"GET /containers/" + id + "/top": ctrJSON(map[string]any{
			"Titles":    []string{"UID", "PID", "CMD"},
			"Processes": [][]string{{"999", "1", "postgres --password=s3cret"}, {"999", "20", "postgres"}},
		}),
	})
	call := tokenRequester(t, host)
	detail := func(scopes []string) ContainerDetail {
		t.Helper()
		rec := call(scopes, "GET", "/containers/"+id)
		if rec.Code != 200 {
			t.Fatalf("详情失败: %d %s", rec.Code, rec.Body)
		}
		var d ContainerDetail
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		return d
	}

	// 只读令牌与无身份请求：值、命令与参数全部脱敏，变量名与可执行文件保留
	for _, scopes := range [][]string{{"docker:read"}, nil} {
		d := detail(scopes)
		if !reflect.DeepEqual(d.Env, []string{"DB_PASSWORD=********", "EMPTY=", "FLAG"}) {
			t.Errorf("%v: env 应脱敏: %v", scopes, d.Env)
		}
		if d.Command != "********" || d.Entrypoint != "********" || !reflect.DeepEqual(d.Args, []string{"********"}) || d.Path != "/entry.sh" {
			t.Errorf("%v: 命令应脱敏: %q %q %v %q", scopes, d.Command, d.Entrypoint, d.Args, d.Path)
		}
		if body := call(scopes, "GET", "/containers/"+id+"/processes").Body.String(); strings.Contains(body, "s3cret") || !strings.Contains(body, `"postgres ********"`) || !strings.Contains(body, `"postgres"`) {
			t.Errorf("%v: 进程命令行参数应脱敏: %s", scopes, body)
		}
	}

	// 写权限令牌直接看到明文
	d := detail([]string{"docker:write"})
	if !reflect.DeepEqual(d.Env, []string{"DB_PASSWORD=s3cret", "EMPTY=", "FLAG"}) || d.Command != "postgres --password=s3cret" || d.Entrypoint != "/entry.sh --token=t0k" {
		t.Errorf("写权限应返回明文: %+v", d)
	}
	if body := call([]string{"docker:write"}, "GET", "/containers/"+id+"/processes").Body.String(); !strings.Contains(body, "--password=s3cret") {
		t.Errorf("写权限应看到完整进程命令行: %s", body)
	}

	// 明文接口返回与详情对应的字段
	rec := call([]string{"docker:write"}, "GET", "/containers/"+id+"/secrets")
	var sec ContainerSecrets
	if err := json.Unmarshal(rec.Body.Bytes(), &sec); err != nil || rec.Code != 200 {
		t.Fatalf("明文接口失败: %d %s", rec.Code, rec.Body)
	}
	want := ContainerSecrets{Env: []string{"DB_PASSWORD=s3cret", "EMPTY=", "FLAG"}, Command: "postgres --password=s3cret", Entrypoint: "/entry.sh --token=t0k", Args: []string{"postgres", "--password=s3cret"}}
	if !reflect.DeepEqual(sec, want) {
		t.Errorf("明文接口内容错误: %+v", sec)
	}
}

func TestContainerSecretsScope(t *testing.T) {
	// 明文接口按写权限鉴权，只读令牌与只读账号（roleGuard 同样依据 Required）都会被拒绝
	if got := apitoken.Required("GET", "/api/docker/containers/abc/secrets"); got != "docker:write" {
		t.Errorf("明文接口应需要 docker:write，得到 %s", got)
	}
	if apitoken.Match([]string{"docker:read"}, apitoken.Required("GET", "/api/docker/containers/abc/secrets")) {
		t.Error("只读令牌不应能访问明文接口")
	}
}

func TestRestoreMaskedSpec(t *testing.T) {
	info := secretInfo(t, strings.Repeat("e", 64))
	spec := ContainerSpec{
		Env:        []string{"DB_PASSWORD=********", "NEW=1", "GONE=********", "EMPTY="},
		Command:    "********",
		Entrypoint: "********",
	}
	restoreMaskedSpec(&spec, info)
	if !reflect.DeepEqual(spec.Env, []string{"DB_PASSWORD=s3cret", "NEW=1", "GONE=********", "EMPTY="}) {
		t.Errorf("占位符应恢复为旧值、旧容器没有的变量保留原样: %v", spec.Env)
	}
	if spec.Command != "postgres --password=s3cret" || spec.Entrypoint != "/entry.sh --token=t0k" {
		t.Errorf("命令占位符应恢复为旧值: %q %q", spec.Command, spec.Entrypoint)
	}

	// 修改过的值不受影响
	spec = ContainerSpec{Env: []string{"DB_PASSWORD=changed"}, Command: "postgres"}
	restoreMaskedSpec(&spec, info)
	if spec.Env[0] != "DB_PASSWORD=changed" || spec.Command != "postgres" {
		t.Errorf("修改过的值不应被恢复: %+v", spec)
	}
}

func TestMaskProcessCommandsWithoutCmdColumn(t *testing.T) {
	procs := [][]string{{"1", "sh -c x"}}
	maskProcessCommands([]string{"PID", "ARGS"}, procs)
	if procs[0][1] != "sh -c x" {
		t.Errorf("没有 CMD 列时不应改动: %v", procs)
	}
}

// 容器提交为镜像后 Env 与 Cmd 会写进镜像配置，镜像详情同样只对有写权限的身份显示明文
func TestImageDetailMasksSecretsWithoutWrite(t *testing.T) {
	_, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /images/sha256:img/json": ctrJSON(map[string]any{"Id": "sha256:img", "Config": map[string]any{
			"Env": []string{"PATH=/usr/bin", "API_TOKEN=s3cret"}, "Cmd": []string{"app", "--key=s3cret"}, "Entrypoint": []string{"/entry.sh"},
		}}),
		"GET /images/sha256:img/history": ctrJSON([]map[string]any{{"Created": 1, "CreatedBy": "/bin/sh -c #(nop) ENV API_TOKEN=s3cret", "Size": 0}}),
	})
	call := tokenRequester(t, host)
	detail := func(scopes []string) ImageDetail {
		t.Helper()
		rec := call(scopes, "GET", "/images/sha256:img")
		if rec.Code != 200 {
			t.Fatalf("镜像详情失败: %d %s", rec.Code, rec.Body)
		}
		var d ImageDetail
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		return d
	}
	for _, scopes := range [][]string{{"docker:read"}, nil} {
		d := detail(scopes)
		if !reflect.DeepEqual(d.Env, []string{"PATH=********", "API_TOKEN=********"}) || !reflect.DeepEqual(d.Cmd, []string{"********"}) ||
			!reflect.DeepEqual(d.Entrypoint, []string{"********"}) || len(d.Layers) != 1 || d.Layers[0].CreatedBy != "********" {
			t.Errorf("%v: 镜像详情应脱敏: %+v", scopes, d)
		}
	}
	d := detail([]string{"docker:write"})
	if !reflect.DeepEqual(d.Env, []string{"PATH=/usr/bin", "API_TOKEN=s3cret"}) || !reflect.DeepEqual(d.Cmd, []string{"app", "--key=s3cret"}) || !strings.Contains(d.Layers[0].CreatedBy, "s3cret") {
		t.Errorf("写权限应看到明文: %+v", d)
	}
}
