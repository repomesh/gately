# OIDC 单点登录

Gately 内置一个 OpenID Connect（OIDC）身份提供方，把 [Web 服务](/docs/modules/web) 的共享用户提供给 Gitea、OpenList、Grafana 等应用登录。应用通过标准的 OIDC 协议得知登录的是谁（用户名、邮箱、分组），从而为每个人建立各自的账号和权限。

它覆盖了 Authelia 的 OIDC 功能。配合共享用户的 TOTP 二次验证和单点登录域，一套账号就能同时保护网关上的站点和接入应用自己的登录。

## 与网页认证的分工

| | 网页认证（共享用户） | OIDC 单点登录 |
| --- | --- | --- |
| 工作方式 | Gately 在网关上拦住未登录的访客，登录后放行 | 应用把用户带到 Gately 登录，再从 Gately 取回用户身份 |
| 应用能否区分用户 | 只知道请求被放行了 | 能，得到用户名、邮箱和分组 |
| 适合 | 没有账号体系的服务、管理后台，或只想多加一道门 | 有自己账号体系、支持 OIDC / OAuth2 登录的应用 |

两者可以同时使用：同一个共享用户既能通过站点的网页认证，也能登录 OIDC 应用。

## 准备

1. 在「Web → 认证用户」页面添加共享用户。OIDC 只接受共享用户，站点自有的用户和第三方登录身份不能用于 OIDC。
2. 按需填写每个用户的邮箱、显示名和分组：
   - 邮箱：作为 `email` 声明提供给应用，并标记为已验证，Gitea 等应用会据此创建或关联账号；
   - 显示名：作为 `name` 声明，留空时使用用户名；
   - 分组：作为 `groups` 声明，可在应用中映射为管理员，也可以用来限制哪些用户能登录某个应用。
3. 准备一个签发者域名，例如 `auth.example.com`：解析到 Gately 所在的主机，并在 [SSL 证书](/docs/modules/ssl) 中准备好覆盖它的证书，再确认某个 HTTPS 监听能收到这个域名的请求。这个域名不需要建站点，Gately 会在所有监听上识别它的 OIDC 路径；建议使用独立的域名，这个域名上的其他路径照常按站点处理。

签发者地址为 `https` 时，通过明文 HTTP 访问 OIDC 路径的 GET 请求会被跳转到 HTTPS，其他请求被拒绝，密码和令牌不会明文传输。前面还有一层反向代理终止 TLS、再以 HTTP 转发给 Gately 时，代理需要带上 `X-Forwarded-Proto: https`，Gately 信任本机、内网来源，以及经可信 PROXY 来源进入的连接带的这个请求头。例如公网 Gately 终止 TLS 后经 frp 隧道以 HTTP 转给内网 Gately：公网站点开启「添加 X-Forwarded-*」和「向后端发送 PROXY v2」，内网 Gately 的监听开启接收 PROXY 协议，并把隧道在本机的来源地址加入可信 PROXY 来源。签发者域名在某个监听上能匹配到站点（含默认站点）时，该站点的 IP 黑白名单、订阅 IP 列表、国家过滤和限流同样作用于 OIDC 路径，可以借此把登录页限制在内网。

## 启用

在「Web → 认证用户」页面的「OIDC 身份提供方」卡片中：

| 字段 | 说明 |
| --- | --- |
| 启用 | 关闭后 OIDC 路径不再响应，已有的客户端与签名密钥保留 |
| 签发者地址 | 例如 `https://auth.example.com`，只能包含协议、域名和端口。应用会核对发现文档中的 `issuer` 与配置的地址完全一致，这里要填应用访问 Gately 时使用的地址，非标准端口也要写上 |

首次保存时 Gately 自动生成 2048 位 RSA 签名密钥，保存在 Web 服务的配置文件中，会随配置一起备份，接口和界面都不会返回它。

启用后，签发者域名上提供以下地址（界面中可一键复制）：

| 用途 | 地址 |
| --- | --- |
| 发现文档 | `https://auth.example.com/.well-known/openid-configuration` |
| 授权 | `https://auth.example.com/__gately_oidc/authorize` |
| 令牌 | `https://auth.example.com/__gately_oidc/token` |
| 用户信息 | `https://auth.example.com/__gately_oidc/userinfo` |
| 公钥（JWKS） | `https://auth.example.com/__gately_oidc/jwks` |
| 退出 | `https://auth.example.com/__gately_oidc/logout` |

大多数应用只需要发现文档地址（也叫 Discovery URL、Auto Discovery URL），有的应用要求填写签发者地址（Issuer URL），少数应用需要逐个填写端点。

## 添加客户端

每个接入的应用对应一个客户端：

| 字段 | 说明 |
| --- | --- |
| 名称 | 显示在登录页上，例如「验证身份后将继续登录 Gitea」 |
| Client ID | 只能包含字母、数字和 `- . _ ~`，填到应用中 |
| Client Secret | 至少 16 个字符；点「生成」得到 43 位随机密钥。保存后只保留 bcrypt 哈希，无法再次查看，请在保存前复制到应用中；遗失时重新生成并同步到应用。也可以直接填写已有的 bcrypt 哈希（以 `$2a$`、`$2b$` 或 `$2y$` 开头） |
| 公共客户端 | 单页应用、手机 App 等无法保存密钥的客户端勾选此项：不使用密钥，必须使用 PKCE（S256），不签发刷新令牌 |
| 回调地址 | 每行一个，与应用发来的 `redirect_uri` 逐字比较，协议、端口、路径和查询参数都要一致 |
| 允许的用户 | 每行一个用户名，或 `group:分组名`，满足任意一行即可登录；留空表示全部共享用户。按用户名匹配，共享用户改名后要同步修改这里 |

::: tip 不确定回调地址时
先随便填一个地址保存，在应用中点一次登录。Gately 会停在「回调地址不匹配」的错误页，页面上显示应用实际发来的地址，原样复制到客户端的回调地址即可。
:::

## 登录流程

1. 用户在应用中点击「使用 Gately 登录」之类的按钮，应用把浏览器带到授权地址；
2. 用户已经登录过 Gately 时直接返回应用；否则显示 Gately 的登录页，启用了 TOTP 的用户还要输入动态验证码；
3. 应用在后台用授权码和 Client Secret 换取 ID Token 与 Access Token，读取用户信息，完成登录。

签发者域名位于[单点登录域](/docs/modules/web#shared-users)之内时（例如单点登录域为 `example.com`，签发者为 `auth.example.com`），用户登录任一受共享用户保护的站点后，OIDC 应用也不再要求登录，反过来也一样。

客户端由管理员登记，Gately 视其为可信应用，不显示授权确认页。

## scope 与声明

| scope | 返回的声明 |
| --- | --- |
| `openid`（必需） | `sub`：共享用户的固定 ID，随机生成，改名后不变；删除用户后再建同名用户会得到新的 ID，不会接管原用户在应用中的账号 |
| `profile` | `name`（显示名，未填时为用户名）、`preferred_username`、`nickname`（用户名） |
| `email` | `email`、`email_verified`（填写了邮箱时才返回） |
| `groups` | `groups`：分组列表 |
| `offline_access` | 额外签发刷新令牌，仅机密客户端 |

ID Token 另含 `iss`、`aud`、`azp`、`exp`、`iat`、`auth_time`、`nonce`、`at_hash`。ID Token 和 Access Token 都是 RS256 签名的 JWT。应用请求的其他 scope 会被忽略。

协议细节：只支持授权码模式（`response_type=code`），授权结果通过查询参数返回（`response_mode=query`）；令牌接口接受 `client_secret_basic` 与 `client_secret_post` 两种认证方式，公共客户端使用 `none`；PKCE 只支持 `S256`；支持 `prompt=none`，未登录时返回 `login_required`，没有该应用权限时返回 `access_denied`；`prompt=login` 或登录时间超过 `max_age` 时要求用户重新输入密码。

退出地址接受 `id_token_hint`、`client_id` 和 `post_logout_redirect_uri`：`id_token_hint` 须是本签发者签发的 ID Token（允许已过期），`post_logout_redirect_uri` 必须是该客户端登记的回调地址，或与其中之一同源，否则退出后停在登录页。

## 有效期与失效

- 授权码：2 分钟，只能使用一次；
- ID Token、Access Token：1 小时；
- 刷新令牌：30 天，刷新时沿用原令牌；
- 修改共享用户的密码或 TOTP、改名、删除用户、把用户移出客户端的允许列表、删除客户端后，尚未兑换的授权码、刷新令牌和已签发的 Access Token 立即失效（用户信息接口拒绝旧的 Access Token）。应用内已经建立的登录状态由应用自己管理，需要时请在应用中让该用户退出；
- 修改邮箱、显示名或分组后，用户信息接口立即返回新值，ID Token 中的声明在下次登录或刷新令牌时更新；允许列表按分组授权时，把用户移出分组立即生效；
- 签名密钥保存在 Web 服务配置文件 `webservice.json` 的 `settings.oidc.signingKey` 中。需要更换时停止 Gately，删除这个字段后启动，会生成新密钥，所有已签发的令牌随之失效。

## 应用配置示例

以下示例假设签发者地址为 `https://auth.example.com`，请替换为自己的地址。

### Gitea

在 Gately 中添加客户端：Client ID 填 `gitea`，生成 Client Secret，回调地址填：

```text
https://git.example.com/user/oauth2/gately/callback
```

其中 `gately` 是下面在 Gitea 中填写的认证源名称，两处需要一致。

在 Gitea 的「管理面板 → 认证源 → 添加认证源」中：

| 字段 | 值 |
| --- | --- |
| 认证类型 | `OAuth2` |
| 认证名称 | `gately` |
| OAuth2 提供程序 | `OpenID Connect` |
| 客户端 ID（键）/ 客户端密钥 | Gately 中客户端的 Client ID 与 Client Secret |
| OpenID 连接自动发现 URL | `https://auth.example.com/.well-known/openid-configuration` |
| 附加授权范围（Scopes） | `profile email groups` |
| 用于提供用户组名称的 Claim 声明名称 | `groups`（可选，配合下一项使用） |
| 管理员用户组的 Claim 声明值 | 分组名，例如 `admins`：该分组的用户登录后成为 Gitea 管理员（可选） |

也可以用命令行添加：

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

希望首次登录时自动创建 Gitea 账号，在 `app.ini` 中加入：

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` 会把邮箱相同的已有 Gitea 账号与 Gately 用户自动关联。自动注册需要邮箱，请为共享用户填写邮箱。

### OpenList

在 Gately 中添加客户端：Client ID 填 `openlist`，生成 Client Secret，回调地址填以下两行（第一行用于登录，第二行用于在个人资料中绑定已有账号）：

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

在 OpenList 的「管理 → 设置 → 单点登录」中：

| 设置项 | 值 |
| --- | --- |
| SSO 登录启用（`sso_login_enabled`） | 开启 |
| SSO 登录平台（`sso_login_platform`） | `OIDC` |
| SSO 客户端 ID / SSO 客户端密钥 | Gately 中客户端的 Client ID 与 Client Secret |
| SSO 端点名称（`sso_endpoint_name`） | 签发者地址 `https://auth.example.com`，不带 `/.well-known/...` |
| OIDC 用户名键（`sso_oidc_username_key`） | `preferred_username`。默认值 `name` 取的是显示名 |
| SSO 自动注册（`sso_auto_register`） | 开启后首次登录自动创建 OpenList 用户；关闭时用户需要先用 OpenList 账号登录，在个人资料中绑定单点登录 |

保存后，OpenList 登录页会出现单点登录按钮。

### Grafana

在 Gately 中添加客户端：Client ID 填 `grafana`，生成 Client Secret，回调地址填 `https://grafana.example.com/login/generic_oauth`。

在 `grafana.ini` 中：

```ini
[server]
root_url = https://grafana.example.com/

[auth.generic_oauth]
enabled = true
name = Gately
client_id = grafana
client_secret = <Client Secret>
scopes = openid profile email groups
auth_url = https://auth.example.com/__gately_oidc/authorize
token_url = https://auth.example.com/__gately_oidc/token
api_url = https://auth.example.com/__gately_oidc/userinfo
use_pkce = true
login_attribute_path = preferred_username
name_attribute_path = name
email_attribute_path = email
groups_attribute_path = groups
role_attribute_path = contains(groups[*], 'admins') && 'Admin' || 'Viewer'
```

`role_attribute_path` 把 `admins` 分组的用户设为 Grafana 管理员，其他人为只读用户。

### 其他应用

支持 OIDC 或「通用 OAuth2」登录的应用都可以接入，常见的填写方式：

- 只要求发现地址或签发者地址：填 `https://auth.example.com/.well-known/openid-configuration` 或 `https://auth.example.com`；
- 要求逐个填写端点：按上面「启用」一节的表格填写授权、令牌、用户信息和公钥地址；
- 用户名字段选 `preferred_username`，邮箱字段选 `email`，分组字段选 `groups`；
- 回调地址在应用的文档或设置页中，也可以按前面的提示从错误页复制。

## 常见问题

**应用提示 issuer 不匹配。** 签发者地址必须与应用访问的地址完全一致，包括 `https` 和端口。修改签发者地址后，部分应用需要重启才会重新读取发现文档。

**登录后停在「回调地址不匹配」。** 把错误页上显示的地址原样加入客户端的回调地址。

**登录后显示「没有使用该应用的权限」。** 当前用户不在客户端的允许列表中。可以调整允许列表，或者点页面上的「换个账号登录」，退出当前账号后用其他共享用户登录，登录后会继续原来的授权。

**应用报 `invalid_client`。** Client Secret 不正确，或者应用把公共客户端当作机密客户端使用。Client Secret 只在保存前可见，遗失时重新生成并同步到应用。令牌接口的认证失败与登录失败共用限流：同一来源连续失败 10 次后暂停 15 分钟，开启了自动封禁时还会封禁来源 IP。

**应用服务端无法连接签发者。** 发现文档、令牌和公钥都由应用的服务端直接请求，不经过浏览器。应用所在的主机或容器需要能解析签发者域名，并能访问对应端口；应用运行在 Docker 中时，可以用 `extra_hosts` 把域名指向宿主机。

**应用要求 `form_post` 或隐式模式。** Gately 只支持授权码模式，并通过查询参数返回结果。大多数应用默认就是这种方式，或者可以在设置中切换。
