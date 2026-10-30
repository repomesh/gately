# Authentification unique OIDC

Gately intègre un fournisseur d'identité OpenID Connect (OIDC) qui permet aux utilisateurs partagés du [Service Web](/fr/docs/modules/web) de se connecter à Gitea, OpenList, Grafana et d'autres applications. Grâce au protocole OIDC standard, l'application sait qui s'est connecté (nom d'utilisateur, e-mail, groupes) et peut créer pour chacun son propre compte et ses propres droits.

Il couvre la fonction OIDC d'Authelia. Avec la vérification en deux étapes TOTP des utilisateurs partagés et le domaine SSO, un seul compte protège à la fois les sites de la passerelle et la connexion propre aux applications raccordées.

## Répartition des rôles avec l'authentification web

| | Authentification web (utilisateurs partagés) | Authentification unique OIDC |
| --- | --- | --- |
| Fonctionnement | Gately arrête sur la passerelle les visiteurs non connectés et les laisse passer après connexion | L'application envoie l'utilisateur se connecter sur Gately, puis récupère son identité auprès de Gately |
| L'application distingue-t-elle les utilisateurs ? | Elle sait seulement que la requête a été autorisée | Oui, elle reçoit le nom d'utilisateur, l'e-mail et les groupes |
| Adapté à | Services sans système de comptes, interfaces d'administration, ou simple porte supplémentaire | Applications dotées de leur propre système de comptes et prenant en charge la connexion OIDC / OAuth2 |

Les deux peuvent être utilisés ensemble : un même utilisateur partagé peut passer l'authentification web d'un site et se connecter aux applications OIDC.

## Préparation

1. Ajoutez des utilisateurs partagés sur la page « Web → Utilisateurs d'auth ». OIDC n'accepte que les utilisateurs partagés ; les utilisateurs propres à un site et les identités de connexion tierces ne peuvent pas servir pour OIDC.
2. Renseignez selon vos besoins l'e-mail, le nom affiché et les groupes de chaque utilisateur :
   - E-mail : fourni à l'application sous forme de claim `email` marqué comme vérifié ; Gitea et d'autres applications s'en servent pour créer ou associer des comptes ;
   - Nom affiché : fourni sous forme de claim `name` ; s'il est vide, le nom d'utilisateur est utilisé ;
   - Groupes : fournis sous forme de claim `groups` ; ils peuvent être associés au rôle d'administrateur dans l'application, ou servir à limiter les utilisateurs autorisés à se connecter à une application.
3. Préparez un domaine pour l'émetteur, par exemple `auth.example.com` : faites-le pointer vers l'hôte de Gately, préparez dans [Certificats SSL](/fr/docs/modules/ssl) un certificat qui le couvre, puis vérifiez qu'un listener HTTPS reçoit bien les requêtes de ce domaine. Inutile de créer un site pour ce domaine : Gately reconnaît ses chemins OIDC sur tous les listeners. Un domaine dédié est recommandé ; les autres chemins de ce domaine restent traités par les sites comme d'habitude.

Lorsque l'URL de l'émetteur est en `https`, les requêtes GET vers les chemins OIDC en HTTP non chiffré sont redirigées vers HTTPS et les autres requêtes sont refusées : mots de passe et jetons ne circulent jamais en clair. Si un autre proxy inverse placé devant termine TLS et transmet à Gately en HTTP, il doit ajouter `X-Forwarded-Proto: https` ; Gately fait confiance à cet en-tête lorsqu'il provient de la machine locale, du réseau interne ou d'une connexion arrivée par une source PROXY de confiance. Par exemple, si un Gately public termine TLS et transmet en HTTP à un Gately du réseau local via un tunnel frp, activez « Ajouter X-Forwarded-* » et « Envoyer PROXY v2 au backend » sur le site public, activez la réception du protocole PROXY sur le listener du Gately local et ajoutez l'adresse source locale du tunnel à ses sources PROXY de confiance. Lorsque le domaine de l'émetteur correspond à un site (site par défaut compris) sur un listener, la liste d'autorisation et la liste de blocage d'IP, les listes d'IP abonnées, le filtre de pays et la limitation de débit de ce site s'appliquent aussi aux chemins OIDC, ce qui permet de limiter la page de connexion au réseau interne.

## Activation

Dans la carte « Fournisseur d'identité OIDC » de la page « Web → Utilisateurs d'auth » :

| Champ | Description |
| --- | --- |
| Activer | Une fois désactivé, les chemins OIDC ne répondent plus ; les clients et la clé de signature existants sont conservés |
| URL de l'émetteur | Par exemple `https://auth.example.com` ; uniquement le schéma, le domaine et le port. Les applications vérifient que l'`issuer` du document de découverte correspond exactement à l'adresse configurée : indiquez donc l'adresse que les applications utilisent pour joindre Gately, y compris un port non standard |

Au premier enregistrement, Gately génère automatiquement une clé de signature RSA de 2048 bits. Elle est stockée dans le fichier de configuration du service Web, sauvegardée avec la configuration, et ni l'API ni l'interface ne la renvoient.

Une fois l'activation faite, le domaine de l'émetteur fournit les adresses suivantes (copiables en un clic dans l'interface) :

| Usage | Adresse |
| --- | --- |
| Document de découverte | `https://auth.example.com/.well-known/openid-configuration` |
| Autorisation | `https://auth.example.com/__gately_oidc/authorize` |
| Jeton | `https://auth.example.com/__gately_oidc/token` |
| Infos utilisateur | `https://auth.example.com/__gately_oidc/userinfo` |
| Clés publiques (JWKS) | `https://auth.example.com/__gately_oidc/jwks` |
| Déconnexion | `https://auth.example.com/__gately_oidc/logout` |

La plupart des applications n'ont besoin que de l'adresse du document de découverte (aussi appelée Discovery URL ou Auto Discovery URL) ; certaines demandent l'URL de l'émetteur (Issuer URL), et quelques-unes exigent de saisir chaque point de terminaison.

## Ajouter des clients

Chaque application raccordée correspond à un client :

| Champ | Description |
| --- | --- |
| Nom | Affiché sur la page de connexion, par exemple « Vérifiez votre identité pour continuer vers Gitea » |
| Client ID | Uniquement des lettres, des chiffres et `- . _ ~` ; à saisir dans l'application |
| Client Secret | Au moins 16 caractères ; cliquez sur « Générer » pour obtenir une clé aléatoire de 43 caractères. Après l'enregistrement, seul le hachage bcrypt est conservé et le secret ne peut plus être affiché : copiez-le dans l'application avant d'enregistrer. En cas de perte, régénérez-le et reportez-le dans l'application. Vous pouvez aussi saisir directement un hachage bcrypt existant (commençant par `$2a$`, `$2b$` ou `$2y$`) |
| Client public | À cocher pour les clients qui ne peuvent pas garder un secret, comme les applications monopages ou mobiles : pas de secret, PKCE (S256) obligatoire, aucun jeton d'actualisation émis |
| URI de redirection | Une par ligne, comparée caractère par caractère au `redirect_uri` envoyé par l'application : schéma, port, chemin et paramètres de requête doivent tous correspondre |
| Utilisateurs autorisés | Un nom d'utilisateur par ligne, ou `group:nom` ; il suffit de remplir l'une des lignes pour se connecter. Vide signifie tous les utilisateurs partagés. La correspondance se fait par nom d'utilisateur : après avoir renommé un utilisateur partagé, mettez aussi cette liste à jour |

::: tip Si vous ne connaissez pas l'URI de redirection
Enregistrez d'abord n'importe quelle adresse, puis lancez une connexion depuis l'application. Gately s'arrête sur la page d'erreur « URI de redirection non concordante », qui affiche l'adresse réellement envoyée par l'application ; copiez-la telle quelle dans l'URI de redirection du client.
:::

## Déroulement de la connexion

1. L'utilisateur clique dans l'application sur un bouton du type « Se connecter avec Gately », et l'application envoie le navigateur vers l'adresse d'autorisation ;
2. Si l'utilisateur est déjà connecté à Gately, il revient directement à l'application ; sinon la page de connexion de Gately s'affiche, et les utilisateurs ayant activé le TOTP saisissent aussi leur code de vérification ;
3. En arrière-plan, l'application échange le code d'autorisation et le Client Secret contre un ID Token et un Access Token, lit les informations de l'utilisateur et termine la connexion.

Lorsque le domaine de l'émetteur se trouve dans le [domaine SSO](/fr/docs/modules/web#shared-users) (par exemple domaine SSO `example.com` et émetteur `auth.example.com`), une fois l'utilisateur connecté sur n'importe quel site protégé par les utilisateurs partagés, les applications OIDC ne demandent plus de connexion, et inversement.

Les clients sont enregistrés par l'administrateur : Gately les considère comme des applications de confiance et n'affiche pas de page de consentement.

## scope et claims

| scope | Claims renvoyés |
| --- | --- |
| `openid` (obligatoire) | `sub` : identifiant fixe de l'utilisateur partagé, généré aléatoirement et conservé en cas de renommage ; un utilisateur recréé sous le même nom après suppression reçoit un nouvel identifiant et ne reprend pas le compte de l'ancien utilisateur dans l'application |
| `profile` | `name` (nom affiché, ou nom d'utilisateur s'il est vide), `preferred_username`, `nickname` (nom d'utilisateur) |
| `email` | `email`, `email_verified` (renvoyés seulement si un e-mail est renseigné) |
| `groups` | `groups` : liste des groupes |
| `offline_access` | Émet en plus un jeton d'actualisation, clients confidentiels uniquement |

L'ID Token contient aussi `iss`, `aud`, `azp`, `exp`, `iat`, `auth_time`, `nonce` et `at_hash`. L'ID Token et l'Access Token sont tous deux des JWT signés en RS256. Les autres scopes demandés par l'application sont ignorés.

Détails du protocole : seul le flux par code d'autorisation (`response_type=code`) est pris en charge, et le résultat de l'autorisation est renvoyé dans les paramètres de requête (`response_mode=query`) ; le point de terminaison de jeton accepte les méthodes d'authentification `client_secret_basic` et `client_secret_post`, et les clients publics utilisent `none` ; PKCE ne prend en charge que `S256` ; `prompt=none` est pris en charge et renvoie `login_required` en l'absence de connexion et `access_denied` si l'utilisateur n'a pas accès à cette application ; avec `prompt=login`, ou si la connexion date de plus de `max_age`, l'utilisateur doit ressaisir son mot de passe.

Le point de terminaison de déconnexion accepte `id_token_hint`, `client_id` et `post_logout_redirect_uri` : `id_token_hint` doit être un ID Token émis par cet émetteur (même expiré), et `post_logout_redirect_uri` doit être l'une des URI de redirection enregistrées pour le client ou avoir la même origine que l'une d'elles ; sinon, l'utilisateur reste sur la page de connexion après la déconnexion.

## Durées de validité et révocation

- Code d'autorisation : 2 minutes, à usage unique ;
- ID Token et Access Token : 1 heure ;
- Jeton d'actualisation : 30 jours ; le jeton d'origine est conservé lors de l'actualisation ;
- Après la modification du mot de passe ou du TOTP d'un utilisateur partagé, son renommage, la suppression de l'utilisateur, son retrait de la liste des utilisateurs autorisés d'un client ou la suppression du client, les codes d'autorisation non encore échangés, les jetons d'actualisation et les Access Tokens déjà émis sont immédiatement invalidés (le point de terminaison d'infos utilisateur refuse les anciens Access Tokens). La session déjà ouverte dans l'application est gérée par l'application elle-même ; au besoin, déconnectez cet utilisateur dans l'application ;
- Après la modification de l'e-mail, du nom affiché ou des groupes, le point de terminaison d'infos utilisateur renvoie aussitôt les nouvelles valeurs, et les claims de l'ID Token sont mis à jour à la prochaine connexion ou actualisation du jeton ; lorsque la liste des utilisateurs autorisés accorde l'accès par groupe, retirer l'utilisateur du groupe prend effet immédiatement ;
- La clé de signature est stockée dans `settings.oidc.signingKey` du fichier de configuration du service Web `webservice.json`. Pour la changer, arrêtez Gately, supprimez ce champ puis redémarrez : une nouvelle clé est générée et tous les jetons déjà émis deviennent invalides.

## Exemples de configuration d'applications

Les exemples ci-dessous supposent que l'URL de l'émetteur est `https://auth.example.com` ; remplacez-la par la vôtre.

### Gitea

Ajoutez un client dans Gately : Client ID `gitea`, générez un Client Secret et indiquez comme URI de redirection :

```text
https://git.example.com/user/oauth2/gately/callback
```

Ici, `gately` est le nom de la source d'authentification saisi plus bas dans Gitea ; les deux doivent être identiques.

Dans Gitea, sous « Site Administration → Authentication Sources → Add Authentication Source » :

| Champ | Valeur |
| --- | --- |
| Authentication Type | `OAuth2` |
| Authentication Name | `gately` |
| OAuth2 Provider | `OpenID Connect` |
| Client ID (Key) / Client Secret | Le Client ID et le Client Secret du client dans Gately |
| OpenID Connect Auto Discovery URL | `https://auth.example.com/.well-known/openid-configuration` |
| Additional Scopes | `profile email groups` |
| Claim name providing group names for this source. (Optional) | `groups` (facultatif, à utiliser avec le champ suivant) |
| Group Claim value for administrator users. (Optional — requires claim name above) | Nom de groupe, par exemple `admins` : les utilisateurs de ce groupe deviennent administrateurs de Gitea à la connexion (facultatif) |

L'ajout peut aussi se faire en ligne de commande :

```bash
gitea admin auth add-oauth --name gately --provider openidConnect --key gitea --secret '<Client Secret>' --auto-discover-url https://auth.example.com/.well-known/openid-configuration --scopes "openid profile email groups" --group-claim-name groups --admin-group admins
```

Pour créer automatiquement le compte Gitea à la première connexion, ajoutez dans `app.ini` :

```ini
[oauth2_client]
ENABLE_AUTO_REGISTRATION = true
USERNAME = preferred_username
ACCOUNT_LINKING = auto
```

`ACCOUNT_LINKING = auto` associe automatiquement à l'utilisateur Gately les comptes Gitea existants ayant le même e-mail. L'inscription automatique nécessite un e-mail : renseignez l'e-mail des utilisateurs partagés.

### OpenList

Ajoutez un client dans Gately : Client ID `openlist`, générez un Client Secret et indiquez comme URI de redirection les deux lignes suivantes (la première pour la connexion, la seconde pour associer un compte existant depuis le profil) :

```text
https://pan.example.com/api/auth/sso_callback?method=sso_get_token
https://pan.example.com/api/auth/sso_callback?method=sso_get_ssoid
```

Dans OpenList, sous « Manage → Settings → SSO » :

| Paramètre | Valeur |
| --- | --- |
| SSO login enabled (`sso_login_enabled`) | Activé |
| SSO login platform (`sso_login_platform`) | `OIDC` |
| SSO client id / SSO client secret | Le Client ID et le Client Secret du client dans Gately |
| SSO endpoint name (`sso_endpoint_name`) | L'URL de l'émetteur `https://auth.example.com`, sans `/.well-known/...` |
| SSO oidc username key (`sso_oidc_username_key`) | `preferred_username`. La valeur par défaut `name` prend le nom affiché |
| SSO auto register (`sso_auto_register`) | Activé, l'utilisateur OpenList est créé automatiquement à la première connexion ; désactivé, l'utilisateur doit d'abord se connecter avec son compte OpenList et associer l'authentification unique dans son profil |

Après l'enregistrement, la page de connexion d'OpenList affiche un bouton d'authentification unique.

### Grafana

Ajoutez un client dans Gately : Client ID `grafana`, générez un Client Secret et indiquez comme URI de redirection `https://grafana.example.com/login/generic_oauth`.

Dans `grafana.ini` :

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

`role_attribute_path` fait des utilisateurs du groupe `admins` des administrateurs Grafana, et des autres des utilisateurs en lecture seule.

### Autres applications

Toute application prenant en charge la connexion OIDC ou « OAuth2 générique » peut être raccordée. Configurations courantes :

- Si seule l'adresse de découverte ou de l'émetteur est demandée : indiquez `https://auth.example.com/.well-known/openid-configuration` ou `https://auth.example.com` ;
- Si chaque point de terminaison est demandé : renseignez les adresses d'autorisation, de jeton, d'infos utilisateur et de clés publiques d'après le tableau de la section « Activation » ;
- Pour le champ du nom d'utilisateur choisissez `preferred_username`, pour l'e-mail `email` et pour les groupes `groups` ;
- L'URI de redirection figure dans la documentation ou les paramètres de l'application ; vous pouvez aussi la copier depuis la page d'erreur, comme indiqué plus haut.

## Questions fréquentes

**L'application signale un issuer non concordant.** L'URL de l'émetteur doit correspondre exactement à l'adresse utilisée par l'application, `https` et port compris. Après une modification de l'URL de l'émetteur, certaines applications doivent être redémarrées pour relire le document de découverte.

**Après la connexion, la page reste sur « URI de redirection non concordante ».** Ajoutez telle quelle l'adresse affichée sur la page d'erreur aux URI de redirection du client.

**Après la connexion, « Pas d'autorisation pour cette application » s'affiche.** L'utilisateur actuel ne fait pas partie des utilisateurs autorisés du client. Modifiez les utilisateurs autorisés, ou cliquez sur « Se connecter avec un autre compte » sur la page pour vous déconnecter et vous connecter avec un autre utilisateur partagé ; l'autorisation d'origine reprend ensuite.

**L'application renvoie `invalid_client`.** Le Client Secret est incorrect, ou l'application utilise un client public comme un client confidentiel. Le Client Secret n'est visible qu'avant l'enregistrement ; en cas de perte, régénérez-le et reportez-le dans l'application. Les échecs d'authentification sur le point de terminaison de jeton partagent la limitation de débit des échecs de connexion : après 10 échecs consécutifs depuis la même origine, une pause de 15 minutes s'applique, et si le bannissement automatique est activé, l'IP d'origine est aussi bannie.

**Le serveur de l'application n'arrive pas à joindre l'émetteur.** Le document de découverte, les jetons et les clés publiques sont demandés directement par le serveur de l'application, sans passer par le navigateur. L'hôte ou le conteneur de l'application doit pouvoir résoudre le domaine de l'émetteur et joindre le port correspondant ; si l'application tourne dans Docker, vous pouvez utiliser `extra_hosts` pour faire pointer le domaine vers l'hôte.

**L'application exige `form_post` ou le flux implicite.** Gately ne prend en charge que le flux par code d'autorisation et renvoie le résultat dans les paramètres de requête. La plupart des applications utilisent ce mode par défaut ou permettent de le changer dans leurs paramètres.
