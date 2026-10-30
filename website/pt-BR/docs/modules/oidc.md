# Login único OIDC

O Gately inclui um provedor de identidade OpenID Connect (OIDC) que oferece os usuários compartilhados dos [Serviços Web](/pt-BR/docs/modules/web) para o login no Gitea, no OpenList, no Grafana e em outros aplicativos. Pelo protocolo padrão OIDC, o aplicativo fica sabendo quem entrou (nome de usuário, e-mail, grupos) e cria uma conta e permissões próprias para cada pessoa.

Isso cobre a função OIDC do Authelia. Junto com a verificação em dois fatores TOTP dos usuários compartilhados e o domínio de SSO, uma única conta protege tanto os sites no gateway quanto o login próprio dos aplicativos conectados.

## Divisão de papéis com a autenticação web

| | Autenticação web (usuários compartilhados) | Login único OIDC |
| --- | --- | --- |
| Como funciona | O Gately barra no gateway os visitantes que não entraram e os libera depois do login | O aplicativo leva o usuário para entrar no Gately e depois obtém a identidade dele no Gately |
| O aplicativo distingue os usuários | Só sabe que a requisição foi liberada | Sim, recebe nome de usuário, e-mail e grupos |
| Indicado para | Serviços sem sistema de contas próprio, painéis de administração ou para colocar uma porta a mais | Aplicativos com sistema de contas próprio que aceitam login por OIDC / OAuth2 |

Os dois podem ser usados ao mesmo tempo: o mesmo usuário compartilhado passa pela autenticação web de um site e também entra nos aplicativos OIDC.

## Preparação

1. Adicione usuários compartilhados na página "Web → Usuários de autenticação". O OIDC aceita apenas usuários compartilhados; os usuários próprios de um site e as identidades de login de terceiros não podem ser usados no OIDC.
2. Preencha, conforme necessário, o e-mail, o nome de exibição e os grupos de cada usuário:
   - E-mail: enviado ao aplicativo como a claim `email` e marcado como verificado; o Gitea e outros aplicativos criam ou vinculam contas a partir dele;
   - Nome de exibição: enviado como a claim `name`; quando vazio, usa-se o nome de usuário;
   - Grupos: enviados como a claim `groups`; podem ser mapeados para administradores no aplicativo ou usados para limitar quais usuários podem entrar em determinado aplicativo.
3. Prepare um domínio do emissor, por exemplo `auth.example.com`: ele precisa resolver para o host do Gately, ter em [Certificados SSL](/pt-BR/docs/modules/ssl) um certificado que o cubra e ter algum listener HTTPS que receba as requisições desse domínio. Não é preciso criar um site para esse domínio: o Gately reconhece os caminhos OIDC dele em todos os listeners. Recomenda-se um domínio exclusivo; os demais caminhos desse domínio continuam sendo tratados pelos sites normalmente.

Quando a URL do emissor usa `https`, as requisições GET aos caminhos OIDC por HTTP sem criptografia são redirecionadas para HTTPS e as demais são recusadas, assim senhas e tokens nunca trafegam em texto puro. Se houver outro proxy reverso na frente que termina o TLS e encaminha ao Gately por HTTP, ele precisa enviar `X-Forwarded-Proto: https`; o Gately confia nesse cabeçalho quando ele vem da própria máquina, da rede interna ou de uma conexão que chega por uma origem PROXY confiável. Por exemplo, quando um Gately público termina o TLS e encaminha por HTTP a um Gately da rede local por um túnel frp, ative "Adicionar X-Forwarded-*" e "Enviar PROXY v2 ao backend" no site público, ative o recebimento do protocolo PROXY no listener do Gately local e adicione o endereço de origem local do túnel às origens PROXY confiáveis. Quando o domínio do emissor corresponde a um site (incluindo o site padrão) em algum listener, a lista de IPs permitidos e bloqueados, as listas de IP assinadas, o filtro de países e a limitação de taxa desse site também valem para os caminhos OIDC, o que permite restringir a página de login à rede interna.

## Ativação

No cartão "Provedor de identidade OIDC" da página "Web → Usuários de autenticação":

| Campo | Descrição |
| --- | --- |
| Ativar | Quando desligado, os caminhos OIDC deixam de responder; os clientes existentes e a chave de assinatura são mantidos |
| URL do emissor | Por exemplo `https://auth.example.com`, apenas esquema, domínio e porta. Os aplicativos conferem se o `issuer` do documento de descoberta é idêntico ao endereço configurado, por isso informe o endereço que os aplicativos usam para acessar o Gately, incluindo uma porta não padrão |

No primeiro salvamento, o Gately gera automaticamente uma chave de assinatura RSA de 2048 bits. Ela fica no arquivo de configuração do serviço Web, entra no backup junto com a configuração e nunca é retornada pela API nem pela interface.

Depois de ativado, o domínio do emissor oferece os seguintes endereços (que podem ser copiados com um clique na interface):

| Uso | Endereço |
| --- | --- |
| Documento de descoberta | `https://auth.example.com/.well-known/openid-configuration` |
| Autorização | `https://auth.example.com/__gately_oidc/authorize` |
| Token | `https://auth.example.com/__gately_oidc/token` |
| Info. do usuário | `https://auth.example.com/__gately_oidc/userinfo` |
| Chaves públicas (JWKS) | `https://auth.example.com/__gately_oidc/jwks` |
| Sair | `https://auth.example.com/__gately_oidc/logout` |

A maioria dos aplicativos só precisa do endereço do documento de descoberta (também chamado de Discovery URL ou Auto Discovery URL); alguns pedem a URL do emissor (Issuer URL), e poucos exigem cada endpoint separadamente.

## Adicionar clientes

Cada aplicativo conectado corresponde a um cliente:

| Campo | Descrição |
| --- | --- |
| Nome | Exibido na página de login, por exemplo "Verifique sua identidade para continuar em Gitea" |
| Client ID | Apenas letras, números e `- . _ ~`; é informado no aplicativo |
| Client Secret | Pelo menos 16 caracteres; clique em "Gerar" para obter uma chave aleatória de 43 caracteres. Depois de salvar, apenas o hash bcrypt é guardado e o segredo não pode ser visto de novo, então copie-o para o aplicativo antes de salvar; se ele se perder, gere outro e atualize o aplicativo. Também é possível informar diretamente um hash bcrypt existente (começando com `$2a$`, `$2b$` ou `$2y$`) |
| Cliente público | Marque para clientes que não conseguem guardar um segredo, como aplicativos de página única e apps móveis: nenhum segredo é usado, o PKCE (S256) é obrigatório e nenhum refresh token é emitido |
| URIs de redirecionamento | Uma por linha; comparadas caractere a caractere com o `redirect_uri` enviado pelo aplicativo, e esquema, porta, caminho e parâmetros de consulta precisam ser iguais |
| Usuários permitidos | Um nome de usuário por linha, ou `group:nome`; basta atender a qualquer linha para entrar. Vazio significa todos os usuários compartilhados. A comparação é pelo nome de usuário, então ao renomear um usuário compartilhado atualize também esta lista |

::: tip Quando não souber a URI de redirecionamento
Preencha primeiro um endereço qualquer, salve e clique uma vez em entrar no aplicativo. O Gately vai parar na página de erro "URI de redirecionamento não confere", que mostra o endereço realmente enviado pelo aplicativo. Copie-o exatamente como está para as URIs de redirecionamento do cliente.
:::

## Fluxo de login

1. O usuário clica no aplicativo em um botão como "Entrar com o Gately", e o aplicativo leva o navegador ao endereço de autorização;
2. Se o usuário já entrou no Gately, volta direto ao aplicativo; caso contrário, aparece a página de login do Gately, e os usuários com TOTP ativado também digitam o código dinâmico;
3. Em segundo plano, o aplicativo troca o código de autorização e o Client Secret por um ID Token e um Access Token, lê as informações do usuário e conclui o login.

Quando o domínio do emissor está dentro do [domínio de SSO](/pt-BR/docs/modules/web#shared-users) (por exemplo, domínio de SSO `example.com` e emissor `auth.example.com`), depois que o usuário entra em qualquer site protegido por usuários compartilhados os aplicativos OIDC também deixam de pedir login, e vice-versa.

Os clientes são cadastrados pelo administrador; o Gately os trata como aplicativos confiáveis e não exibe página de consentimento.

## scope e claims

| scope | Claims retornadas |
| --- | --- |
| `openid` (obrigatório) | `sub`: ID fixo do usuário compartilhado, gerado aleatoriamente e mantido ao renomear; ao excluir o usuário e criar outro com o mesmo nome, ele recebe um novo ID e não assume a conta do usuário anterior no aplicativo |
| `profile` | `name` (nome de exibição, ou o nome de usuário se não preenchido), `preferred_username`, `nickname` (nome de usuário) |
| `email` | `email`, `email_verified` (só quando há e-mail preenchido) |
| `groups` | `groups`: lista de grupos |
| `offline_access` | Emite também um refresh token, somente para clientes confidenciais |

O ID Token contém ainda `iss`, `aud`, `azp`, `exp`, `iat`, `auth_time`, `nonce` e `at_hash`. O ID Token e o Access Token são JWTs assinados com RS256. Outros scopes solicitados pelo aplicativo são ignorados.

Detalhes do protocolo: só o fluxo de código de autorização é suportado (`response_type=code`), e o resultado volta em parâmetros de consulta (`response_mode=query`); o endpoint de token aceita os métodos de autenticação `client_secret_basic` e `client_secret_post`, e os clientes públicos usam `none`; o PKCE só suporta `S256`; `prompt=none` é suportado e retorna `login_required` quando não há login e `access_denied` quando o usuário não tem permissão para o aplicativo; com `prompt=login`, ou quando o login é mais antigo que `max_age`, o usuário precisa digitar a senha de novo.

O endpoint de logout aceita `id_token_hint`, `client_id` e `post_logout_redirect_uri`: `id_token_hint` precisa ser um ID Token emitido por este emissor (pode estar expirado), e `post_logout_redirect_uri` precisa ser uma das URIs de redirecionamento cadastradas para o cliente ou ter a mesma origem de uma delas; caso contrário, após o logout o usuário fica na página de login.

## Validade e revogação

- Código de autorização: 2 minutos, uso único;
- ID Token, Access Token: 1 hora;
- Refresh token: 30 dias; ao renovar, o token original continua sendo usado;
- Ao alterar a senha ou o TOTP de um usuário compartilhado, renomear ou excluir o usuário, retirá-lo da lista de usuários permitidos de um cliente ou excluir o cliente, os códigos de autorização ainda não trocados, os refresh tokens e os Access Tokens já emitidos deixam de valer na hora (o endpoint de informações do usuário recusa os Access Tokens antigos). As sessões já abertas dentro do aplicativo são gerenciadas pelo próprio aplicativo; quando necessário, encerre a sessão desse usuário no aplicativo;
- Depois de alterar o e-mail, o nome de exibição ou os grupos, o endpoint de informações do usuário retorna os novos valores na hora, e as claims do ID Token são atualizadas no próximo login ou na próxima renovação do token; quando a lista de usuários permitidos autoriza por grupo, retirar o usuário do grupo vale na hora;
- A chave de assinatura fica em `settings.oidc.signingKey` do arquivo de configuração `webservice.json` do serviço Web. Para trocá-la, pare o Gately, apague esse campo e inicie de novo: uma nova chave é gerada e todos os tokens emitidos deixam de valer.

## Exemplos de configuração de aplicativos

Os exemplos abaixo supõem a URL do emissor `https://auth.example.com`; substitua pelo seu endereço.

### Gitea

Adicione um cliente no Gately: Client ID `gitea`, gere o Client Secret e preencha a URI de redirecionamento com:

```text
https://git.example.com/user/oauth2/gately/callback
```

Aqui `gately` é o nome da fonte de autenticação que será preenchido no Gitea abaixo; os dois precisam ser iguais.

No Gitea, em "Site Administration → Authentication Sources → Add Authentication Source":

| Campo | Valor |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | Client ID e Client Secret do cliente no Gately |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups` (opcional, usado junto com o próximo campo) |
| Group Claim value for administrator users. (Optional — requires claim name above) | Nome do grupo, por exemplo `admins`: os usuários desse grupo viram administradores do Gitea ao entrar (opcional) |

Também é possível adicionar pela linha de comando:

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

Para criar automaticamente a conta do Gitea no primeiro login, adicione ao `app.ini`:

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` vincula automaticamente uma conta existente do Gitea com o mesmo e-mail ao usuário do Gately. O registro automático exige e-mail, então preencha o e-mail dos usuários compartilhados.

### OpenList

Adicione um cliente no Gately: Client ID `openlist`, gere o Client Secret e preencha as URIs de redirecionamento com as duas linhas abaixo (a primeira para o login, a segunda para vincular uma conta existente no perfil):

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

No OpenList, em "Manage → Settings → SSO":

| Configuração | Valor |
| --- | --- |
| SSO login enabled (`sso_login_enabled`) | Ligado |
| SSO login platform (`sso_login_platform`) | `OIDC` |
| SSO client id / SSO client secret | Client ID e Client Secret do cliente no Gately |
| SSO endpoint name (`sso_endpoint_name`) | URL do emissor `https://auth.example.com`, sem `/.well-known/...` |
| SSO oidc username key (`sso_oidc_username_key`) | `preferred_username`. O valor padrão `name` pega o nome de exibição |
| SSO auto register (`sso_auto_register`) | Ligado, cria automaticamente o usuário do OpenList no primeiro login; desligado, o usuário precisa primeiro entrar com a conta do OpenList e vincular o login único no perfil |

Depois de salvar, a página de login do OpenList passa a mostrar um botão de login único.

### Grafana

Adicione um cliente no Gately: Client ID `grafana`, gere o Client Secret e preencha a URI de redirecionamento com `https://grafana.example.com/login/generic_oauth`.

No `grafana.ini`:

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

`role_attribute_path` torna administradores do Grafana os usuários do grupo `admins`; os demais ficam somente leitura.

### Outros aplicativos

Qualquer aplicativo que aceite login por OIDC ou "OAuth2 genérico" pode ser conectado. Formas comuns de preenchimento:

- Se só pedir o endereço de descoberta ou a URL do emissor: informe `https://auth.example.com/.well-known/openid-configuration` ou `https://auth.example.com`;
- Se pedir cada endpoint separadamente: preencha os endereços de autorização, token, informações do usuário e chaves públicas conforme a tabela da seção "Ativação" acima;
- Escolha `preferred_username` como campo de nome de usuário, `email` como campo de e-mail e `groups` como campo de grupos;
- A URI de redirecionamento está na documentação ou na página de configurações do aplicativo; também dá para copiá-la da página de erro, como na dica acima.

## Perguntas frequentes

**O aplicativo diz que o issuer não confere.** A URL do emissor precisa ser idêntica ao endereço acessado pelo aplicativo, incluindo `https` e a porta. Depois de alterar a URL do emissor, alguns aplicativos precisam ser reiniciados para ler o documento de descoberta de novo.

**Depois do login, aparece "URI de redirecionamento não confere".** Adicione o endereço exibido na página de erro, exatamente como está, às URIs de redirecionamento do cliente.

**Depois do login, aparece "Sem permissão para usar este aplicativo".** O usuário atual não está entre os usuários permitidos do cliente. Ajuste os usuários permitidos ou clique em "Entrar com outra conta" na página para sair e entrar com outro usuário compartilhado; depois a autorização original continua.

**O aplicativo retorna `invalid_client`.** O Client Secret está errado, ou o aplicativo usa um cliente público como se fosse confidencial. O Client Secret só fica visível antes de salvar; se ele se perder, gere outro e atualize o aplicativo. As falhas de autenticação no endpoint de token compartilham o limite de tentativas com as falhas de login: depois de 10 falhas seguidas da mesma origem, há uma pausa de 15 minutos e, com o banimento automático ativado, o IP de origem também é banido.

**O servidor do aplicativo não consegue se conectar ao emissor.** O documento de descoberta, os tokens e as chaves públicas são buscados diretamente pelo servidor do aplicativo, sem passar pelo navegador. O host ou contêiner do aplicativo precisa resolver o domínio do emissor e acessar a porta correspondente; se o aplicativo roda no Docker, use `extra_hosts` para apontar o domínio para o host.

**O aplicativo exige `form_post` ou o fluxo implícito.** O Gately só suporta o fluxo de código de autorização e retorna o resultado em parâmetros de consulta. A maioria dos aplicativos já usa esse modo por padrão ou permite trocá-lo nas configurações.
