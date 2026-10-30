# OIDC 통합 로그인

Gately에는 OpenID Connect(OIDC) ID 공급자가 내장되어 있어 [웹 서비스](/ko/docs/modules/web)의 공유 사용자로 Gitea, OpenList, Grafana 등의 앱에 로그인할 수 있습니다. 앱은 표준 OIDC 프로토콜로 누가 로그인했는지(사용자 이름, 이메일, 그룹)를 알 수 있으므로 사람마다 각자의 계정과 권한을 만들 수 있습니다.

Authelia의 OIDC 기능을 대신합니다. 공유 사용자의 TOTP 2단계 인증과 통합 로그인 도메인을 함께 쓰면 하나의 계정으로 게이트웨이의 사이트와 연결된 앱 자체의 로그인을 모두 보호할 수 있습니다.

## 웹 인증과의 역할 분담

| | 웹 인증(공유 사용자) | OIDC 통합 로그인 |
| --- | --- | --- |
| 동작 방식 | Gately가 게이트웨이에서 로그인하지 않은 방문자를 막고, 로그인하면 통과시킴 | 앱이 사용자를 Gately 로그인으로 보낸 뒤 Gately에서 사용자 신원을 받아 옴 |
| 앱이 사용자를 구분할 수 있는지 | 요청이 통과되었다는 것만 앎 | 가능, 사용자 이름·이메일·그룹을 받음 |
| 적합한 대상 | 계정 체계가 없는 서비스, 관리 화면, 또는 문을 하나 더 두고 싶을 때 | 자체 계정 체계가 있고 OIDC / OAuth2 로그인을 지원하는 앱 |

두 가지를 함께 쓸 수 있습니다. 같은 공유 사용자로 사이트의 웹 인증도 통과하고 OIDC 앱에도 로그인할 수 있습니다.

## 준비

1. 「Web → 인증 사용자」 페이지에서 공유 사용자를 추가합니다. OIDC는 공유 사용자만 받으며, 사이트 자체 사용자와 서드파티 로그인 ID는 OIDC에 쓸 수 없습니다.
2. 필요에 따라 각 사용자의 이메일, 표시 이름, 그룹을 입력합니다.
   - 이메일: `email` 클레임으로 앱에 제공되며 인증된 것으로 표시됩니다. Gitea 등의 앱은 이를 기준으로 계정을 만들거나 연결합니다.
   - 표시 이름: `name` 클레임으로 제공되며, 비워 두면 사용자 이름을 씁니다.
   - 그룹: `groups` 클레임으로 제공되며, 앱에서 관리자로 매핑하거나 특정 앱에 로그인할 수 있는 사용자를 제한하는 데 쓸 수 있습니다.
3. `auth.example.com` 같은 발급자 도메인을 준비합니다. Gately가 있는 호스트로 해석되게 하고, [SSL 인증서](/ko/docs/modules/ssl)에서 이 도메인을 포함하는 인증서를 준비한 다음, HTTPS 리스너 중 하나가 이 도메인의 요청을 받는지 확인합니다. 이 도메인에는 사이트를 만들 필요가 없으며 Gately가 모든 리스너에서 OIDC 경로를 인식합니다. 별도 도메인을 쓰는 것을 권장하며, 이 도메인의 다른 경로는 평소대로 사이트에서 처리됩니다.

발급자 주소가 `https`이면 평문 HTTP로 OIDC 경로에 접근하는 GET 요청은 HTTPS로 리디렉션되고 그 밖의 요청은 거부되므로, 비밀번호와 토큰이 평문으로 전송되지 않습니다. 앞단에 리버스 프록시가 하나 더 있어 TLS를 종료하고 HTTP로 Gately에 전달하는 경우 프록시가 `X-Forwarded-Proto: https`를 붙여야 하며, Gately는 로컬 호스트와 내부망, 그리고 신뢰할 수 있는 PROXY 출처를 거쳐 들어온 연결의 이 헤더를 신뢰합니다. 예를 들어 공인 Gately가 TLS를 종료한 뒤 frp 터널을 통해 HTTP로 내부망 Gately에 전달한다면, 공인 사이트에서 "X-Forwarded-* 추가"와 "백엔드에 PROXY v2 전송"을 켜고, 내부망 Gately의 리스너에서 PROXY 프로토콜 수신을 켠 뒤 터널의 로컬 출발지 주소를 신뢰할 수 있는 PROXY 출처에 추가합니다. 발급자 도메인이 어떤 리스너에서 사이트(기본 사이트 포함)와 일치하면 그 사이트의 IP 허용·차단 목록, 구독 IP 목록, 국가 필터, 속도 제한이 OIDC 경로에도 적용되므로, 이를 이용해 로그인 페이지를 내부망으로 제한할 수 있습니다.

## 사용 설정

「Web → 인증 사용자」 페이지의 「OIDC ID 공급자」 카드에서 설정합니다.

| 필드 | 설명 |
| --- | --- |
| 사용 | 끄면 OIDC 경로가 더 이상 응답하지 않으며, 기존 클라이언트와 서명 키는 유지됩니다 |
| 발급자 주소 | 예: `https://auth.example.com`. 스킴, 도메인, 포트만 입력할 수 있습니다. 앱은 디스커버리 문서의 `issuer`가 설정된 주소와 정확히 일치하는지 확인하므로, 앱이 Gately에 접근할 때 쓰는 주소를 입력해야 하고 비표준 포트도 적어야 합니다 |

처음 저장할 때 Gately가 2048비트 RSA 서명 키를 자동으로 생성합니다. 이 키는 Web 서비스 설정 파일에 저장되어 설정과 함께 백업되며, API와 화면 어디에서도 반환하지 않습니다.

사용 설정 후 발급자 도메인에서 다음 주소를 제공합니다(화면에서 한 번에 복사할 수 있습니다).

| 용도 | 주소 |
| --- | --- |
| 디스커버리 문서 | `https://auth.example.com/.well-known/openid-configuration` |
| 인가 | `https://auth.example.com/__gately_oidc/authorize` |
| 토큰 | `https://auth.example.com/__gately_oidc/token` |
| 사용자 정보 | `https://auth.example.com/__gately_oidc/userinfo` |
| 공개 키(JWKS) | `https://auth.example.com/__gately_oidc/jwks` |
| 로그아웃 | `https://auth.example.com/__gately_oidc/logout` |

대부분의 앱은 디스커버리 문서 주소(Discovery URL, Auto Discovery URL이라고도 함)만 있으면 됩니다. 일부 앱은 발급자 주소(Issuer URL)를 요구하고, 소수의 앱은 엔드포인트를 하나씩 입력해야 합니다.

## 클라이언트 추가

연결하는 앱마다 클라이언트 하나가 대응합니다.

| 필드 | 설명 |
| --- | --- |
| 이름 | 로그인 페이지에 표시됩니다. 예: 「본인 확인 후 Gitea에 계속 로그인합니다」 |
| Client ID | 영문자, 숫자, `- . _ ~`만 쓸 수 있으며 앱에 입력합니다 |
| Client Secret | 16자 이상입니다. 「생성」을 누르면 43자리 무작위 키를 얻습니다. 저장 후에는 bcrypt 해시만 보관되어 다시 볼 수 없으니 저장 전에 앱에 복사하세요. 잃어버렸다면 다시 생성해 앱에 반영합니다. 기존 bcrypt 해시(`$2a$`, `$2b$` 또는 `$2y$`로 시작)를 직접 입력할 수도 있습니다 |
| 공개 클라이언트 | 싱글 페이지 앱, 모바일 앱처럼 시크릿을 보관할 수 없는 클라이언트는 이 항목을 체크합니다. 시크릿을 쓰지 않으며 PKCE(S256)가 필수이고, 리프레시 토큰은 발급하지 않습니다 |
| 리디렉션 URI | 한 줄에 하나씩 입력하며, 앱이 보내는 `redirect_uri`와 글자 그대로 비교합니다. 스킴, 포트, 경로, 쿼리 매개변수가 모두 같아야 합니다 |
| 허용 사용자 | 한 줄에 사용자 이름 하나 또는 `group:그룹명`을 입력하며, 어느 한 줄이라도 만족하면 로그인할 수 있습니다. 비워 두면 모든 공유 사용자가 대상입니다. 사용자 이름으로 일치시키므로 공유 사용자 이름을 바꾸면 여기도 함께 고쳐야 합니다 |

::: tip 리디렉션 URI를 모를 때
아무 주소나 입력해 저장한 뒤 앱에서 한 번 로그인해 봅니다. Gately가 「리디렉션 URI가 일치하지 않습니다」 오류 페이지에서 멈추고 앱이 실제로 보낸 주소를 보여 주므로, 그대로 복사해 클라이언트의 리디렉션 URI에 넣으면 됩니다.
:::

## 로그인 흐름

1. 사용자가 앱에서 「Gately로 로그인」 같은 버튼을 누르면 앱이 브라우저를 인가 주소로 보냅니다.
2. 이미 Gately에 로그인한 사용자는 바로 앱으로 돌아가고, 그렇지 않으면 Gately 로그인 페이지가 표시됩니다. TOTP를 쓰는 사용자는 일회용 인증 코드도 입력해야 합니다.
3. 앱은 백그라운드에서 인가 코드와 Client Secret으로 ID 토큰과 액세스 토큰을 받고, 사용자 정보를 읽어 로그인을 마칩니다.

발급자 도메인이 [통합 로그인 도메인](/ko/docs/modules/web#shared-users) 안에 있으면(예: 통합 로그인 도메인이 `example.com`, 발급자가 `auth.example.com`) 사용자가 공유 사용자로 보호되는 사이트 중 하나에 로그인한 뒤에는 OIDC 앱도 다시 로그인을 요구하지 않으며, 반대 방향도 마찬가지입니다.

클라이언트는 관리자가 등록하므로 Gately는 이를 신뢰할 수 있는 앱으로 보고 권한 동의 페이지를 표시하지 않습니다.

## scope와 클레임

| scope | 반환되는 클레임 |
| --- | --- |
| `openid`(필수) | `sub`: 공유 사용자의 고정 ID로, 무작위로 생성되며 이름을 바꿔도 변하지 않습니다. 사용자를 삭제한 뒤 같은 이름으로 다시 만들면 새 ID를 받으므로 원래 사용자의 앱 계정을 이어받지 않습니다 |
| `profile` | `name`(표시 이름, 비어 있으면 사용자 이름), `preferred_username`, `nickname`(사용자 이름) |
| `email` | `email`, `email_verified`(이메일을 입력한 경우에만 반환) |
| `groups` | `groups`: 그룹 목록 |
| `offline_access` | 리프레시 토큰을 추가로 발급(기밀 클라이언트만) |

ID 토큰에는 `iss`, `aud`, `azp`, `exp`, `iat`, `auth_time`, `nonce`, `at_hash`도 들어 있습니다. ID 토큰과 액세스 토큰은 모두 RS256으로 서명한 JWT입니다. 앱이 요청한 그 밖의 scope는 무시됩니다.

프로토콜 세부 사항: 인가 코드 방식(`response_type=code`)만 지원하며, 인가 결과는 쿼리 매개변수로 반환합니다(`response_mode=query`). 토큰 엔드포인트는 `client_secret_basic`과 `client_secret_post` 두 가지 인증 방식을 받으며, 공개 클라이언트는 `none`을 씁니다. PKCE는 `S256`만 지원합니다. `prompt=none`을 지원하며, 로그인하지 않은 경우 `login_required`를, 해당 앱 권한이 없으면 `access_denied`를 반환합니다. `prompt=login`이거나 로그인한 지 `max_age`가 지났으면 비밀번호를 다시 입력하게 합니다.

로그아웃 엔드포인트는 `id_token_hint`, `client_id`, `post_logout_redirect_uri`를 받습니다. `id_token_hint`는 이 발급자가 발급한 ID 토큰이어야 하며(만료되어도 됨), `post_logout_redirect_uri`는 해당 클라이언트에 등록한 리디렉션 URI 중 하나이거나 그중 하나와 출처(origin)가 같아야 합니다. 조건을 충족하지 않으면 로그아웃 후 로그인 페이지에 머뭅니다.

## 유효 기간과 무효화

- 인가 코드: 2분이며 한 번만 쓸 수 있습니다.
- ID 토큰, 액세스 토큰: 1시간입니다.
- 리프레시 토큰: 30일이며, 갱신할 때 기존 토큰을 그대로 씁니다.
- 공유 사용자의 비밀번호나 TOTP를 바꾸거나, 사용자 이름을 바꾸거나, 사용자를 삭제하거나, 사용자를 클라이언트의 허용 목록에서 빼거나, 클라이언트를 삭제하면 아직 교환하지 않은 인가 코드, 리프레시 토큰, 이미 발급된 액세스 토큰이 즉시 무효가 됩니다(사용자 정보 엔드포인트가 이전 액세스 토큰을 거부합니다). 앱 안에 이미 만들어진 로그인 상태는 앱이 직접 관리하므로, 필요하면 앱에서 해당 사용자를 로그아웃시키세요.
- 이메일, 표시 이름, 그룹을 바꾸면 사용자 정보 엔드포인트는 즉시 새 값을 반환하고, ID 토큰의 클레임은 다음 로그인이나 토큰 갱신 때 갱신됩니다. 허용 목록에서 그룹으로 권한을 준 경우 사용자를 그룹에서 빼면 즉시 반영됩니다.
- 서명 키는 Web 서비스 설정 파일 `webservice.json`의 `settings.oidc.signingKey`에 저장됩니다. 바꾸려면 Gately를 멈추고 이 필드를 삭제한 뒤 시작하면 새 키가 생성되며, 이미 발급된 모든 토큰은 무효가 됩니다.

## 앱 설정 예시

아래 예시는 발급자 주소를 `https://auth.example.com`으로 가정합니다. 자신의 주소로 바꾸세요.

### Gitea

Gately에서 클라이언트를 추가합니다. Client ID는 `gitea`로 입력하고, Client Secret을 생성하고, 리디렉션 URI는 다음과 같이 입력합니다.

```text
https://git.example.com/user/oauth2/gately/callback
```

여기서 `gately`는 아래 Gitea에서 입력하는 인증 소스 이름이며, 두 곳이 같아야 합니다.

Gitea의 「Site Administration → Authentication Sources → Add Authentication Source」에서 설정합니다.

| 필드 | 값 |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | Gately 클라이언트의 Client ID와 Client Secret |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups`(선택, 다음 항목과 함께 사용) |
| Group Claim value for administrator users. (Optional — requires claim name above) | 그룹 이름, 예: `admins`. 이 그룹의 사용자는 로그인하면 Gitea 관리자가 됩니다(선택) |

명령줄로 추가할 수도 있습니다.

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

처음 로그인할 때 Gitea 계정을 자동으로 만들려면 `app.ini`에 다음을 추가합니다.

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto`는 이메일이 같은 기존 Gitea 계정을 Gately 사용자와 자동으로 연결합니다. 자동 가입에는 이메일이 필요하므로 공유 사용자의 이메일을 입력하세요.

### OpenList

Gately에서 클라이언트를 추가합니다. Client ID는 `openlist`로 입력하고, Client Secret을 생성하고, 리디렉션 URI에는 다음 두 줄을 입력합니다(첫 줄은 로그인용, 둘째 줄은 프로필에서 기존 계정을 연결하는 용도).

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

OpenList의 「Manage → Settings → SSO」에서 설정합니다.

| 설정 항목 | 값 |
| --- | --- |
| SSO login enabled(`sso_login_enabled`) | 켜기 |
| SSO login platform(`sso_login_platform`) | `OIDC` |
| SSO client id / SSO client secret | Gately 클라이언트의 Client ID와 Client Secret |
| SSO endpoint name(`sso_endpoint_name`) | 발급자 주소 `https://auth.example.com`, `/.well-known/...`은 붙이지 않음 |
| SSO oidc username key(`sso_oidc_username_key`) | `preferred_username`. 기본값 `name`은 표시 이름을 가져옵니다 |
| SSO auto register(`sso_auto_register`) | 켜면 처음 로그인할 때 OpenList 사용자를 자동으로 만듭니다. 끄면 사용자가 먼저 OpenList 계정으로 로그인해 프로필에서 통합 로그인을 연결해야 합니다 |

저장하면 OpenList 로그인 페이지에 통합 로그인 버튼이 나타납니다.

### Grafana

Gately에서 클라이언트를 추가합니다. Client ID는 `grafana`로 입력하고, Client Secret을 생성하고, 리디렉션 URI는 `https://grafana.example.com/login/generic_oauth`로 입력합니다.

`grafana.ini`에서 설정합니다.

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

`role_attribute_path`는 `admins` 그룹의 사용자를 Grafana 관리자로, 나머지는 읽기 전용 사용자로 지정합니다.

### 기타 앱

OIDC 또는 「일반 OAuth2」 로그인을 지원하는 앱이라면 모두 연결할 수 있습니다. 흔한 입력 방식은 다음과 같습니다.

- 디스커버리 주소나 발급자 주소만 요구하는 경우: `https://auth.example.com/.well-known/openid-configuration` 또는 `https://auth.example.com`을 입력합니다.
- 엔드포인트를 하나씩 요구하는 경우: 위 「사용 설정」 절의 표에 따라 인가, 토큰, 사용자 정보, 공개 키 주소를 입력합니다.
- 사용자 이름 필드는 `preferred_username`, 이메일 필드는 `email`, 그룹 필드는 `groups`를 선택합니다.
- 리디렉션 URI는 앱의 문서나 설정 페이지에 있으며, 앞의 팁처럼 오류 페이지에서 복사할 수도 있습니다.

## 자주 묻는 질문

**앱이 issuer 불일치를 알립니다.** 발급자 주소는 `https`와 포트를 포함해 앱이 접근하는 주소와 정확히 같아야 합니다. 발급자 주소를 바꾼 뒤에는 일부 앱을 다시 시작해야 디스커버리 문서를 다시 읽습니다.

**로그인 후 「리디렉션 URI가 일치하지 않습니다」에서 멈춥니다.** 오류 페이지에 표시된 주소를 그대로 클라이언트의 리디렉션 URI에 추가하세요.

**로그인 후 「이 앱을 사용할 권한이 없습니다」가 표시됩니다.** 현재 사용자가 클라이언트의 허용 목록에 없습니다. 허용 목록을 조정하거나 페이지의 「다른 계정으로 로그인」을 눌러 로그아웃한 뒤 다른 공유 사용자로 로그인하세요. 로그인하면 원래의 인가가 이어집니다.

**앱이 `invalid_client`를 보고합니다.** Client Secret이 틀렸거나, 앱이 공개 클라이언트를 기밀 클라이언트로 사용하고 있습니다. Client Secret은 저장 전에만 볼 수 있으므로, 잃어버렸다면 다시 생성해 앱에 반영하세요. 토큰 엔드포인트의 인증 실패는 로그인 실패와 같은 속도 제한을 공유합니다. 같은 출처에서 10회 연속 실패하면 15분간 중지되며, 자동 차단을 켰다면 출처 IP도 차단됩니다.

**앱 서버가 발급자에 연결하지 못합니다.** 디스커버리 문서, 토큰, 공개 키는 브라우저를 거치지 않고 앱 서버가 직접 요청합니다. 앱이 있는 호스트나 컨테이너가 발급자 도메인을 해석하고 해당 포트에 접근할 수 있어야 합니다. 앱이 Docker에서 실행된다면 `extra_hosts`로 도메인을 호스트로 향하게 할 수 있습니다.

**앱이 `form_post`나 암시적(implicit) 방식을 요구합니다.** Gately는 인가 코드 방식만 지원하며 결과를 쿼리 매개변수로 반환합니다. 대부분의 앱은 기본적으로 이 방식을 쓰거나 설정에서 바꿀 수 있습니다.
