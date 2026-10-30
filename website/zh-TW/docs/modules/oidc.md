# OIDC 單一登入

Gately 內建一個 OpenID Connect（OIDC）身分提供者，把 [Web 服務](/zh-TW/docs/modules/web) 的共用使用者提供給 Gitea、OpenList、Grafana 等應用程式登入。應用程式透過標準的 OIDC 協定得知登入的是誰（使用者名稱、電子郵件、群組），從而為每個人建立各自的帳號和權限。

它涵蓋了 Authelia 的 OIDC 功能。搭配共用使用者的 TOTP 二次驗證和單一登入網域，一套帳號就能同時保護閘道上的站台和接入應用程式自己的登入。

## 與網頁驗證的分工

| | 網頁驗證（共用使用者） | OIDC 單一登入 |
| --- | --- | --- |
| 運作方式 | Gately 在閘道上攔住未登入的訪客，登入後放行 | 應用程式把使用者帶到 Gately 登入，再從 Gately 取回使用者身分 |
| 應用程式能否區分使用者 | 只知道請求被放行了 | 能，取得使用者名稱、電子郵件和群組 |
| 適合 | 沒有帳號體系的服務、管理後台，或只想多加一道門 | 有自己帳號體系、支援 OIDC / OAuth2 登入的應用程式 |

兩者可以同時使用：同一個共用使用者既能通過站台的網頁驗證，也能登入 OIDC 應用程式。

## 準備

1. 在「Web → 認證使用者」頁面新增共用使用者。OIDC 只接受共用使用者，站台自有的使用者和第三方登入身分不能用於 OIDC。
2. 依需要填寫每個使用者的電子郵件、顯示名稱和群組：
   - 電子郵件：作為 `email` 聲明提供給應用程式，並標記為已驗證，Gitea 等應用程式會據此建立或關聯帳號；
   - 顯示名稱：作為 `name` 聲明，留空時使用使用者名稱；
   - 群組：作為 `groups` 聲明，可在應用程式中對應為管理員，也可以用來限制哪些使用者能登入某個應用程式。
3. 準備一個簽發者網域，例如 `auth.example.com`：解析到 Gately 所在的主機，並在 [SSL 憑證](/zh-TW/docs/modules/ssl) 中準備好涵蓋它的憑證，再確認某個 HTTPS 監聽能收到這個網域的請求。這個網域不需要建站台，Gately 會在所有監聽上識別它的 OIDC 路徑；建議使用獨立的網域，這個網域上的其他路徑照常按站台處理。

簽發者位址為 `https` 時，透過明文 HTTP 存取 OIDC 路徑的 GET 請求會被重新導向到 HTTPS，其他請求被拒絕，密碼和權杖不會明文傳輸。前面還有一層反向代理終止 TLS、再以 HTTP 轉發給 Gately 時，代理需要帶上 `X-Forwarded-Proto: https`，Gately 信任本機、內網來源，以及經可信 PROXY 來源進入的連線帶的這個請求標頭。例如公網 Gately 終止 TLS 後經 frp 隧道以 HTTP 轉給內網 Gately：公網站點開啟「新增 X-Forwarded-*」和「向後端傳送 PROXY v2」，內網 Gately 的監聽開啟接收 PROXY 協定，並把隧道在本機的來源位址加入可信 PROXY 來源。簽發者網域在某個監聽上能比對到站台（含預設站台）時，該站台的 IP 黑白名單、訂閱 IP 清單、國家過濾和限流同樣作用於 OIDC 路徑，可以藉此把登入頁限制在內網。

## 啟用

在「Web → 認證使用者」頁面的「OIDC 身分提供者」卡片中：

| 欄位 | 說明 |
| --- | --- |
| 啟用 | 關閉後 OIDC 路徑不再回應，既有的用戶端與簽章金鑰保留 |
| 簽發者位址 | 例如 `https://auth.example.com`，只能包含協定、網域和連接埠。應用程式會核對探索文件中的 `issuer` 與設定的位址完全一致，這裡要填應用程式存取 Gately 時使用的位址，非標準連接埠也要寫上 |

首次儲存時 Gately 自動產生 2048 位元 RSA 簽章金鑰，儲存在 Web 服務的設定檔中，會隨設定一起備份，API 和介面都不會回傳它。

啟用後，簽發者網域上提供以下位址（介面中可一鍵複製）：

| 用途 | 位址 |
| --- | --- |
| 探索文件 | `https://auth.example.com/.well-known/openid-configuration` |
| 授權 | `https://auth.example.com/__gately_oidc/authorize` |
| 權杖 | `https://auth.example.com/__gately_oidc/token` |
| 使用者資訊 | `https://auth.example.com/__gately_oidc/userinfo` |
| 公鑰（JWKS） | `https://auth.example.com/__gately_oidc/jwks` |
| 登出 | `https://auth.example.com/__gately_oidc/logout` |

大多數應用程式只需要探索文件位址（也叫 Discovery URL、Auto Discovery URL），有的應用程式要求填寫簽發者位址（Issuer URL），少數應用程式需要逐一填寫端點。

## 新增用戶端

每個接入的應用程式對應一個用戶端：

| 欄位 | 說明 |
| --- | --- |
| 名稱 | 顯示在登入頁上，例如「驗證身分後將繼續登入 Gitea」 |
| Client ID | 只能包含字母、數字和 `- . _ ~`，填到應用程式中 |
| Client Secret | 至少 16 個字元；點「產生」得到 43 位隨機金鑰。儲存後只保留 bcrypt 雜湊，無法再次查看，請在儲存前複製到應用程式中；遺失時重新產生並同步到應用程式。也可以直接填寫既有的 bcrypt 雜湊（以 `$2a$`、`$2b$` 或 `$2y$` 開頭） |
| 公開用戶端 | 單頁應用程式、手機 App 等無法保存金鑰的用戶端勾選此項：不使用金鑰，必須使用 PKCE（S256），不簽發重新整理權杖 |
| 回呼位址 | 每行一個，與應用程式送來的 `redirect_uri` 逐字比較，協定、連接埠、路徑和查詢參數都要一致 |
| 允許的使用者 | 每行一個使用者名稱，或 `group:群組名`，符合任意一行即可登入；留空表示所有共用使用者。按使用者名稱比對，共用使用者改名後要同步修改這裡 |

::: tip 不確定回呼位址時
先隨便填一個位址儲存，在應用程式中點一次登入。Gately 會停在「回呼位址不符」的錯誤頁，頁面上顯示應用程式實際送來的位址，原樣複製到用戶端的回呼位址即可。
:::

## 登入流程

1. 使用者在應用程式中點擊「使用 Gately 登入」之類的按鈕，應用程式把瀏覽器帶到授權位址；
2. 使用者已經登入過 Gately 時直接返回應用程式；否則顯示 Gately 的登入頁，啟用了 TOTP 的使用者還要輸入動態驗證碼；
3. 應用程式在背景用授權碼和 Client Secret 換取 ID Token 與 Access Token，讀取使用者資訊，完成登入。

簽發者網域位於[單一登入網域](/zh-TW/docs/modules/web#shared-users)之內時（例如單一登入網域為 `example.com`，簽發者為 `auth.example.com`），使用者登入任一受共用使用者保護的站台後，OIDC 應用程式也不再要求登入，反過來也一樣。

用戶端由管理員登記，Gately 視其為可信任的應用程式，不顯示授權確認頁。

## scope 與聲明

| scope | 回傳的聲明 |
| --- | --- |
| `openid`（必需） | `sub`：共用使用者的固定 ID，隨機產生，改名後不變；刪除使用者後再建同名使用者會得到新的 ID，不會接管原使用者在應用程式中的帳號 |
| `profile` | `name`（顯示名稱，未填時為使用者名稱）、`preferred_username`、`nickname`（使用者名稱） |
| `email` | `email`、`email_verified`（填寫了電子郵件時才回傳） |
| `groups` | `groups`：群組清單 |
| `offline_access` | 額外簽發重新整理權杖，僅機密用戶端 |

ID Token 另含 `iss`、`aud`、`azp`、`exp`、`iat`、`auth_time`、`nonce`、`at_hash`。ID Token 和 Access Token 都是 RS256 簽章的 JWT。應用程式請求的其他 scope 會被忽略。

協定細節：只支援授權碼模式（`response_type=code`），授權結果透過查詢參數回傳（`response_mode=query`）；權杖端點接受 `client_secret_basic` 與 `client_secret_post` 兩種驗證方式，公開用戶端使用 `none`；PKCE 只支援 `S256`；支援 `prompt=none`，未登入時回傳 `login_required`，沒有該應用程式權限時回傳 `access_denied`；`prompt=login` 或登入時間超過 `max_age` 時要求使用者重新輸入密碼。

登出位址接受 `id_token_hint`、`client_id` 和 `post_logout_redirect_uri`：`id_token_hint` 須是本簽發者簽發的 ID Token（允許已過期），`post_logout_redirect_uri` 必須是該用戶端登記的回呼位址，或與其中之一同源，否則登出後停在登入頁。

## 有效期與失效

- 授權碼：2 分鐘，只能使用一次；
- ID Token、Access Token：1 小時；
- 重新整理權杖：30 天，重新整理時沿用原權杖；
- 修改共用使用者的密碼或 TOTP、改名、刪除使用者、把使用者移出用戶端的允許清單、刪除用戶端後，尚未兌換的授權碼、重新整理權杖和已簽發的 Access Token 立即失效（使用者資訊端點拒絕舊的 Access Token）。應用程式內已經建立的登入狀態由應用程式自己管理，需要時請在應用程式中讓該使用者登出；
- 修改電子郵件、顯示名稱或群組後，使用者資訊端點立即回傳新值，ID Token 中的聲明在下次登入或重新整理權杖時更新；允許清單按群組授權時，把使用者移出群組立即生效；
- 簽章金鑰儲存在 Web 服務設定檔 `webservice.json` 的 `settings.oidc.signingKey` 中。需要更換時停止 Gately，刪除這個欄位後啟動，會產生新金鑰，所有已簽發的權杖隨之失效。

## 應用程式設定範例

以下範例假設簽發者位址為 `https://auth.example.com`，請替換為自己的位址。

### Gitea

在 Gately 中新增用戶端：Client ID 填 `gitea`，產生 Client Secret，回呼位址填：

```text
https://git.example.com/user/oauth2/gately/callback
```

其中 `gately` 是下面在 Gitea 中填寫的驗證來源名稱，兩處需要一致。

在 Gitea 的「Site Administration → Authentication Sources → Add Authentication Source」中：

| 欄位 | 值 |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | Gately 中用戶端的 Client ID 與 Client Secret |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups`（選填，搭配下一項使用） |
| Group Claim value for administrator users. (Optional — requires claim name above) | 群組名，例如 `admins`：該群組的使用者登入後成為 Gitea 管理員（選填） |

也可以用命令列新增：

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

希望首次登入時自動建立 Gitea 帳號，在 `app.ini` 中加入：

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` 會把電子郵件相同的既有 Gitea 帳號與 Gately 使用者自動關聯。自動註冊需要電子郵件，請為共用使用者填寫電子郵件。

### OpenList

在 Gately 中新增用戶端：Client ID 填 `openlist`，產生 Client Secret，回呼位址填以下兩行（第一行用於登入，第二行用於在個人資料中綁定既有帳號）：

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

在 OpenList 的「Manage → Settings → SSO」中：

| 設定項 | 值 |
| --- | --- |
| 啟用 SSO 登入（`sso_login_enabled`） | 開啟 |
| SSO 登入平台（`sso_login_platform`） | `OIDC` |
| SSO 用户端 ID / SSO 用户端密鑰 | Gately 中用戶端的 Client ID 與 Client Secret |
| SSO 端點名稱（`sso_endpoint_name`） | 簽發者位址 `https://auth.example.com`，不帶 `/.well-known/...` |
| OIDC 使用者名稱鍵（`sso_oidc_username_key`） | `preferred_username`。預設值 `name` 取的是顯示名稱 |
| SSO 自動註冊（`sso_auto_register`） | 開啟後首次登入自動建立 OpenList 使用者；關閉時使用者需要先用 OpenList 帳號登入，在個人資料中綁定單一登入 |

儲存後，OpenList 登入頁會出現單一登入按鈕。

### Grafana

在 Gately 中新增用戶端：Client ID 填 `grafana`，產生 Client Secret，回呼位址填 `https://grafana.example.com/login/generic_oauth`。

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

`role_attribute_path` 把 `admins` 群組的使用者設為 Grafana 管理員，其他人為唯讀使用者。

### 其他應用程式

支援 OIDC 或「通用 OAuth2」登入的應用程式都可以接入，常見的填寫方式：

- 只要求探索位址或簽發者位址：填 `https://auth.example.com/.well-known/openid-configuration` 或 `https://auth.example.com`；
- 要求逐一填寫端點：依上面「啟用」一節的表格填寫授權、權杖、使用者資訊和公鑰位址；
- 使用者名稱欄位選 `preferred_username`，電子郵件欄位選 `email`，群組欄位選 `groups`；
- 回呼位址在應用程式的文件或設定頁中，也可以依前面的提示從錯誤頁複製。

## 常見問題

**應用程式提示 issuer 不符。** 簽發者位址必須與應用程式存取的位址完全一致，包括 `https` 和連接埠。修改簽發者位址後，部分應用程式需要重新啟動才會重新讀取探索文件。

**登入後停在「回呼位址不符」。** 把錯誤頁上顯示的位址原樣加入用戶端的回呼位址。

**登入後顯示「沒有使用該應用程式的權限」。** 目前使用者不在用戶端的允許清單中。可以調整允許清單，或者點頁面上的「換個帳號登入」，登出目前帳號後用其他共用使用者登入，登入後會繼續原本的授權。

**應用程式報 `invalid_client`。** Client Secret 不正確，或者應用程式把公開用戶端當作機密用戶端使用。Client Secret 只在儲存前可見，遺失時重新產生並同步到應用程式。權杖端點的驗證失敗與登入失敗共用限流：同一來源連續失敗 10 次後暫停 15 分鐘，開啟了自動封鎖時還會封鎖來源 IP。

**應用程式伺服器端無法連線簽發者。** 探索文件、權杖和公鑰都由應用程式的伺服器端直接請求，不經過瀏覽器。應用程式所在的主機或容器需要能解析簽發者網域，並能存取對應連接埠；應用程式執行在 Docker 中時，可以用 `extra_hosts` 把網域指向宿主機。

**應用程式要求 `form_post` 或隱含模式。** Gately 只支援授權碼模式，並透過查詢參數回傳結果。大多數應用程式預設就是這種方式，或者可以在設定中切換。
