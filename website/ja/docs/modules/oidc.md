# OIDC シングルサインオン

Gately には OpenID Connect（OIDC）の ID プロバイダーが組み込まれており、[Web サービス](/ja/docs/modules/web) の共有ユーザーで Gitea、OpenList、Grafana などのアプリにログインできます。アプリは標準の OIDC プロトコルでログインしたユーザー（ユーザー名、メール、グループ）を知り、一人ひとりに個別のアカウントと権限を用意できます。

Authelia の OIDC 機能をカバーします。共有ユーザーの TOTP 2 段階認証と SSO ドメインを組み合わせれば、1 組のアカウントでゲートウェイ上のサイトと、接続したアプリ自身のログインの両方を保護できます。

## Web 認証との役割分担

| | Web 認証（共有ユーザー） | OIDC シングルサインオン |
| --- | --- | --- |
| 仕組み | Gately がゲートウェイで未ログインの訪問者を止め、ログイン後に通す | アプリがユーザーを Gately のログインへ送り、Gately からユーザー情報を受け取る |
| アプリがユーザーを区別できるか | リクエストが通されたことしかわからない | できる。ユーザー名、メール、グループを受け取る |
| 向いている用途 | アカウント機能のないサービス、管理画面、門をもう一つ増やしたい場合 | 独自のアカウント機能を持ち、OIDC / OAuth2 ログインに対応したアプリ |

両方を同時に使えます。同じ共有ユーザーで、サイトの Web 認証を通過することも OIDC アプリにログインすることもできます。

## 準備

1. 「Web → 認証ユーザー」ページで共有ユーザーを追加します。OIDC で使えるのは共有ユーザーだけで、サイト独自のユーザーやサードパーティログインの ID は OIDC に使えません。
2. 必要に応じて、各ユーザーのメール、表示名、グループを入力します。
   - メール：`email` クレームとしてアプリに渡され、検証済みとしてマークされます。Gitea などのアプリはこれを使ってアカウントを作成または関連付けます。
   - 表示名：`name` クレームになります。空の場合はユーザー名を使います。
   - グループ：`groups` クレームになります。アプリ側で管理者に割り当てたり、アプリにログインできるユーザーを制限したりするのに使えます。
3. 発行者ドメイン（例：`auth.example.com`）を用意します。Gately が動いているホストを指すよう DNS を設定し、[SSL 証明書](/ja/docs/modules/ssl) でこのドメインを含む証明書を用意したうえで、いずれかの HTTPS リスナーがこのドメインへのリクエストを受け取れることを確認します。このドメインにサイトは不要で、Gately はすべてのリスナーで OIDC のパスを認識します。専用のドメインを推奨します。このドメインのほかのパスは通常どおりサイトで処理されます。

発行者 URL が `https` の場合、平文の HTTP で OIDC のパスにアクセスした GET リクエストは HTTPS へリダイレクトされ、それ以外のリクエストは拒否されるため、パスワードやトークンが平文で送られることはありません。前段にさらにリバースプロキシがあって TLS を終端し、HTTP で Gately に転送する場合は、プロキシが `X-Forwarded-Proto: https` を付ける必要があります。Gately は、ローカルホストと内部ネットワーク、および信頼済みの PROXY 送信元を経由した接続から届いたこのヘッダーを信頼します。たとえば公開側の Gately が TLS を終端し、frp トンネル経由で HTTP のまま LAN 側の Gately に転送する場合は、公開側のサイトで「X-Forwarded-* を付与」と「バックエンドへ PROXY v2 を送信」をオンにし、LAN 側 Gately のリスナーで PROXY プロトコルの受け付けを有効にして、トンネルのローカル側の送信元アドレスを信頼済みの PROXY 送信元に追加します。発行者ドメインがいずれかのリスナーでサイト（デフォルトサイトを含む）に一致する場合、そのサイトの IP のブラックリスト・ホワイトリスト、購読 IP リスト、国フィルター、レート制限は OIDC のパスにも適用されるため、これを使ってログインページを内部ネットワークに限定できます。

## 有効化

「Web → 認証ユーザー」ページの「OIDC ID プロバイダー」カードで設定します。

| フィールド | 説明 |
| --- | --- |
| 有効化 | オフにすると OIDC のパスは応答しなくなります。既存のクライアントと署名鍵は保持されます |
| 発行者 URL | 例：`https://auth.example.com`。指定できるのはスキーム、ドメイン、ポートのみです。アプリはディスカバリードキュメントの `issuer` が設定したアドレスと完全に一致するかを確認するため、アプリが Gately にアクセスするときのアドレスを入力します。標準以外のポートも含めてください |

初回保存時に Gately は 2048 ビットの RSA 署名鍵を自動生成します。鍵は Web サービスの設定ファイルに保存されて設定と一緒にバックアップされ、API や画面から返されることはありません。

有効化すると、発行者ドメインで次のアドレスが使えます（画面からワンクリックでコピーできます）。

| 用途 | アドレス |
| --- | --- |
| ディスカバリードキュメント | `https://auth.example.com/.well-known/openid-configuration` |
| 認可 | `https://auth.example.com/__gately_oidc/authorize` |
| トークン | `https://auth.example.com/__gately_oidc/token` |
| ユーザー情報 | `https://auth.example.com/__gately_oidc/userinfo` |
| 公開鍵（JWKS） | `https://auth.example.com/__gately_oidc/jwks` |
| ログアウト | `https://auth.example.com/__gately_oidc/logout` |

ほとんどのアプリはディスカバリードキュメントのアドレス（Discovery URL、Auto Discovery URL とも呼ばれます）だけで設定できます。発行者アドレス（Issuer URL）を求めるアプリもあり、少数のアプリでは各エンドポイントを個別に入力する必要があります。

## クライアントの追加

接続するアプリごとにクライアントを 1 つ作成します。

| フィールド | 説明 |
| --- | --- |
| 名前 | ログインページに表示されます。例：「本人確認後、Gitea へのログインを続行します」 |
| Client ID | 英字、数字、`- . _ ~` のみ使えます。アプリに入力します |
| Client Secret | 16 文字以上。「生成」を押すと 43 文字のランダムなシークレットが得られます。保存後は bcrypt ハッシュのみ保持され再表示できないため、保存前にアプリへコピーしてください。紛失した場合は再生成してアプリにも反映します。既存の bcrypt ハッシュ（`$2a$`、`$2b$` または `$2y$` で始まるもの）を直接入力することもできます |
| パブリッククライアント | シングルページアプリやモバイルアプリなど、シークレットを保管できないクライアントではオンにします。シークレットを使わず、PKCE（S256）が必須になり、リフレッシュトークンは発行されません |
| リダイレクト URI | 1 行に 1 つ。アプリが送る `redirect_uri` と 1 文字ずつ比較され、スキーム、ポート、パス、クエリパラメーターがすべて一致する必要があります |
| 許可するユーザー | 1 行に 1 つのユーザー名、または `group:グループ名`。いずれかの行に該当すればログインできます。空欄の場合はすべての共有ユーザーが対象です。ユーザー名で照合するため、共有ユーザーの名前を変更したらここも合わせて変更してください |

::: tip リダイレクト URI がわからないとき
まず適当なアドレスを入力して保存し、アプリでログインを 1 回試します。Gately は「リダイレクト URI が一致しません」のエラーページで止まり、アプリが実際に送ったアドレスが表示されるので、それをそのままクライアントのリダイレクト URI にコピーします。
:::

## ログインの流れ

1. ユーザーがアプリで「Gately でログイン」のようなボタンを押すと、アプリはブラウザを認可アドレスへ送ります。
2. ユーザーがすでに Gately にログインしていれば、そのままアプリに戻ります。そうでなければ Gately のログインページが表示され、TOTP を有効にしたユーザーはワンタイムコードも入力します。
3. アプリはバックグラウンドで認可コードと Client Secret を ID Token と Access Token に交換し、ユーザー情報を読み取ってログインを完了します。

発行者ドメインが [SSO ドメイン](/ja/docs/modules/web#shared-users) の範囲内にある場合（例：SSO ドメインが `example.com`、発行者が `auth.example.com`）、共有ユーザーで保護されたいずれかのサイトにログインすると、OIDC アプリでもログインを求められなくなります。逆も同様です。

クライアントは管理者が登録するため、Gately は信頼できるアプリとして扱い、同意画面は表示しません。

## scope とクレーム

| scope | 返されるクレーム |
| --- | --- |
| `openid`（必須） | `sub`：共有ユーザーの固定 ID。ランダムに生成され、名前を変更しても変わりません。ユーザーを削除して同じ名前のユーザーを作り直すと新しい ID になり、元のユーザーのアプリ内アカウントを引き継ぐことはありません |
| `profile` | `name`（表示名。未入力の場合はユーザー名）、`preferred_username`、`nickname`（ユーザー名） |
| `email` | `email`、`email_verified`（メールが入力されている場合のみ） |
| `groups` | `groups`：グループの一覧 |
| `offline_access` | リフレッシュトークンも発行（機密クライアントのみ） |

ID Token にはこのほか `iss`、`aud`、`azp`、`exp`、`iat`、`auth_time`、`nonce`、`at_hash` が含まれます。ID Token と Access Token はどちらも RS256 で署名された JWT です。アプリが要求したそれ以外の scope は無視されます。

プロトコルの詳細：対応するのは認可コードフロー（`response_type=code`）のみで、認可結果はクエリパラメーターで返します（`response_mode=query`）。トークンエンドポイントは `client_secret_basic` と `client_secret_post` の 2 つの認証方式を受け付け、パブリッククライアントは `none` を使います。PKCE は `S256` のみ対応です。`prompt=none` に対応しており、未ログインの場合は `login_required`、そのアプリの権限がない場合は `access_denied` を返します。`prompt=login` の場合やログインから `max_age` を超えている場合は、パスワードの再入力を求めます。

ログアウトエンドポイントは `id_token_hint`、`client_id`、`post_logout_redirect_uri` を受け付けます。`id_token_hint` はこの発行者が発行した ID Token である必要があり（期限切れでも可）、`post_logout_redirect_uri` はそのクライアントに登録したリダイレクト URI のいずれか、またはそのいずれかと同一オリジンである必要があります。条件を満たさない場合、ログアウト後はログインページにとどまります。

## 有効期間と無効化

- 認可コード：2 分、1 回のみ使用可能。
- ID Token、Access Token：1 時間。
- リフレッシュトークン：30 日。更新時も同じトークンを使い続けます。
- 共有ユーザーのパスワードや TOTP の変更、名前の変更、ユーザーの削除、クライアントの許可リストからの除外、クライアントの削除を行うと、未使用の認可コード、リフレッシュトークン、発行済みの Access Token は即座に無効になります（ユーザー情報エンドポイントは古い Access Token を拒否します）。アプリ内ですでに確立したログイン状態はアプリ自身が管理するため、必要に応じてアプリ側でそのユーザーをログアウトさせてください。
- メール、表示名、グループを変更すると、ユーザー情報エンドポイントはすぐに新しい値を返し、ID Token のクレームは次回のログインまたはトークン更新時に更新されます。許可リストでグループ単位で許可している場合、ユーザーをグループから外すとすぐに反映されます。
- 署名鍵は Web サービスの設定ファイル `webservice.json` の `settings.oidc.signingKey` に保存されています。交換するには Gately を停止し、このフィールドを削除してから起動します。新しい鍵が生成され、発行済みのトークンはすべて無効になります。

## アプリの設定例

以下の例では発行者アドレスを `https://auth.example.com` としています。ご自身のアドレスに置き換えてください。

### Gitea

Gately でクライアントを追加します。Client ID に `gitea` を入力し、Client Secret を生成し、リダイレクト URI に次を入力します。

```text
https://git.example.com/user/oauth2/gately/callback
```

ここでの `gately` は、後で Gitea に入力する認証ソース名です。両者を一致させる必要があります。

Gitea の「Site Administration → Authentication Sources → Add Authentication Source」で設定します。

| フィールド | 値 |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | Gately のクライアントの Client ID と Client Secret |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups`（任意。次の項目と組み合わせて使います） |
| Group Claim value for administrator users. (Optional — requires claim name above) | グループ名（例：`admins`）。このグループのユーザーはログイン後に Gitea の管理者になります（任意） |

コマンドラインで追加することもできます。

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

初回ログイン時に Gitea アカウントを自動作成するには、`app.ini` に次を追加します。

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` は、メールが同じ既存の Gitea アカウントを Gately のユーザーに自動で関連付けます。自動登録にはメールが必要なので、共有ユーザーにメールを入力してください。

### OpenList

Gately でクライアントを追加します。Client ID に `openlist` を入力し、Client Secret を生成し、リダイレクト URI に次の 2 行を入力します（1 行目はログイン用、2 行目はプロフィールで既存アカウントを連携する用）。

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

OpenList の「Manage → Settings → SSO」で設定します。

| 設定項目 | 値 |
| --- | --- |
| SSO login enabled（`sso_login_enabled`） | オン |
| SSO login platform（`sso_login_platform`） | `OIDC` |
| SSO client id / SSO client secret | Gately のクライアントの Client ID と Client Secret |
| SSO endpoint name（`sso_endpoint_name`） | 発行者アドレス `https://auth.example.com`。`/.well-known/...` は付けません |
| SSO oidc username key（`sso_oidc_username_key`） | `preferred_username`。デフォルトの `name` では表示名が使われます |
| SSO auto register（`sso_auto_register`） | オンにすると初回ログイン時に OpenList ユーザーが自動作成されます。オフの場合、ユーザーはまず OpenList アカウントでログインし、プロフィールでシングルサインオンを連携する必要があります |

保存すると、OpenList のログインページにシングルサインオンのボタンが表示されます。

### Grafana

Gately でクライアントを追加します。Client ID に `grafana` を入力し、Client Secret を生成し、リダイレクト URI に `https://grafana.example.com/login/generic_oauth` を入力します。

`grafana.ini` で次のように設定します。

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

`role_attribute_path` により、`admins` グループのユーザーは Grafana の管理者に、それ以外のユーザーは閲覧者になります。

### その他のアプリ

OIDC または「汎用 OAuth2」ログインに対応したアプリであれば接続できます。よくある入力方法は次のとおりです。

- ディスカバリーアドレスまたは発行者アドレスだけを求められる場合：`https://auth.example.com/.well-known/openid-configuration` または `https://auth.example.com` を入力します。
- 各エンドポイントの入力を求められる場合：上の「有効化」の表にある認可、トークン、ユーザー情報、公開鍵のアドレスを入力します。
- ユーザー名のフィールドには `preferred_username`、メールのフィールドには `email`、グループのフィールドには `groups` を選びます。
- リダイレクト URI はアプリのドキュメントや設定画面に記載されています。前述のヒントのとおりエラーページからコピーすることもできます。

## よくある質問

**アプリで issuer の不一致エラーが出る。** 発行者アドレスは、`https` やポートを含め、アプリがアクセスするアドレスと完全に一致している必要があります。発行者アドレスを変更した後、ディスカバリードキュメントを読み直すために再起動が必要なアプリもあります。

**ログイン後に「リダイレクト URI が一致しません」で止まる。** エラーページに表示されたアドレスをそのままクライアントのリダイレクト URI に追加します。

**ログイン後に「このアプリを使用する権限がありません」と表示される。** 現在のユーザーはクライアントの許可リストにありません。許可リストを調整するか、ページの「別のアカウントでログイン」をクリックしてログアウトし、別の共有ユーザーでログインしてください。ログイン後は元の認可が続行されます。

**アプリが `invalid_client` を返す。** Client Secret が正しくないか、アプリがパブリッククライアントをコンフィデンシャルクライアントとして使っています。Client Secret は保存前にしか表示されないため、紛失した場合は再生成してアプリにも反映します。トークンエンドポイントでの認証失敗はログイン失敗とレート制限を共有します。同じ送信元で 10 回連続して失敗すると 15 分間停止し、自動ブロックが有効な場合は送信元 IP もブロックされます。

**アプリのサーバーから発行者に接続できない。** ディスカバリードキュメント、トークン、公開鍵はアプリのサーバーが直接リクエストし、ブラウザは経由しません。アプリが動いているホストやコンテナが発行者ドメインを名前解決でき、対応するポートにアクセスできる必要があります。アプリが Docker で動いている場合は、`extra_hosts` でドメインをホストマシンに向けられます。

**アプリが `form_post` やインプリシットフローを要求する。** Gately は認可コードフローのみに対応し、結果をクエリパラメーターで返します。ほとんどのアプリはデフォルトでこの方式を使うか、設定で切り替えられます。
