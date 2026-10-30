package docker

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wangwanjie/gately/internal/auth"
	"github.com/wangwanjie/gately/internal/crud"
	"github.com/wangwanjie/gately/internal/httpx"
)

// 本文件处理容器详情中的敏感字段：环境变量、Command、Entrypoint 与启动参数常带有密码、令牌，
// 没有 Docker 写权限的身份（只读账号、只读 API 令牌）看到的是脱敏值，明文需调用按写权限鉴权的
// GET /containers/{id}/secrets（路径以 secrets 结尾，见 apitoken.Required）。

// secretsScope 是查看容器敏感字段明文所需的权限。
const secretsScope = "docker:write"

// canViewSecrets 判断当前请求能否直接看到容器敏感字段的明文。
func canViewSecrets(r *http.Request) bool {
	return auth.Allowed(r.Context(), secretsScope)
}

// ContainerSecrets 是容器敏感字段的明文，字段与详情中的同名字段一一对应。
type ContainerSecrets struct {
	Env        []string `json:"env"`
	Command    string   `json:"command"`
	Entrypoint string   `json:"entrypoint"`
	Args       []string `json:"args"`
}

func (m *Module) handleDetailSecrets(w http.ResponseWriter, r *http.Request) {
	d, ok := m.loadDetail(w, r)
	if !ok {
		return
	}
	httpx.OK(w, ContainerSecrets{Env: d.Env, Command: d.Command, Entrypoint: d.Entrypoint, Args: d.Args})
}

// maskDetailSecrets 把详情中的敏感字段替换为占位符：环境变量保留变量名，只隐藏非空的值；
// Command、Entrypoint 与启动参数整体隐藏（Command 常是传给 Entrypoint 的参数，无法区分哪段是程序名），
// 可执行文件路径 Path 保留。
func maskDetailSecrets(d *ContainerDetail) {
	env := make([]string, len(d.Env))
	for i, e := range d.Env {
		env[i] = maskEnv(e)
	}
	d.Env = env
	d.Command = crud.Mask(d.Command)
	d.Entrypoint = crud.Mask(d.Entrypoint)
	if len(d.Args) > 0 {
		d.Args = []string{crud.SecretMask}
	}
}

// maskImageSecrets 对没有写权限的身份隐藏镜像配置中的敏感字段：容器提交为镜像时 Env 与 Cmd 会原样写入镜像，
// 构建历史的 CreatedBy 也可能带有 ENV、ARG 中的密钥。环境变量保留变量名，Cmd、Entrypoint 与各层命令整体隐藏。
func maskImageSecrets(d *ImageDetail) {
	env := make([]string, len(d.Env))
	for i, e := range d.Env {
		env[i] = maskEnv(e)
	}
	d.Env = env
	if len(d.Cmd) > 0 {
		d.Cmd = []string{crud.SecretMask}
	}
	if len(d.Entrypoint) > 0 {
		d.Entrypoint = []string{crud.SecretMask}
	}
	for i := range d.Layers {
		d.Layers[i].CreatedBy = crud.Mask(d.Layers[i].CreatedBy)
	}
}

// maskEnv 隐藏 KEY=VALUE 中非空的值；不含等号（只有变量名）或值为空时原样返回。
func maskEnv(e string) string {
	k, v, ok := strings.Cut(e, "=")
	if !ok || v == "" {
		return e
	}
	return k + "=" + crud.SecretMask
}

// restoreMaskedSpec 把编辑提交中仍为占位符的环境变量、Command 与 Entrypoint 恢复为旧容器的值，
// 避免用脱敏详情回填的表单把占位符写进新容器。旧容器没有同名变量时保留原样，由用户自行修正。
func restoreMaskedSpec(spec *ContainerSpec, info map[string]json.RawMessage) {
	var cfg inspectConfig
	decodeField(info, "Config", &cfg)
	old := map[string]string{}
	for _, e := range cfg.Env {
		if k, _, ok := strings.Cut(e, "="); ok {
			old[k] = e
		}
	}
	for i, e := range spec.Env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || v != crud.SecretMask {
			continue
		}
		if prev, found := old[strings.TrimSpace(k)]; found {
			spec.Env[i] = prev
		}
	}
	if spec.Command == crud.SecretMask {
		spec.Command = shellJoin(cfg.Cmd)
	}
	if spec.Entrypoint == crud.SecretMask {
		spec.Entrypoint = shellJoin(cfg.Entrypoint)
	}
}

// maskProcessCommands 隐藏进程列表命令行列（docker top 的 CMD/COMMAND）中的参数，只保留可执行文件。
func maskProcessCommands(titles []string, procs [][]string) {
	col := -1
	for i, t := range titles {
		if t == "CMD" || t == "COMMAND" {
			col = i
			break
		}
	}
	if col < 0 {
		return
	}
	for _, p := range procs {
		if col >= len(p) {
			continue
		}
		if exe, rest, ok := strings.Cut(strings.TrimSpace(p[col]), " "); ok && strings.TrimSpace(rest) != "" {
			p[col] = exe + " " + crud.SecretMask
		}
	}
}
