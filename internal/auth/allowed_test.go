package auth

import (
	"context"
	"testing"
)

func TestAllowed(t *testing.T) {
	bg := context.Background()
	admin := context.WithValue(bg, roleKey{}, RoleAdmin)
	readonly := context.WithValue(bg, roleKey{}, RoleReadonly)
	token := WithScopeCheck(bg, func(need string) bool {
		return need == "docker:read"
	})
	cases := []struct {
		name string
		ctx  context.Context
		need string
		want bool
	}{
		{"管理员具备写权限", admin, "docker:write", true},
		{"只读账号具备读权限", readonly, "docker:read", true},
		{"只读账号不具备写权限", readonly, "docker:write", false},
		{"令牌按自身权限判定：读", token, "docker:read", true},
		{"令牌按自身权限判定：写", token, "docker:write", false},
		{"没有身份时无权限", bg, "docker:read", false},
	}
	for _, c := range cases {
		if got := Allowed(c.ctx, c.need); got != c.want {
			t.Errorf("%s: Allowed(%q) = %v，期望 %v", c.name, c.need, got, c.want)
		}
	}
}
