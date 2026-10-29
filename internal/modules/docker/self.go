package docker

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// 本文件识别 Gately 自身所在的容器，避免在界面上误停、误删自己。

var (
	selfOnce sync.Once
	selfCID  string
	selfHost string
)

// containerIDPatterns 依次匹配 mountinfo 中的 /containers/<id>/、cgroup 中的 docker/<id> 与 docker-<id>.scope。
// 必须带上路径前缀：overlay 一行的 upperdir 里也有 64 位十六进制层 ID，直接匹配会取错。
var containerIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`/containers/([0-9a-f]{64})/`),
	regexp.MustCompile(`/docker/([0-9a-f]{64})(?:[/\s]|$)`),
	regexp.MustCompile(`docker-([0-9a-f]{64})\.scope`),
}

// detectSelf 读取当前进程所在的容器 ID（64 位十六进制）与主机名；不在容器中时 ID 为空。
func detectSelf() {
	selfHost, _ = os.Hostname()
	// 优先从 mountinfo / cgroup 中提取完整容器 ID，兼容自定义主机名与 host 网络模式（主机名为宿主机名）
	for _, f := range []string{"/proc/self/mountinfo", "/proc/self/cgroup"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, re := range containerIDPatterns {
			if m := re.FindStringSubmatch(string(data)); m != nil {
				selfCID = m[1]
				return
			}
		}
	}
}

// selfRebuildError 是拒绝重建 Gately 自身所在容器的提示：重建的第一步是停止旧容器，
// 停止后执行重建的 Gately 进程随之退出，创建、启动新容器与失败恢复都无法完成。
const selfRebuildError = "不能在 Gately 内升级或重建 Gately 自身所在的容器，请在宿主机上执行 docker pull 后重建容器（Compose 部署执行 docker compose pull && docker compose up -d）"

// isSelfContainer 判断给定容器 ID 是否为运行 Gately 的容器；测试中可替换。
var isSelfContainer = detectIsSelf

func detectIsSelf(id string) bool {
	selfOnce.Do(detectSelf)
	if id == "" {
		return false
	}
	if selfCID != "" {
		return id == selfCID
	}
	// 退回到默认主机名（容器 ID 前 12 位）
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return false
	}
	return len(selfHost) >= 12 && strings.HasPrefix(id, selfHost)
}
