# OIDC single sign-on

Gately has a built-in OpenID Connect (OIDC) identity provider that lets Gitea, OpenList, Grafana and other applications sign users in with the shared users of [Web Services](/en/docs/modules/web). Through the standard OIDC protocol the application learns who signed in (username, email, groups), so it can give each person their own account and permissions.

It covers Authelia's OIDC features. Combined with TOTP two-step verification for shared users and the SSO domain, one set of accounts protects both the sites on the gateway and the applications' own sign-in.

## How it differs from web authentication

| | Web authentication (shared users) | OIDC single sign-on |
| --- | --- | --- |
| How it works | Gately stops visitors who are not signed in at the gateway and lets them through after sign-in | The application sends the user to Gately to sign in, then gets the user's identity from Gately |
| Can the application tell users apart | It only knows the request was let through | Yes, it receives the username, email and groups |
| Best for | Services without their own accounts, admin panels, or just adding one more gate | Applications with their own accounts that support OIDC / OAuth2 sign-in |

You can use both at the same time: the same shared user can pass a site's web authentication and sign in to OIDC applications.

## Preparation

1. Add shared users on the "Web → Auth users" page. OIDC only accepts shared users; a site's own users and third-party sign-in identities cannot be used for OIDC.
2. Fill in each user's email, display name and groups as needed:
   - Email: given to applications as the `email` claim and marked as verified; Gitea and other applications use it to create or link accounts;
   - Display name: the `name` claim; the username is used when it is empty;
   - Groups: the `groups` claim; applications can map it to administrators, and it can also limit which users may sign in to an application.
3. Prepare an issuer domain, such as `auth.example.com`: point it at the host running Gately, prepare a certificate covering it in [SSL Certificates](/en/docs/modules/ssl), and make sure an HTTPS listener receives requests for this domain. The domain needs no site; Gately recognizes its OIDC paths on every listener. A dedicated domain is recommended; other paths on this domain are handled by sites as usual.

When the issuer URL uses `https`, GET requests to the OIDC paths over plain HTTP are redirected to HTTPS and other requests are rejected, so passwords and tokens never travel in plain text. If another reverse proxy in front terminates TLS and forwards to Gately over HTTP, it must send `X-Forwarded-Proto: https`; Gately trusts this header from the local host, private networks, and connections that arrive through a trusted PROXY source. For example, when a public Gately terminates TLS and forwards to a LAN Gately over HTTP through an frp tunnel, turn on "Add X-Forwarded-*" and "Send PROXY v2 to the backend" on the public site, enable accepting the PROXY protocol on the LAN Gately's listener, and add the tunnel's local source address to its trusted PROXY sources. When the issuer domain matches a site (including the default site) on a listener, that site's IP allowlist and blocklist, subscribed IP lists, country filter and rate limiting also apply to the OIDC paths, which you can use to restrict the login page to your internal network.

## Enabling

In the "OIDC identity provider" card on the "Web → Auth users" page:

| Field | Description |
| --- | --- |
| Enable | When off, the OIDC paths no longer respond; existing clients and the signing key are kept |
| Issuer URL | Such as `https://auth.example.com`, with scheme, domain and port only. Applications check that the `issuer` in the discovery document matches the configured address exactly, so enter the address applications use to reach Gately, including a non-standard port |

On the first save Gately generates a 2048-bit RSA signing key. It is stored in the Web Service configuration file and backed up with the configuration, and neither the API nor the interface ever returns it.

Once enabled, the issuer domain serves the following addresses (each can be copied with one click in the interface):

| Purpose | Address |
| --- | --- |
| Discovery document | `https://auth.example.com/.well-known/openid-configuration` |
| Authorize | `https://auth.example.com/__gately_oidc/authorize` |
| Token | `https://auth.example.com/__gately_oidc/token` |
| User info | `https://auth.example.com/__gately_oidc/userinfo` |
| Public keys (JWKS) | `https://auth.example.com/__gately_oidc/jwks` |
| Sign out | `https://auth.example.com/__gately_oidc/logout` |

Most applications only need the discovery document address (also called Discovery URL or Auto Discovery URL). Some ask for the issuer address (Issuer URL), and a few need each endpoint entered separately.

## Adding clients

Each connected application has its own client:

| Field | Description |
| --- | --- |
| Name | Shown on the login page, for example "Verify your identity to continue to Gitea" |
| Client ID | Letters, digits and `- . _ ~` only; enter it in the application |
| Client Secret | At least 16 characters; click "Generate" for a random 43-character secret. Only its bcrypt hash is kept after saving and it cannot be shown again, so copy it into the application before saving; if lost, generate a new one and update the application. You can also enter an existing bcrypt hash (starting with `$2a$`, `$2b$` or `$2y$`) |
| Public client | Tick this for single-page apps, mobile apps and other clients that cannot keep a secret: no secret is used, PKCE (S256) is required and no refresh token is issued |
| Redirect URIs | One per line, compared character by character with the `redirect_uri` sent by the application; scheme, port, path and query must all match |
| Allowed users | One username per line, or `group:name`; matching any line allows sign-in. Leave empty to allow every shared user. Matching is by username, so update this list after renaming a shared user |

::: tip When you are not sure about the redirect URI
Save any address first, then click sign-in once in the application. Gately stops at the "Redirect URI mismatch" error page, which shows the address the application actually sent; copy it as is into the client's redirect URIs.
:::

## Sign-in flow

1. The user clicks a button such as "Sign in with Gately" in the application, and the application sends the browser to the authorize address;
2. If the user is already signed in to Gately, they go straight back to the application; otherwise Gately's login page is shown, and users with TOTP also enter a verification code;
3. In the background the application exchanges the authorization code and the Client Secret for an ID Token and an Access Token, reads the user info and completes sign-in.

When the issuer domain is inside the [SSO domain](/en/docs/modules/web#shared-users) (for example the SSO domain is `example.com` and the issuer is `auth.example.com`), a user who has signed in to any site protected by shared users is no longer asked to sign in by OIDC applications, and the other way round.

Clients are registered by the administrator, so Gately treats them as trusted applications and shows no consent page.

## Scopes and claims

| scope | Claims returned |
| --- | --- |
| `openid` (required) | `sub`: a fixed ID of the shared user, generated randomly and unchanged by renaming; a user created with the same name after deleting the old one gets a new ID and does not take over the old user's account in the application |
| `profile` | `name` (display name, or the username when empty), `preferred_username`, `nickname` (username) |
| `email` | `email`, `email_verified` (only when an email is set) |
| `groups` | `groups`: list of groups |
| `offline_access` | A refresh token is issued as well, confidential clients only |

The ID Token also contains `iss`, `aud`, `azp`, `exp`, `iat`, `auth_time`, `nonce` and `at_hash`. Both the ID Token and the Access Token are JWTs signed with RS256. Any other scope requested by the application is ignored.

Protocol details: only the authorization code flow is supported (`response_type=code`), and the result is returned in query parameters (`response_mode=query`); the token endpoint accepts `client_secret_basic` and `client_secret_post` authentication, and public clients use `none`; PKCE supports `S256` only; `prompt=none` is supported and returns `login_required` when the user is not signed in and `access_denied` when the user may not use the application; `prompt=login`, or a sign-in older than `max_age`, makes the user enter the password again.

The logout endpoint accepts `id_token_hint`, `client_id` and `post_logout_redirect_uri`: `id_token_hint` must be an ID Token issued by this issuer (expired tokens are accepted), and `post_logout_redirect_uri` must be one of the client's registered redirect URIs or share an origin with one of them; otherwise the user stays on the login page after signing out.

## Lifetimes and revocation

- Authorization code: 2 minutes, single use;
- ID Token, Access Token: 1 hour;
- Refresh token: 30 days; the same token is kept when refreshing;
- Changing a shared user's password or TOTP, renaming or deleting the user, removing the user from a client's allowed users or deleting the client immediately invalidates unredeemed authorization codes, refresh tokens and Access Tokens already issued (the user info endpoint rejects old Access Tokens). Sessions already established inside the application are managed by the application itself; sign the user out there when needed;
- After changing the email, display name or groups, the user info endpoint returns the new values immediately, and the claims in the ID Token are updated at the next sign-in or token refresh; when a client's allowed users grant access by group, removing the user from the group takes effect immediately;
- The signing key is stored in `settings.oidc.signingKey` of the Web Service configuration file `webservice.json`. To replace it, stop Gately, delete this field and start it again; a new key is generated and every token already issued becomes invalid.

## Application setup examples

The examples below assume the issuer address is `https://auth.example.com`; replace it with your own.

### Gitea

Add a client in Gately: enter `gitea` as the Client ID, generate a Client Secret, and use this redirect URI:

```text
https://git.example.com/user/oauth2/gately/callback
```

Here `gately` is the authentication source name entered in Gitea below; the two must match.

In Gitea, under "Site Administration → Authentication Sources → Add Authentication Source":

| Field | Value |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | The client's Client ID and Client Secret in Gately |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups` (optional, used with the next field) |
| Group Claim value for administrator users. (Optional — requires claim name above) | A group name, such as `admins`: users in this group become Gitea administrators after signing in (optional) |

You can also add it from the command line:

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

To create Gitea accounts automatically on first sign-in, add to `app.ini`:

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` links existing Gitea accounts with the same email to the Gately user automatically. Automatic registration needs an email, so fill in an email for each shared user.

### OpenList

Add a client in Gately: enter `openlist` as the Client ID, generate a Client Secret, and use these two redirect URIs (the first for sign-in, the second for linking an existing account in the profile):

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

In OpenList, under "Manage → Settings → SSO":

| Setting | Value |
| --- | --- |
| SSO login enabled (`sso_login_enabled`) | On |
| SSO login platform (`sso_login_platform`) | `OIDC` |
| SSO client id / SSO client secret | The client's Client ID and Client Secret in Gately |
| SSO endpoint name (`sso_endpoint_name`) | The issuer address `https://auth.example.com`, without `/.well-known/...` |
| SSO oidc username key (`sso_oidc_username_key`) | `preferred_username`. The default `name` takes the display name |
| SSO auto register (`sso_auto_register`) | When on, an OpenList user is created on first sign-in; when off, users first sign in with an OpenList account and link single sign-on in their profile |

After saving, the OpenList login page shows a single sign-on button.

### Grafana

Add a client in Gately: enter `grafana` as the Client ID, generate a Client Secret, and use the redirect URI `https://grafana.example.com/login/generic_oauth`.

In `grafana.ini`:

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

`role_attribute_path` makes users in the `admins` group Grafana administrators and everyone else viewers.

### Other applications

Any application that supports OIDC or "generic OAuth2" sign-in can be connected. Common ways to fill in the settings:

- Only a discovery or issuer address is required: enter `https://auth.example.com/.well-known/openid-configuration` or `https://auth.example.com`;
- Each endpoint must be entered: use the authorize, token, user info and public key addresses from the table in "Enabling" above;
- Choose `preferred_username` for the username field, `email` for the email field and `groups` for the groups field;
- The redirect URI is in the application's documentation or settings page, or copy it from the error page as described in the tip above.

## FAQ

**The application reports an issuer mismatch.** The issuer address must match the address the application uses exactly, including `https` and the port. After changing the issuer address, some applications need a restart to read the discovery document again.

**Sign-in stops at "Redirect URI mismatch".** Add the address shown on the error page as is to the client's redirect URIs.

**After sign-in it shows "Not allowed to use this application".** The current user is not in the client's allowed users. Adjust the allowed users, or click "Sign in with another account" on the page to sign out and sign in as another shared user; the original authorization continues afterwards.

**The application reports `invalid_client`.** The Client Secret is wrong, or the application uses a public client as a confidential client. The Client Secret is only visible before saving; if lost, generate a new one and update the application. Authentication failures at the token endpoint share the rate limit with sign-in failures: after 10 consecutive failures from the same source it pauses for 15 minutes, and with auto ban on the source IP is banned as well.

**The application server cannot reach the issuer.** The discovery document, tokens and public keys are requested directly by the application's server, not through the browser. The host or container running the application must resolve the issuer domain and reach its port; when the application runs in Docker, `extra_hosts` can point the domain at the host.

**The application requires `form_post` or the implicit flow.** Gately only supports the authorization code flow and returns the result in query parameters. Most applications use this by default or can switch to it in their settings.
