package i18n

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/wangwanjie/gately/internal/i18n/extract"
)

func testCatalog(m map[string]string) {
	catalogMu.Lock()
	catalogs["xx"] = buildCatalog(m)
	catalogMu.Unlock()
}

func TestTrExactTemplateAndWrap(t *testing.T) {
	testCatalog(map[string]string{
		"创建目录失败: ":     "failed to create directory: ",
		"端口 %d 已被占用":   "port %d is in use",
		"加载 %s 失败: %w": "failed to load %[1]s: %[2]s",
		"模块启动失败: ":     "module failed to start: ",
		"丢包率 %.0f%%":   "packet loss %.0f%%",
		"测试":           "test",
	})
	cases := map[string]string{
		"端口 8080 已被占用":                 "port 8080 is in use",
		"模块启动失败: 端口 80 已被占用":           "module failed to start: port 80 is in use",
		"加载 配置 失败: 创建目录失败: permission": "failed to load 配置: failed to create directory: permission",
		"丢包率 12%":      "packet loss 12%",
		"测试":           "test",
		"English only": "English only",
	}
	for in, want := range cases {
		if got := Tr("xx", in); got != want {
			t.Errorf("Tr(%q) = %q，期望 %q", in, got, want)
		}
	}
	if got := Tr(SourceLang, "端口 1 已被占用"); got != "端口 1 已被占用" {
		t.Errorf("源语言应原样返回: %q", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"zh_CN.UTF-8": "zh-CN", "zh-Hant-TW": "zh-TW", "zh-HK": "zh-TW", "en_US": "en", "pt": "pt-BR",
		"pt-PT": "pt-BR", "ja-JP": "ja", "C": "", "": "", "xx": "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q，期望 %q", in, got, want)
		}
	}
	if got := FromAcceptLanguage("fr-CH, fr;q=0.9, en;q=0.8"); got != "fr" {
		t.Errorf("FromAcceptLanguage = %q", got)
	}
}

func TestMiddlewareTranslatesErrors(t *testing.T) {
	testCatalog(map[string]string{"请求体为空": "request body is empty"})
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"请求体为空"}` + "\n"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.Header.Set("Accept-Language", "xx")
	// 未注册的语言码 Normalize 后为空，改用 en 的结果路径：直接验证 transWriter
	rec := httptest.NewRecorder()
	tw := &transWriter{ResponseWriter: rec, lang: "xx", path: "/api/x", get: false}
	h2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"请求体为空"}` + "\n"))
	})
	h2.ServeHTTP(tw, req)
	tw.finish()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "request body is empty" {
		t.Fatalf("错误响应未翻译: %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码被改变: %d", rec.Code)
	}
	// 简体中文请求不翻译
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req2.Header.Set("Accept-Language", "zh-CN")
	h.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "请求体为空") {
		t.Fatalf("中文请求不应翻译: %s", rec2.Body.String())
	}
}

// TestCatalogsComplete 保证源码里的每个中文字面量在所有语言里都有译文，且格式动词与原文一致。
func TestCatalogsComplete(t *testing.T) {
	keys, err := extract.Literals("../../internal", "../../cmd")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range Langs {
		if lang == SourceLang {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("locales", lang+".json"))
		if err != nil {
			t.Fatalf("缺少语言文件 %s: %v", lang, err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s 不是合法 JSON: %v", lang, err)
		}
		missing := 0
		for _, k := range keys {
			v := m[k]
			if v == "" {
				if missing < 5 {
					t.Errorf("%s 缺少译文: %q", lang, k)
				}
				missing++
				continue
			}
			if !reflect.DeepEqual(normVerbs(Verbs(k)), normVerbs(Verbs(v))) {
				t.Errorf("%s 格式动词与原文不一致: %q => %q", lang, k, v)
			}
		}
		if missing > 0 {
			t.Errorf("%s 共缺少 %d 条译文，请运行 go run ./internal/i18n/cmd/i18nsync 后补译", lang, missing)
		}
	}
}

func normVerbs(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = regexpIndex.ReplaceAllString(s, "")
	}
	// 顺序可因 %[n] 调整，按集合比较
	sortStrings(out)
	return out
}

var regexpIndex = regexp.MustCompile(`\[\d+\]`)

func sortStrings(v []string) { sort.Strings(v) }

// TestEmbeddedLocalesUpToDate 保证内嵌的 .json.gz 与 locales/<语言>.json 内容一致，避免改了 JSON 忘记重新生成。
func TestEmbeddedLocalesUpToDate(t *testing.T) {
	for _, lang := range Langs {
		if lang == SourceLang {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("locales", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var want map[string]string
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		got, err := readLocale(lang)
		if err != nil {
			t.Fatalf("%s 内嵌译文读取失败: %v", lang, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s 的内嵌译文已过期，请运行 go run ./internal/i18n/cmd/i18nsync -root . 重新生成", lang)
		}
	}
}

// TestCatalogLazyLoad 确认译文表按语言首次使用时才加载。
func TestCatalogLazyLoad(t *testing.T) {
	catalogMu.Lock()
	delete(catalogs, "de")
	catalogMu.Unlock()
	if Tr("de", "测试一下不存在的文案") == "" {
		t.Fatal("翻译结果不应为空")
	}
	catalogMu.Lock()
	c, ok := catalogs["de"]
	catalogMu.Unlock()
	if !ok || c == nil || len(c.exact) == 0 {
		t.Fatal("de 译文表应在首次使用时加载")
	}
	if catalogFor("xx-unknown") != nil {
		t.Fatal("不存在的语言应返回 nil")
	}
}

// 带前缀的日志行：通用模板（“%s失败: %v”）能整句匹配时，句中更具体的模板优先，避免前缀连同句子落进参数而漏翻。
func TestSpecificTemplateInsidePrefixedLine(t *testing.T) {
	testCatalog(map[string]string{
		"%s失败: %v":        "%s failed: %v",
		"来自 %s 的登录失败: %v": "Sign-in from %s failed: %v",
		"用户名或密码错误":        "Incorrect username or password",
		"加载 %s 失败: %w":    "failed to load %[1]s: %[2]s",
	})
	cases := map[string]string{
		"[warn] [auth] 来自 1.2.3.4 的登录失败: 用户名或密码错误": "[warn] [auth] Sign-in from 1.2.3.4 failed: Incorrect username or password",
		"来自 1.2.3.4 的登录失败: 用户名或密码错误":               "Sign-in from 1.2.3.4 failed: Incorrect username or password",
		// 外层通用模板包住更具体的内层句子时，两层都要翻译
		"加载 配置 失败: 来自 1.2.3.4 的登录失败: 用户名或密码错误": "failed to load 配置: Sign-in from 1.2.3.4 failed: Incorrect username or password",
		"同步失败: 超时": "同步 failed: 超时",
	}
	for in, want := range cases {
		if got := Tr("xx", in); got != want {
			t.Errorf("Tr(%q) = %q，期望 %q", in, got, want)
		}
	}
}
