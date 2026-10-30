# OIDC-Single-Sign-on

Gately enthält einen eingebauten OpenID-Connect-Identitätsanbieter (OIDC), der die gemeinsamen Benutzer der [Web-Dienste](/de/docs/modules/web) für die Anmeldung bei Gitea, OpenList, Grafana und anderen Anwendungen bereitstellt. Über das Standardprotokoll OIDC erfahren die Anwendungen, wer sich angemeldet hat (Benutzername, E-Mail, Gruppen), und können so für jede Person ein eigenes Konto mit eigenen Berechtigungen anlegen.

Damit ist die OIDC-Funktion von Authelia abgedeckt. Zusammen mit der TOTP-Zwei-Faktor-Prüfung der gemeinsamen Benutzer und der SSO-Domain schützt ein einziges Konto sowohl die Sites am Gateway als auch die Anmeldung der angebundenen Anwendungen.

## Abgrenzung zur Web-Authentifizierung

| | Web-Authentifizierung (gemeinsame Benutzer) | OIDC-Single-Sign-on |
| --- | --- | --- |
| Funktionsweise | Gately hält nicht angemeldete Besucher am Gateway auf und lässt sie nach der Anmeldung durch | Die Anwendung schickt den Benutzer zur Anmeldung an Gately und holt dort seine Identität ab |
| Kann die Anwendung Benutzer unterscheiden | Sie weiß nur, dass die Anfrage durchgelassen wurde | Ja, sie erhält Benutzername, E-Mail und Gruppen |
| Geeignet für | Dienste ohne eigene Kontoverwaltung, Admin-Oberflächen oder als zusätzliche Hürde | Anwendungen mit eigener Kontoverwaltung, die Anmeldung per OIDC / OAuth2 unterstützen |

Beides lässt sich gleichzeitig nutzen: Derselbe gemeinsame Benutzer kann die Web-Authentifizierung einer Site passieren und sich bei OIDC-Anwendungen anmelden.

## Vorbereitung

1. Legen Sie auf der Seite „Web → Auth-Benutzer“ gemeinsame Benutzer an. OIDC akzeptiert nur gemeinsame Benutzer; die eigenen Benutzer einer Site und Identitäten aus der Anmeldung über Drittanbieter können nicht für OIDC verwendet werden.
2. Tragen Sie bei Bedarf für jeden Benutzer E-Mail, Anzeigename und Gruppen ein:
   - E-Mail: wird der Anwendung als Claim `email` übergeben und als verifiziert markiert; Gitea und andere Anwendungen legen darüber Konten an oder verknüpfen sie;
   - Anzeigename: wird als Claim `name` übergeben; ist er leer, wird der Benutzername verwendet;
   - Gruppen: werden als Claim `groups` übergeben; sie lassen sich in der Anwendung auf Administratoren abbilden oder dazu nutzen, festzulegen, welche Benutzer sich bei einer Anwendung anmelden dürfen.
3. Bereiten Sie eine Aussteller-Domain vor, zum Beispiel `auth.example.com`: Sie muss auf den Host von Gately auflösen, unter [SSL-Zertifikate](/de/docs/modules/ssl) muss ein Zertifikat bereitstehen, das sie abdeckt, und ein HTTPS-Listener muss Anfragen für diese Domain empfangen. Für die Domain ist keine Site nötig; Gately erkennt ihre OIDC-Pfade auf allen Listenern. Eine eigene Domain wird empfohlen; andere Pfade auf dieser Domain werden wie gewohnt von den Sites behandelt.

Verwendet die Aussteller-URL `https`, werden GET-Anfragen an die OIDC-Pfade über unverschlüsseltes HTTP auf HTTPS umgeleitet und andere Anfragen abgelehnt, sodass Passwörter und Tokens nie im Klartext übertragen werden. Beendet ein weiterer vorgeschalteter Reverse Proxy TLS und leitet per HTTP an Gately weiter, muss er `X-Forwarded-Proto: https` mitsenden; Gately vertraut diesem Header, wenn er vom lokalen Host, aus dem internen Netz oder über eine Verbindung von einer vertrauenswürdigen PROXY-Quelle kommt. Beendet etwa ein öffentliches Gately TLS und leitet per HTTP durch einen frp-Tunnel an ein Gately im LAN weiter, schalten Sie auf der öffentlichen Site „X-Forwarded-* hinzufügen“ und „PROXY v2 an das Backend senden“ ein, aktivieren am Listener des LAN-Gately die Annahme des PROXY-Protokolls und tragen die lokale Quelladresse des Tunnels als vertrauenswürdige PROXY-Quelle ein. Passt die Aussteller-Domain auf einem Listener zu einer Site (einschließlich der Standard-Site), gelten deren IP-Allowlist und -Blocklist, abonnierte IP-Listen, Länderfilter und Ratenbegrenzung auch für die OIDC-Pfade; so lässt sich die Anmeldeseite auf das interne Netz beschränken.

## Aktivieren

In der Karte „OIDC-Identitätsanbieter“ auf der Seite „Web → Auth-Benutzer“:

| Feld | Beschreibung |
| --- | --- |
| Aktivieren | Ist es ausgeschaltet, antworten die OIDC-Pfade nicht mehr; vorhandene Clients und der Signaturschlüssel bleiben erhalten |
| Aussteller-URL | Zum Beispiel `https://auth.example.com`, nur Schema, Domain und Port. Anwendungen prüfen, ob `issuer` im Discovery-Dokument exakt mit der konfigurierten Adresse übereinstimmt. Tragen Sie daher die Adresse ein, über die die Anwendungen Gately erreichen, einschließlich eines nicht standardmäßigen Ports |

Beim ersten Speichern erzeugt Gately automatisch einen 2048-Bit-RSA-Signaturschlüssel. Er wird in der Konfigurationsdatei des Web-Dienstes gespeichert, mit der Konfiguration gesichert und weder von der API noch von der Oberfläche zurückgegeben.

Nach dem Aktivieren stellt die Aussteller-Domain folgende Adressen bereit (in der Oberfläche mit einem Klick kopierbar):

| Zweck | Adresse |
| --- | --- |
| Discovery-Dokument | `https://auth.example.com/.well-known/openid-configuration` |
| Autorisierung | `https://auth.example.com/__gately_oidc/authorize` |
| Token | `https://auth.example.com/__gately_oidc/token` |
| Benutzerinfo | `https://auth.example.com/__gately_oidc/userinfo` |
| Öffentliche Schlüssel (JWKS) | `https://auth.example.com/__gately_oidc/jwks` |
| Abmelden | `https://auth.example.com/__gately_oidc/logout` |

Die meisten Anwendungen brauchen nur die Adresse des Discovery-Dokuments (auch Discovery URL oder Auto Discovery URL genannt). Manche verlangen die Aussteller-URL (Issuer URL), einige wenige die einzelnen Endpunkte.

## Clients hinzufügen

Jede angebundene Anwendung entspricht einem Client:

| Feld | Beschreibung |
| --- | --- |
| Name | Wird auf der Anmeldeseite angezeigt, zum Beispiel „Bestätigen Sie Ihre Identität, um mit Gitea fortzufahren“ |
| Client ID | Nur Buchstaben, Ziffern und `- . _ ~`; wird in der Anwendung eingetragen |
| Client Secret | Mindestens 16 Zeichen; „Erzeugen“ liefert einen zufälligen Schlüssel mit 43 Zeichen. Nach dem Speichern wird nur der bcrypt-Hash aufbewahrt, das Secret lässt sich nicht erneut anzeigen. Kopieren Sie es daher vor dem Speichern in die Anwendung; geht es verloren, erzeugen Sie ein neues und übernehmen es in die Anwendung. Sie können auch direkt einen vorhandenen bcrypt-Hash eintragen (beginnend mit `$2a$`, `$2b$` oder `$2y$`) |
| Öffentlicher Client | Für Clients, die kein Secret sicher speichern können, etwa Single-Page- und Mobil-Apps: Es wird kein Secret verwendet, PKCE (S256) ist Pflicht, und es werden keine Refresh Tokens ausgestellt |
| Weiterleitungs-URIs | Eine pro Zeile; wird Zeichen für Zeichen mit der von der Anwendung gesendeten `redirect_uri` verglichen, Schema, Port, Pfad und Query-Parameter müssen übereinstimmen |
| Erlaubte Benutzer | Ein Benutzername pro Zeile oder `group:Name`; wer eine beliebige Zeile erfüllt, darf sich anmelden. Leer bedeutet alle gemeinsamen Benutzer. Der Abgleich erfolgt über den Benutzernamen; nach dem Umbenennen eines gemeinsamen Benutzers muss diese Liste angepasst werden |

::: tip Wenn die Weiterleitungs-URI unklar ist
Tragen Sie zunächst eine beliebige Adresse ein, speichern Sie und klicken Sie in der Anwendung einmal auf Anmelden. Gately bleibt auf der Fehlerseite „Weiterleitungs-URI stimmt nicht überein“ stehen, die die tatsächlich von der Anwendung gesendete Adresse anzeigt. Kopieren Sie sie unverändert in die Weiterleitungs-URIs des Clients.
:::

## Anmeldeablauf

1. Der Benutzer klickt in der Anwendung auf eine Schaltfläche wie „Mit Gately anmelden“, und die Anwendung leitet den Browser zur Autorisierungsadresse;
2. Ist der Benutzer bereits bei Gately angemeldet, geht es direkt zurück zur Anwendung; andernfalls erscheint die Anmeldeseite von Gately, Benutzer mit aktiviertem TOTP geben zusätzlich einen Einmalcode ein;
3. Die Anwendung tauscht im Hintergrund den Autorisierungscode und das Client Secret gegen ID Token und Access Token, liest die Benutzerinfo und schließt die Anmeldung ab.

Liegt die Aussteller-Domain innerhalb der [SSO-Domain](/de/docs/modules/web#shared-users) (zum Beispiel SSO-Domain `example.com` und Aussteller `auth.example.com`), verlangen OIDC-Anwendungen keine Anmeldung mehr, sobald sich der Benutzer bei einer beliebigen durch gemeinsame Benutzer geschützten Site angemeldet hat, und umgekehrt.

Clients werden vom Administrator registriert; Gately betrachtet sie als vertrauenswürdige Anwendungen und zeigt keine Zustimmungsseite an.

## Scopes und Claims

| scope | Zurückgegebene Claims |
| --- | --- |
| `openid` (erforderlich) | `sub`: feste ID des gemeinsamen Benutzers, zufällig erzeugt und beim Umbenennen unverändert; wird ein Benutzer gelöscht und unter demselben Namen neu angelegt, erhält er eine neue ID und übernimmt das Konto des alten Benutzers in der Anwendung nicht |
| `profile` | `name` (Anzeigename, ohne Eintrag der Benutzername), `preferred_username`, `nickname` (Benutzername) |
| `email` | `email`, `email_verified` (nur wenn eine E-Mail eingetragen ist) |
| `groups` | `groups`: Liste der Gruppen |
| `offline_access` | Zusätzlich wird ein Refresh Token ausgestellt, nur für vertrauliche Clients |

Das ID Token enthält außerdem `iss`, `aud`, `azp`, `exp`, `iat`, `auth_time`, `nonce` und `at_hash`. ID Token und Access Token sind mit RS256 signierte JWTs. Andere von der Anwendung angeforderte Scopes werden ignoriert.

Protokolldetails: Unterstützt wird nur der Authorization-Code-Flow (`response_type=code`), das Ergebnis wird über Query-Parameter zurückgegeben (`response_mode=query`). Der Token-Endpunkt akzeptiert die Authentifizierungsmethoden `client_secret_basic` und `client_secret_post`, öffentliche Clients verwenden `none`. PKCE unterstützt nur `S256`. `prompt=none` wird unterstützt und liefert ohne Anmeldung `login_required`, ohne Berechtigung für die Anwendung `access_denied`. Bei `prompt=login` oder wenn die Anmeldung länger als `max_age` zurückliegt, muss der Benutzer sein Passwort erneut eingeben.

Der Abmelde-Endpunkt akzeptiert `id_token_hint`, `client_id` und `post_logout_redirect_uri`: `id_token_hint` muss ein von diesem Aussteller ausgestelltes ID Token sein (auch abgelaufen), und `post_logout_redirect_uri` muss eine der für den Client eingetragenen Weiterleitungs-URIs sein oder denselben Ursprung wie eine davon haben; andernfalls bleibt der Benutzer nach dem Abmelden auf der Anmeldeseite.

## Gültigkeit und Widerruf

- Autorisierungscode: 2 Minuten, nur einmal verwendbar;
- ID Token, Access Token: 1 Stunde;
- Refresh Token: 30 Tage; beim Aktualisieren wird das bisherige Token weiterverwendet;
- Wird das Passwort oder TOTP eines gemeinsamen Benutzers geändert, der Benutzer umbenannt, gelöscht, aus der Liste der erlaubten Benutzer eines Clients entfernt oder der Client gelöscht, werden noch nicht eingelöste Autorisierungscodes, Refresh Tokens und bereits ausgestellte Access Tokens sofort ungültig (der Benutzerinfo-Endpunkt weist alte Access Tokens zurück). Bestehende Anmeldesitzungen innerhalb einer Anwendung verwaltet die Anwendung selbst; melden Sie den Benutzer bei Bedarf in der Anwendung ab;
- Nach Änderungen an E-Mail, Anzeigename oder Gruppen liefert der Benutzerinfo-Endpunkt sofort die neuen Werte; die Claims im ID Token werden bei der nächsten Anmeldung oder Token-Aktualisierung aktualisiert. Gewährt die Liste der erlaubten Benutzer den Zugriff über eine Gruppe, wirkt das Entfernen des Benutzers aus der Gruppe sofort;
- Der Signaturschlüssel steht in `settings.oidc.signingKey` der Konfigurationsdatei `webservice.json` des Web-Dienstes. Um ihn zu wechseln, stoppen Sie Gately, löschen dieses Feld und starten neu; es wird ein neuer Schlüssel erzeugt, und alle ausgestellten Tokens werden damit ungültig.

## Konfigurationsbeispiele für Anwendungen

Die folgenden Beispiele gehen von der Aussteller-URL `https://auth.example.com` aus; ersetzen Sie sie durch Ihre eigene Adresse.

### Gitea

Fügen Sie in Gately einen Client hinzu: Client ID `gitea`, Client Secret erzeugen, als Weiterleitungs-URI eintragen:

```text
https://git.example.com/user/oauth2/gately/callback
```

`gately` ist dabei der Name der Authentifizierungsquelle, der unten in Gitea eingetragen wird; beide müssen übereinstimmen.

In Gitea unter „Site Administration → Authentication Sources → Add Authentication Source“:

| Feld | Wert |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | Client ID und Client Secret des Clients in Gately |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups` (optional, zusammen mit dem nächsten Feld) |
| Group Claim value for administrator users. (Optional — requires claim name above) | Gruppenname, zum Beispiel `admins`: Benutzer dieser Gruppe werden nach der Anmeldung Gitea-Administratoren (optional) |

Alternativ per Kommandozeile:

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

Sollen Gitea-Konten bei der ersten Anmeldung automatisch angelegt werden, ergänzen Sie in `app.ini`:

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` verknüpft vorhandene Gitea-Konten mit derselben E-Mail automatisch mit dem Gately-Benutzer. Die automatische Registrierung braucht eine E-Mail; tragen Sie deshalb bei den gemeinsamen Benutzern eine E-Mail ein.

### OpenList

Fügen Sie in Gately einen Client hinzu: Client ID `openlist`, Client Secret erzeugen, als Weiterleitungs-URIs die folgenden zwei Zeilen eintragen (die erste für die Anmeldung, die zweite für das Verknüpfen eines vorhandenen Kontos im Profil):

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

In OpenList unter „Manage → Settings → SSO“:

| Einstellung | Wert |
| --- | --- |
| SSO login enabled (`sso_login_enabled`) | Eingeschaltet |
| SSO login platform (`sso_login_platform`) | `OIDC` |
| SSO client id / SSO client secret | Client ID und Client Secret des Clients in Gately |
| SSO endpoint name (`sso_endpoint_name`) | Aussteller-URL `https://auth.example.com`, ohne `/.well-known/...` |
| SSO oidc username key (`sso_oidc_username_key`) | `preferred_username`. Der Standardwert `name` liefert den Anzeigenamen |
| SSO auto register (`sso_auto_register`) | Eingeschaltet wird bei der ersten Anmeldung automatisch ein OpenList-Benutzer angelegt; ausgeschaltet müssen sich Benutzer zuerst mit ihrem OpenList-Konto anmelden und Single Sign-on im Profil verknüpfen |

Nach dem Speichern erscheint auf der Anmeldeseite von OpenList eine Schaltfläche für Single Sign-on.

### Grafana

Fügen Sie in Gately einen Client hinzu: Client ID `grafana`, Client Secret erzeugen, als Weiterleitungs-URI `https://grafana.example.com/login/generic_oauth` eintragen.

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

`role_attribute_path` macht Benutzer der Gruppe `admins` zu Grafana-Administratoren, alle anderen erhalten nur Leserechte.

### Andere Anwendungen

Jede Anwendung, die die Anmeldung per OIDC oder „Generic OAuth2“ unterstützt, lässt sich anbinden. Übliche Angaben:

- Wird nur die Discovery-URL oder die Aussteller-URL verlangt: `https://auth.example.com/.well-known/openid-configuration` oder `https://auth.example.com` eintragen;
- Werden die Endpunkte einzeln verlangt: Autorisierungs-, Token-, Benutzerinfo- und Public-Key-Adresse gemäß der Tabelle im Abschnitt „Aktivieren“ oben eintragen;
- Als Benutzernamenfeld `preferred_username`, als E-Mail-Feld `email` und als Gruppenfeld `groups` wählen;
- Die Weiterleitungs-URI steht in der Dokumentation oder auf der Einstellungsseite der Anwendung; alternativ lässt sie sich wie im Hinweis oben von der Fehlerseite kopieren.

## Häufige Fragen

**Die Anwendung meldet, dass der issuer nicht übereinstimmt.** Die Aussteller-URL muss exakt mit der Adresse übereinstimmen, über die die Anwendung zugreift, einschließlich `https` und Port. Nach einer Änderung der Aussteller-URL müssen manche Anwendungen neu gestartet werden, bevor sie das Discovery-Dokument neu einlesen.

**Nach der Anmeldung erscheint „Weiterleitungs-URI stimmt nicht überein“.** Fügen Sie die auf der Fehlerseite angezeigte Adresse unverändert zu den Weiterleitungs-URIs des Clients hinzu.

**Nach der Anmeldung erscheint „Keine Berechtigung für diese Anwendung“.** Der aktuelle Benutzer gehört nicht zu den erlaubten Benutzern des Clients. Passen Sie die erlaubten Benutzer an oder klicken Sie auf der Seite auf „Mit einem anderen Konto anmelden“, um sich abzumelden und mit einem anderen gemeinsamen Benutzer anzumelden; danach wird die ursprüngliche Autorisierung fortgesetzt.

**Die Anwendung meldet `invalid_client`.** Das Client Secret ist falsch, oder die Anwendung verwendet einen öffentlichen Client als vertraulichen Client. Das Client Secret ist nur vor dem Speichern sichtbar; geht es verloren, erzeugen Sie ein neues und übernehmen es in die Anwendung. Fehlgeschlagene Authentifizierungen am Token-Endpunkt teilen sich die Ratenbegrenzung mit fehlgeschlagenen Anmeldungen: Nach 10 Fehlversuchen in Folge von derselben Quelle wird 15 Minuten pausiert, und bei aktivierter automatischer Sperre wird zusätzlich die Quell-IP gesperrt.

**Der Server der Anwendung erreicht den Aussteller nicht.** Discovery-Dokument, Token und öffentliche Schlüssel ruft der Server der Anwendung direkt ab, nicht der Browser. Der Host oder Container der Anwendung muss die Aussteller-Domain auflösen und den zugehörigen Port erreichen können. Läuft die Anwendung in Docker, können Sie die Domain mit `extra_hosts` auf den Host zeigen lassen.

**Die Anwendung verlangt `form_post` oder den Implicit Flow.** Gately unterstützt nur den Authorization-Code-Flow und gibt das Ergebnis über Query-Parameter zurück. Die meisten Anwendungen verwenden das standardmäßig oder lassen sich in den Einstellungen umstellen.
