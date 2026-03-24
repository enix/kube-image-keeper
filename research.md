# Rapport d'analyse : internal/controller/kuik

## Vue d'ensemble

Le package `internal/controller/kuik` contient les controllers Kubernetes (reconcilers) du projet kube-image-keeper v2. Il se compose de 10 fichiers Go organises autour de 3 controllers principaux, un controller generique et une couche de logique partagee.

---

## Fichiers et structure

| Fichier | Role |
|---|---|
| `clusterimagesetmirror_controller.go` | Reconciler pour ClusterImageSetMirror (cluster-scoped) |
| `imagesetmirror_controller.go` | Reconciler pour ImageSetMirror (namespaced) |
| `clusterimagesetavailability_controller.go` | Reconciler pour ClusterImageSetAvailability (monitoring) |
| `commonimagesetmirror.go` | Logique partagee entre les deux reconcilers mirror |
| `secretowner_controller.go` | Controller generique pour le nettoyage de Secrets |
| `field_names.go` | Constantes (finalizers, labels, annotations) |
| `suite_test.go` | Setup Ginkgo/envtest pour les tests |
| `*_controller_test.go` (x3) | Tests unitaires (squelettes Kubebuilder) |

---

## Controller 1 : ClusterImageSetMirrorReconciler

### Responsabilite
Reconcilie les ressources `ClusterImageSetMirror` (CISM). Detecte les images de containers utilisees dans le cluster entier, les filtre via `ImageFilter`, et les copie (mirror) vers des registries de destination.

### Flux de reconciliation

1. **Fetch** de la CISM et de tous les Pods du cluster
2. **Deletion** : si la CISM est en cours de suppression, nettoie les images mirrorees sur les registries de destination puis retire le finalizer `kuik.enix.io/mirror-cleanup`
3. **Finalizer** : ajoute le finalizer si absent (avec `RetryOnConflict`)
4. **Calcul des mirror prefixes** : appel a `getAllMirrorPrefixes(ctx, true)` pour eviter les boucles de mirroring (un mirror qui match ses propres images). Le parametre `true` ignore les namespaces, car les CISM sont cluster-scoped.
5. **Merge des images** : `mergePreviousAndCurrentMatchingImages` fusionne l'etat precedent (status) avec les images actuellement trouvees dans les Pods. Met a jour `UnusedSince` pour suivre l'obsolescence.
6. **Patch du status** (premiere fois) : ecrit la liste `MatchingImages` fusionnee
7. **Cleanup des images expirees** : pour chaque image marquee `UnusedSince` :
   - Si `Cleanup.Enabled` est false, on conserve
   - Si le delai de retention n'est pas ecoule, on planifie un requeue
   - Sinon, on supprime l'image du registry de destination via `cleanupMirror`
8. **Patch du status** (deuxieme fois) : ecrit la liste purgee
9. **Mirroring** : pour chaque image active (sans `UnusedSince`) dont `MirroredAt` est nil, effectue la copie via `mirrorImage`
10. **Patch du status** (par image) : apres chaque tentative de mirroring

### Watcher de Pods
Le controller observe les Pods via `WatchesRawSource` + `TypedKind`. Un mapper filtre les Pods : pour chaque Pod cree/modifie, il liste toutes les CISMs, et pour chacune, verifie si les images du Pod matchent le `ImageFilter`. Si oui, il enqueue une reconciliation de la CISM concernee.

### Options du controller
- `GenerationChangedPredicate` : ne reagit qu'aux changements de `.metadata.generation` (pas les updates de status)
- `newMirroringRateLimiter` : rate limiter custom (exponential backoff 1s-1000s + bucket 10 req/s burst 100)

---

## Controller 2 : ImageSetMirrorReconciler

### Responsabilite
Reconcilie les ressources `ImageSetMirror` (ISM), variante namespace-scoped des CISM. La logique est quasi identique a la CISM.

### Differences avec ClusterImageSetMirrorReconciler

| Aspect | CISM | ISM |
|---|---|---|
| Scope | Cluster | Namespace |
| Pods listes | Tous les Pods | Pods du namespace de l'ISM uniquement |
| Mirror prefixes | `getAllMirrorPrefixes(ctx, true)` (ignore namespaces) | `getAllMirrorPrefixes(ctx, false)` (respecte les namespaces) |
| Finalizer update | Avec `RetryOnConflict` | Sans `RetryOnConflict` (risque potentiel de conflit) |
| Pod mapper | Liste toutes les CISMs | Liste les ISMs du namespace du Pod |

### Observation notable
Le code de reconciliation entre CISM et ISM est tres similaire (quasi duplique). La logique partagee est dans `commonimagesetmirror.go`, mais le code de reconciliation lui-meme (gestion des finalizers, boucle cleanup, boucle mirroring) est copie dans les deux fichiers. On note une asymetrie : le CISM utilise `RetryOnConflict` pour l'ajout/retrait de finalizers, mais pas l'ISM (sauf pour le retrait via la base commune, qui est aussi sans retry dans l'ISM).

---

## Controller 3 : ClusterImageSetAvailabilityReconciler

### Responsabilite
Reconcilie les ressources `ClusterImageSetAvailability` (CISA). Ce controller est dedie au **monitoring de la disponibilite** des images de containers dans les registries sources (pas les mirrors). Il verifie periodiquement si les images sont toujours accessibles.

### Flux de reconciliation

1. **Fetch** de la CISA et de tous les Pods
2. **syncImageList** : synchronise la liste `Status.Images` avec les images reellement utilisees :
   - Decouvre les nouvelles images depuis les Pods (via `normalizedImageNamesFromPod` + `ImageFilter`)
   - Met a jour `UnusedSince` pour les images plus utilisees
   - Marque pour suppression immediate les images hors-scope (filtre change) ou jamais monitorees et plus utilisees (`instantExpiryMarker` = `0001-01-01 01:00:00`)
   - Supprime les images dont `UnusedSince` depasse `UnusedImageExpiry`
3. **getRegistriesCandidates** : selectionne un candidat par registry. Pour chaque registry, identifie l'image dont le `LastMonitor` est le plus ancien (priorite aux images jamais monitorees). Enregistre aussi le `registryLastMonitor` (dernier check sur ce registry) pour le rate limiting.
4. **checkNextForRegistry** : pour chaque registry, calcule si un check est "du" en comparant le temps ecoule depuis le dernier check avec `tickDuration = Interval / MaxPerInterval`. Si le delai est respecte, effectue le check via `performCheck`.
5. **Requeue** : calcule le `minRequeueAfter` parmi tous les registries pour planifier la prochaine reconciliation. Gere le cas d'un informer cache stale (aucun candidat mais des images existantes) en requeuant immediatement.

### Rate limiting par registry
La configuration est chargee depuis `config.Config.Monitoring.Registries` avec un mecanisme de merge :
- `Default` : configuration par defaut pour tous les registries
- `Items[registry]` : override par registry (method, interval, maxPerInterval, timeout, fallbackCredentialSecret)

Le `tickDuration` (= `Interval / MaxPerInterval`) definit l'intervalle minimum entre deux checks sur un meme registry. Cela permet de limiter le nombre de requetes par periode de temps.

### performCheck
- Resout les credentials (pull secrets des Pods, ou fallback credential secret)
- Appelle `registry.CheckImageAvailability` avec la methode configuree et le timeout
- Met a jour le status de l'image (`Available`, `NotFound`, `Unreachable`, `InvalidAuth`, `UnavailableSecret`, `QuotaExceeded`)
- Gere un cas special : si le secret est introuvable ET que le registry renvoie `InvalidAuth`, `UnavailableSecret` prend precedence

### resolveCredentials
Strategie de resolution en cascade :
1. Cherche un Pod utilisant l'image et ayant des `ImagePullSecrets`
2. Si aucun Pod trouve ou erreur, utilise le `FallbackCredentialSecret` de la config du registry
3. Si aucun fallback, renvoie nil (acces anonyme) ou l'erreur accumulee

---

## Logique partagee : commonimagesetmirror.go

### ImageSetMirrorBaseReconciler
Struct de base embeddee par les deux reconcilers mirror. Fournit :

#### Gestion des credentials
- `getPullSecret` : lit un Secret Kubernetes par namespace/name
- `getPullSecretsFromPods` : recueille les pull secrets du Pod qui utilise une image donnee
- `getImageSecretFromMirrors` : recupere le credential secret configure dans la spec Mirror pour une image de destination

#### Operations sur les registries
- `mirrorImage` : copie une image source vers une destination. Logique de fallback : si la copie echoue, verifie si l'image existe deja a destination (via `GetDescriptor`). Si oui, considere le mirroring comme reussi. La copie est filtree pour l'architecture `amd64` uniquement.
- `cleanupMirror` : supprime une image d'un registry de destination. Ne fait rien si aucun credential n'est configure.

#### Detection des images
- `normalizedImageNamesFromPod` : extrait les noms d'images normalises d'un Pod. Sources : containers + init containers + annotation `kuik.enix.io/original-images` (images avant rewrite par le webhook). Ignore les images basees sur un digest (`@sha256:...`). Renvoie un `iter.Seq[string]` (Go iterators).
- `podsByNormalizedMatchingImages` : filtre les images des Pods via un `filter.Filter`, en excluant les images dont le prefix correspond a un mirror (prevention de boucle). Retourne un map image -> Pod (le dernier Pod rencontre l'emporte).

#### Gestion du cycle de vie des images
- `mergePreviousAndCurrentMatchingImages` : fusionne les images du status existant avec celles trouvees dans les Pods. Construit les `MirrorStatus` attendus pour chaque image.
- `updateUnusedSince` : met a jour le champ `UnusedSince` des images dans le status :
  - Image plus matchee par le filtre : `UnusedSince` = `0001-01-01 01:00:00` (suppression instantanee)
  - Image matchee mais plus utilisee par aucun Pod : `UnusedSince` = now
  - Image toujours en usage : `UnusedSince` = nil
  - Fusionne aussi les mirrors via `mergeMirrors`
- `mergeMirrors` : ajoute les mirrors attendus manquants dans la liste actuelle. Ne supprime pas les mirrors obsoletes (FIXME dans le code).

#### Autres
- `getAllMirrorPrefixes` : liste tous les prefixes de mirrors (depuis toutes les CISMs et ISMs) pour eviter les boucles de mirroring
- `newMirroringRateLimiter` : rate limiter custom pour les queues de reconciliation

---

## Controller generique : SecretOwnerReconciler[T]

### Responsabilite
Controller generique parametre par un type `T client.Object`. Gere le cycle de vie d'un finalizer `kuik.enix.io/secret-cleanup` sur les ressources qui possedent des Secrets.

### Flux
1. **Deletion** : si le finalizer est present, appelle `CleanupOwnedSecrets` puis retire le finalizer
2. **Normal** : ajoute le finalizer si absent (avec `RetryOnConflict`)

### CleanupOwnedSecrets
Liste tous les Secrets ayant le label `kuik.enix.io/owner-uid` correspondant a l'UID de la ressource proprietaire, puis les supprime un par un.

### Setup
Construit dynamiquement le nom du controller a partir du GVK de `T` : `kuik-secretowner-<Kind>`.

---

## Constantes (field_names.go)

| Constante | Valeur | Usage |
|---|---|---|
| `cleanupFinalizer` | `kuik.enix.io/secret-cleanup` | Finalizer pour le nettoyage de Secrets |
| `imageSetMirrorFinalizer` | `kuik.enix.io/mirror-cleanup` | Finalizer pour le nettoyage des images mirrorees |
| `OwnerVersionLabel` | `kuik.enix.io/owner-version` | Label pour tracker la version de l'owner |
| `OwnerGroupLabel` | `kuik.enix.io/owner-group` | Label pour tracker le group de l'owner |
| `OwnerKindLabel` | `kuik.enix.io/owner-kind` | Label pour tracker le kind de l'owner |
| `OwnerUIDLabel` | `kuik.enix.io/owner-uid` | Label pour tracker l'UID de l'owner |
| `OwnerNameLabel` | `kuik.enix.io/owner-name` | Label pour tracker le nom de l'owner |
| `OriginalImagesAnnotation` | `kuik.enix.io/original-images` | Annotation contenant les images originales avant rewrite |

---

## Tests

Les tests sont des squelettes generes par Kubebuilder (pattern BDD Ginkgo/Gomega). Ils utilisent envtest pour simuler un API server Kubernetes. Chaque test :
1. Cree la ressource CRD
2. Lance un `Reconcile` et verifie l'absence d'erreur
3. Nettoie la ressource

Les tests ne couvrent pas encore les cas metier reels (mirroring effectif, cleanup, monitoring). Ils sont marques de commentaires `TODO(user)`.

Le `suite_test.go` configure l'environnement envtest avec les CRDs depuis `config/crd/bases/` et gere la detection automatique des binaires envtest pour l'execution depuis un IDE.

---

## Specificites et observations

### Architecture
- **Composition plutot qu'heritage** : les reconcilers mirror embeddent `ImageSetMirrorBaseReconciler`, et `SecretOwnerReconciler` utilise des generics Go
- **Pas d'interface commune** entre les deux reconcilers mirror malgre le code tres similaire
- **Pattern "type alias"** dans l'API : `ClusterImageSetMirrorSpec` est un alias de `ImageSetMirrorSpec`, ce qui permet le cast direct dans le code (`(*kuikv1alpha1.ImageSetMirrorSpec)(&cism.Spec)`)

### Prevention de boucles de mirroring
Le systeme collecte tous les prefixes de mirrors (depuis toutes les CISMs et ISMs) et filtre les images qui commencent par un de ces prefixes. Cela empeche qu'un mirror re-mirror ses propres images en boucle.

### Gestion de l'obsolescence (UnusedSince)
Deux niveaux de marquage :
- **Valeur speciale** `0001-01-01 01:00:00` : suppression instantanee (image hors-scope ou jamais monitoree et plus utilisee). Le +1h est un contournement pour eviter que la valeur zero soit ignoree par le patch JSON (zero value == nil en Go).
- **Timestamp normal** : l'image est en retention, sera supprimee apres `Cleanup.Retention` ou `UnusedImageExpiry`

### Fallback de mirroring
Si la copie d'image echoue, le code tente de verifier si l'image existe deja a destination. Si oui, le mirroring est considere comme reussi. Cela gere le cas ou l'image a ete mirroree par un processus precedent ou externe.

### Architecture amd64 uniquement
La copie d'image (`CopyImage`) est filtree pour l'architecture `amd64` uniquement (hardcode). C'est une limitation notable.

### FIXME dans le code
`mergeMirrors` ne supprime pas les mirrors obsoletes (presents dans le status mais pas dans la spec). Un commentaire `FIXME` le signale.

### Asymetrie de gestion de conflits
Le `ClusterImageSetMirrorReconciler` utilise `RetryOnConflict` pour les operations de finalizer, mais le `ImageSetMirrorReconciler` ne le fait pas. Cela pourrait poser probleme en cas de mises a jour concurrentes.

### Rate limiting du monitoring
Le `ClusterImageSetAvailabilityReconciler` implemente un rate limiting par registry base sur un systeme de "tick" : `tickDuration = Interval / MaxPerInterval`. A chaque reconciliation, un seul check est effectue par registry (celui dont le `LastMonitor` est le plus ancien). Cela distribue la charge uniformement dans le temps.

### Gestion du cache stale
Le controller CISA detecte le cas ou l'informer cache renvoie zero candidat alors que la CISA a des images dans son status. Dans ce cas, il requeue immediatement pour retenter avec un cache plus frais.
