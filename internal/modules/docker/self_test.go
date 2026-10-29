package docker

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerIDPatterns(t *testing.T) {
	const real = "4bff9c70c35e07d652a11fa9b836dcbe6ecab280274226130ef23436d851f34a"
	const layer = "7c8e6bf192e84d2b61d8374d9959204c04696bf0312d03ab3c483d327fd9ccc6"
	mountinfo := "4119 2049 0:144 / / rw - overlay overlay rw,upperdir=/vol1/docker/overlay2/" + layer + "/diff,workdir=/vol1/docker/overlay2/" + layer + "/work\n" +
		"4128 4119 0:56 /docker/containers/" + real + "/resolv.conf /etc/resolv.conf rw - btrfs /dev/x rw\n"
	var got string
	for _, re := range containerIDPatterns {
		if m := re.FindStringSubmatch(mountinfo); m != nil {
			got = m[1]
			break
		}
	}
	if got != real {
		t.Fatalf("mountinfo 解析得到 %q，期望 %q", got, real)
	}
	cg := "0::/system.slice/docker-" + real + ".scope\n"
	got = ""
	for _, re := range containerIDPatterns {
		if m := re.FindStringSubmatch(cg); m != nil {
			got = m[1]
			break
		}
	}
	if got != real {
		t.Fatalf("cgroup 解析得到 %q，期望 %q", got, real)
	}
}

// fakeSelf 把 id 视为 Gately 自身所在的容器，测试结束后恢复。
func fakeSelf(t *testing.T, id string) {
	t.Helper()
	old := isSelfContainer
	isSelfContainer = func(cid string) bool {
		return cid == id
	}
	t.Cleanup(func() {
		isSelfContainer = old
	})
}

// 升级、按新配置重建自身所在的容器会先停止自己，必须在停止之前拒绝（issue #38）
func TestSelfContainerRebuildRejected(t *testing.T) {
	info := ctrSampleInfo(t)
	var id string
	_ = json.Unmarshal(info["Id"], &id)
	other := strings.Repeat("d", 64)
	f, host := newCtrFake(t, map[string]http.HandlerFunc{
		"GET /containers/" + id + "/json": ctrJSON(info),
	})
	_, api := ctrAPI(t, host)
	fakeSelf(t, id)

	rec := api("POST", "/containers/"+id+"/upgrade", "", nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "宿主机") {
		t.Fatalf("升级自身应拒绝并提示在宿主机更新: %d %s", rec.Code, rec.Body)
	}
	rec = api("POST", "/containers/"+id+"/edit", "application/json", jsonBody(specFromInspect(info, nil)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "宿主机") {
		t.Fatalf("重建自身应拒绝: %d %s", rec.Code, rec.Body)
	}

	// 批量升级跳过自身，其余容器照常处理（这里 other 不存在，同样记为失败）
	rec = api("POST", "/containers/upgrade-batch", "application/json", jsonBody(map[string]any{"ids": []string{id, other}}))
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	task := ctrWaitTask(t, out["task"])
	joined := strings.Join(task.Lines, "\n")
	if task.Status != "error" || !strings.Contains(task.Error, "2 个容器升级失败") || !strings.Contains(joined, "已跳过") {
		t.Fatalf("批量升级应跳过自身: %+v", task)
	}
	if f.called("GET /containers/"+other+"/json") == 0 {
		t.Errorf("跳过自身后应继续处理其余容器: %v", f.calls)
	}
	for _, step := range []string{"POST /images/create", "POST /containers/" + id + "/stop", "POST /containers/create"} {
		if f.called(step) != 0 {
			t.Errorf("不应对自身执行 %s: %v", step, f.calls)
		}
	}
}

// Compose 项目包含自身时，up 会重建自身、删除项目会移除自身，都应拒绝
func TestComposeSelfProjectRejected(t *testing.T) {
	installFakeCLI(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644)
	f := composeFake(dir)
	_, do := newResEnv(t, f)
	fakeSelf(t, "app1")
	if rec := do("POST", "/compose/web/up", "", nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "docker compose up -d") {
		t.Fatalf("包含自身的项目 up 应拒绝: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/compose/web/stop", "", nil); rec.Code != 400 {
		t.Fatalf("包含自身的项目 stop 应拒绝: %d %s", rec.Code, rec.Body)
	}
	// pull 只拉取镜像，不重建容器，可以执行
	if tk := waitTask(t, taskID(t, do("POST", "/compose/web/pull", "", nil))); tk.Status != "done" {
		t.Fatalf("pull 不应被拒绝: %+v", tk)
	}
}
