# Inicio de sesión único OIDC

Gately incluye un proveedor de identidad OpenID Connect (OIDC) que ofrece los usuarios compartidos de los [Servicios web](/es/docs/modules/web) para iniciar sesión en Gitea, OpenList, Grafana y otras aplicaciones. Mediante el protocolo OIDC estándar, la aplicación sabe quién ha iniciado sesión (nombre de usuario, correo y grupos), así que puede crear una cuenta y unos permisos propios para cada persona.

Cubre la función OIDC de Authelia. Junto con la verificación en dos pasos TOTP de los usuarios compartidos y el dominio de SSO, una sola cuenta protege a la vez los sitios del gateway y el inicio de sesión propio de las aplicaciones conectadas.

## Reparto de funciones con la autenticación web

| | Autenticación web (usuarios compartidos) | Inicio de sesión único OIDC |
| --- | --- | --- |
| Funcionamiento | Gately detiene en el gateway a los visitantes sin sesión y los deja pasar tras iniciar sesión | La aplicación lleva al usuario a iniciar sesión en Gately y luego obtiene de Gately su identidad |
| ¿Distingue la aplicación a los usuarios? | Solo sabe que la petición se ha dejado pasar | Sí, recibe el nombre de usuario, el correo y los grupos |
| Adecuado para | Servicios sin sistema de cuentas, paneles de administración, o cuando solo se quiere añadir una puerta más | Aplicaciones con su propio sistema de cuentas que admiten inicio de sesión OIDC / OAuth2 |

Ambos pueden usarse a la vez: el mismo usuario compartido puede pasar la autenticación web de un sitio e iniciar sesión en aplicaciones OIDC.

## Preparación

1. Añade usuarios compartidos en la página «Web → Usuarios de autenticación». OIDC solo acepta usuarios compartidos; los usuarios propios de un sitio y las identidades de inicio de sesión de terceros no sirven para OIDC.
2. Rellena, según necesites, el correo, el nombre visible y los grupos de cada usuario:
   - Correo: se entrega a la aplicación como claim `email` marcado como verificado; Gitea y otras aplicaciones lo usan para crear o vincular cuentas;
   - Nombre visible: se entrega como claim `name`; si está vacío se usa el nombre de usuario;
   - Grupos: se entregan como claim `groups`; en la aplicación pueden asignarse a administradores y también sirven para limitar qué usuarios pueden iniciar sesión en una aplicación.
3. Prepara un dominio para el emisor, por ejemplo `auth.example.com`: haz que resuelva al host donde está Gately, prepara en [Certificados SSL](/es/docs/modules/ssl) un certificado que lo cubra y comprueba que algún listener HTTPS recibe las peticiones de ese dominio. No hace falta crear un sitio para este dominio: Gately reconoce sus rutas OIDC en todos los listeners. Se recomienda un dominio dedicado; las demás rutas de ese dominio se siguen tratando como cualquier sitio.

Cuando la URL del emisor usa `https`, las peticiones GET a las rutas OIDC por HTTP sin cifrar se redirigen a HTTPS y las demás se rechazan, de modo que contraseñas y tokens nunca viajan en texto plano. Si delante hay otro proxy inverso que termina TLS y reenvía a Gately por HTTP, ese proxy debe enviar `X-Forwarded-Proto: https`; Gately confía en esta cabecera cuando llega desde el propio equipo, desde la red interna o por una conexión que entra a través de un origen PROXY de confianza. Por ejemplo, si un Gately público termina TLS y reenvía por HTTP a un Gately de la LAN a través de un túnel frp, activa «Añadir X-Forwarded-*» y «Enviar PROXY v2 al backend» en el sitio público, habilita la recepción del protocolo PROXY en el listener del Gately de la LAN y añade la dirección de origen local del túnel a sus orígenes PROXY de confianza. Cuando el dominio del emisor coincide con un sitio (incluido el sitio por defecto) en algún listener, las listas de IP permitidas y bloqueadas, las listas de IP suscritas, el filtro de países y el límite de peticiones de ese sitio también se aplican a las rutas OIDC, lo que permite limitar la página de inicio de sesión a la red interna.

## Activación

En la tarjeta «Proveedor de identidad OIDC» de la página «Web → Usuarios de autenticación»:

| Campo | Descripción |
| --- | --- |
| Activar | Al desactivarlo, las rutas OIDC dejan de responder; los clientes y la clave de firma existentes se conservan |
| URL del emisor | Por ejemplo `https://auth.example.com`; solo puede contener esquema, dominio y puerto. Las aplicaciones comprueban que el `issuer` del documento de descubrimiento coincide exactamente con la dirección configurada, así que indica la dirección que usan las aplicaciones para acceder a Gately, incluido el puerto si no es el estándar |

Al guardar por primera vez, Gately genera automáticamente una clave de firma RSA de 2048 bits. Se guarda en el archivo de configuración de Servicios web, se incluye en las copias de seguridad de la configuración y ni la API ni la interfaz la devuelven.

Una vez activado, el dominio del emisor ofrece estas direcciones (la interfaz permite copiarlas con un clic):

| Uso | Dirección |
| --- | --- |
| Documento de descubrimiento | `https://auth.example.com/.well-known/openid-configuration` |
| Autorización | `https://auth.example.com/__gately_oidc/authorize` |
| Token | `https://auth.example.com/__gately_oidc/token` |
| Info. de usuario | `https://auth.example.com/__gately_oidc/userinfo` |
| Claves públicas (JWKS) | `https://auth.example.com/__gately_oidc/jwks` |
| Cerrar sesión | `https://auth.example.com/__gately_oidc/logout` |

La mayoría de las aplicaciones solo necesitan la dirección del documento de descubrimiento (también llamada Discovery URL o Auto Discovery URL); algunas piden la URL del emisor (Issuer URL) y unas pocas exigen introducir cada endpoint.

## Añadir clientes

Cada aplicación conectada corresponde a un cliente:

| Campo | Descripción |
| --- | --- |
| Nombre | Se muestra en la página de inicio de sesión, por ejemplo «Verifica tu identidad para continuar en Gitea» |
| Client ID | Solo puede contener letras, números y `- . _ ~`; se introduce en la aplicación |
| Client Secret | Al menos 16 caracteres; pulsa «Generar» para obtener una clave aleatoria de 43 caracteres. Al guardar solo se conserva el hash bcrypt y no se puede volver a ver, así que cópialo en la aplicación antes de guardar; si lo pierdes, genéralo de nuevo y actualízalo en la aplicación. También puedes introducir directamente un hash bcrypt existente (que empiece por `$2a$`, `$2b$` o `$2y$`) |
| Cliente público | Márcalo para clientes que no pueden guardar un secreto, como aplicaciones de una sola página o apps móviles: no usan secreto, PKCE (S256) es obligatorio y no reciben token de actualización |
| URI de redirección | Una por línea; se compara carácter a carácter con el `redirect_uri` que envía la aplicación, así que esquema, puerto, ruta y parámetros de consulta deben coincidir |
| Usuarios permitidos | Un nombre de usuario por línea, o `group:nombre`; basta con cumplir cualquier línea para iniciar sesión. Vacío significa todos los usuarios compartidos. La comparación es por nombre de usuario, así que si renombras un usuario compartido actualiza también esta lista |

::: tip Si no sabes la URI de redirección
Guarda primero cualquier dirección y pulsa una vez iniciar sesión en la aplicación. Gately se detendrá en la página de error «La URI de redirección no coincide», que muestra la dirección que envió realmente la aplicación; cópiala tal cual en la URI de redirección del cliente.
:::

## Flujo de inicio de sesión

1. El usuario pulsa en la aplicación un botón como «Iniciar sesión con Gately» y la aplicación lleva el navegador a la dirección de autorización;
2. Si el usuario ya había iniciado sesión en Gately, vuelve directamente a la aplicación; si no, ve la página de inicio de sesión de Gately, y los usuarios con TOTP también introducen el código de verificación;
3. En segundo plano, la aplicación canjea el código de autorización y el Client Secret por un ID Token y un Access Token, lee la información del usuario y completa el inicio de sesión.

Cuando el dominio del emisor está dentro del [dominio de SSO](/es/docs/modules/web#shared-users) (por ejemplo, dominio de SSO `example.com` y emisor `auth.example.com`), tras iniciar sesión en cualquier sitio protegido por usuarios compartidos las aplicaciones OIDC tampoco piden iniciar sesión, y a la inversa.

Los clientes los registra el administrador, así que Gately los trata como aplicaciones de confianza y no muestra una página de consentimiento.

## scope y claims

| scope | Claims devueltos |
| --- | --- |
| `openid` (obligatorio) | `sub`: ID fijo del usuario compartido, generado al azar y que se mantiene al renombrarlo; si eliminas el usuario y creas otro con el mismo nombre, este recibe un ID nuevo y no hereda la cuenta del usuario anterior en la aplicación |
| `profile` | `name` (nombre visible, o el nombre de usuario si está vacío), `preferred_username`, `nickname` (nombre de usuario) |
| `email` | `email`, `email_verified` (solo si se ha indicado un correo) |
| `groups` | `groups`: lista de grupos |
| `offline_access` | Emite además un token de actualización, solo para clientes confidenciales |

El ID Token incluye también `iss`, `aud`, `azp`, `exp`, `iat`, `auth_time`, `nonce` y `at_hash`. Tanto el ID Token como el Access Token son JWT firmados con RS256. Los demás scopes que pida la aplicación se ignoran.

Detalles del protocolo: solo se admite el flujo de código de autorización (`response_type=code`), y el resultado de la autorización se devuelve en parámetros de consulta (`response_mode=query`); el endpoint de token acepta los métodos de autenticación `client_secret_basic` y `client_secret_post`, y los clientes públicos usan `none`; PKCE solo admite `S256`; se admite `prompt=none`, que devuelve `login_required` si no hay sesión iniciada y `access_denied` si el usuario no tiene permiso para esa aplicación; con `prompt=login`, o si el inicio de sesión es anterior a `max_age`, se pide al usuario que vuelva a introducir la contraseña.

El endpoint de cierre de sesión acepta `id_token_hint`, `client_id` y `post_logout_redirect_uri`: `id_token_hint` debe ser un ID Token emitido por este emisor (se aceptan caducados) y `post_logout_redirect_uri` debe ser una de las URI de redirección registradas para el cliente o tener el mismo origen que alguna de ellas; si no, tras cerrar sesión se queda en la página de inicio de sesión.

## Validez e invalidación

- Código de autorización: 2 minutos, de un solo uso;
- ID Token y Access Token: 1 hora;
- Token de actualización: 30 días; al refrescar se reutiliza el token original;
- Al cambiar la contraseña o el TOTP de un usuario compartido, renombrarlo, eliminarlo, quitarlo de la lista de usuarios permitidos de un cliente o eliminar el cliente, los códigos de autorización aún no canjeados, los tokens de actualización y los Access Tokens ya emitidos se invalidan al instante (el endpoint de información de usuario rechaza los Access Tokens antiguos). La sesión ya creada dentro de la aplicación la gestiona la propia aplicación; si hace falta, cierra la sesión de ese usuario en la aplicación;
- Tras cambiar el correo, el nombre visible o los grupos, el endpoint de información de usuario devuelve los valores nuevos al instante, y los claims del ID Token se actualizan en el siguiente inicio de sesión o al refrescar el token; si la lista de usuarios permitidos autoriza por grupo, sacar al usuario del grupo surte efecto al instante;
- La clave de firma se guarda en `settings.oidc.signingKey` del archivo de configuración de Servicios web `webservice.json`. Para cambiarla, detén Gately, elimina ese campo y vuelve a iniciarlo: se generará una clave nueva y todos los tokens emitidos dejarán de ser válidos.

## Ejemplos de configuración de aplicaciones

Los ejemplos siguientes suponen que la URL del emisor es `https://auth.example.com`; sustitúyela por la tuya.

### Gitea

Añade un cliente en Gately: Client ID `gitea`, genera un Client Secret y como URI de redirección indica:

```text
https://git.example.com/user/oauth2/gately/callback
```

Aquí `gately` es el nombre de la fuente de autenticación que se introduce más abajo en Gitea; ambos deben coincidir.

En Gitea, en «Site Administration → Authentication Sources → Add Authentication Source»:

| Campo | Valor |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | El Client ID y el Client Secret del cliente en Gately |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups` (opcional, se usa junto con el campo siguiente) |
| Group Claim value for administrator users. (Optional — requires claim name above) | Nombre de grupo, por ejemplo `admins`: los usuarios de ese grupo pasan a ser administradores de Gitea al iniciar sesión (opcional) |

También puede añadirse desde la línea de comandos:

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

Para crear automáticamente la cuenta de Gitea en el primer inicio de sesión, añade a `app.ini`:

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` vincula automáticamente con el usuario de Gately las cuentas de Gitea existentes que tengan el mismo correo. El registro automático necesita un correo, así que indica el correo de los usuarios compartidos.

### OpenList

Añade un cliente en Gately: Client ID `openlist`, genera un Client Secret y como URI de redirección indica estas dos líneas (la primera para iniciar sesión, la segunda para vincular una cuenta existente desde el perfil):

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

En OpenList, en «Manage → Settings → SSO»:

| Ajuste | Valor |
| --- | --- |
| SSO login enabled (`sso_login_enabled`) | Activado |
| SSO login platform (`sso_login_platform`) | `OIDC` |
| SSO client id / SSO client secret | El Client ID y el Client Secret del cliente en Gately |
| SSO endpoint name (`sso_endpoint_name`) | La URL del emisor `https://auth.example.com`, sin `/.well-known/...` |
| SSO oidc username key (`sso_oidc_username_key`) | `preferred_username`. El valor por defecto `name` toma el nombre visible |
| SSO auto register (`sso_auto_register`) | Activado, crea el usuario de OpenList en el primer inicio de sesión; desactivado, el usuario debe iniciar sesión primero con su cuenta de OpenList y vincular el inicio de sesión único en su perfil |

Tras guardar, la página de inicio de sesión de OpenList muestra un botón de inicio de sesión único.

### Grafana

Añade un cliente en Gately: Client ID `grafana`, genera un Client Secret y como URI de redirección indica `https://grafana.example.com/login/generic_oauth`.

En `grafana.ini`:

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

`role_attribute_path` hace administradores de Grafana a los usuarios del grupo `admins`, y al resto, usuarios de solo lectura.

### Otras aplicaciones

Puede conectarse cualquier aplicación que admita inicio de sesión OIDC o «OAuth2 genérico». Formas habituales de configurarla:

- Si solo pide la dirección de descubrimiento o la del emisor: indica `https://auth.example.com/.well-known/openid-configuration` o `https://auth.example.com`;
- Si pide cada endpoint: rellena las direcciones de autorización, token, información de usuario y claves públicas según la tabla de la sección «Activación»;
- Para el campo de nombre de usuario elige `preferred_username`, para el de correo `email` y para el de grupos `groups`;
- La URI de redirección figura en la documentación o en los ajustes de la aplicación; también puedes copiarla de la página de error, como se indica más arriba.

## Preguntas frecuentes

**La aplicación indica que el issuer no coincide.** La URL del emisor debe coincidir exactamente con la dirección a la que accede la aplicación, incluidos `https` y el puerto. Tras cambiar la URL del emisor, algunas aplicaciones necesitan reiniciarse para volver a leer el documento de descubrimiento.

**Tras iniciar sesión se queda en «La URI de redirección no coincide».** Añade tal cual la dirección que muestra la página de error a las URI de redirección del cliente.

**Tras iniciar sesión aparece «Sin permiso para usar esta aplicación».** El usuario actual no está en los usuarios permitidos del cliente. Ajusta los usuarios permitidos o pulsa «Iniciar sesión con otra cuenta» en la página para cerrar sesión y entrar con otro usuario compartido; después continúa la autorización original.

**La aplicación devuelve `invalid_client`.** El Client Secret es incorrecto, o la aplicación usa un cliente público como si fuera confidencial. El Client Secret solo es visible antes de guardar; si lo pierdes, genéralo de nuevo y actualízalo en la aplicación. Los fallos de autenticación en el endpoint de token comparten límite con los fallos de inicio de sesión: tras 10 fallos seguidos desde el mismo origen se pausa durante 15 minutos y, si el bloqueo automático está activado, también se bloquea la IP de origen.

**El servidor de la aplicación no puede conectar con el emisor.** El documento de descubrimiento, el token y las claves públicas los pide directamente el servidor de la aplicación, sin pasar por el navegador. El host o contenedor donde se ejecuta la aplicación debe poder resolver el dominio del emisor y acceder al puerto correspondiente; si la aplicación se ejecuta en Docker, puedes usar `extra_hosts` para apuntar el dominio al host.

**La aplicación exige `form_post` o el flujo implícito.** Gately solo admite el flujo de código de autorización y devuelve el resultado en parámetros de consulta. La mayoría de las aplicaciones usan este modo por defecto o permiten cambiarlo en sus ajustes.
