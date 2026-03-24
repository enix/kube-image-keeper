# Proposal : generer les destinations mirror pour toutes les variantes RIS d'une image

## Objectif

Quand un `ImageSetMirror` (ISM/CISM) matche une image qui existe aussi comme variante dans un `ReplicatedImageSet` (RIS/CRIS), le controller doit mirror l'image vers **toutes les destinations mirror possibles** (une par path de variante RIS), et non pas seulement vers la destination construite a partir du path de l'image source d'origine.

## Exemple concret

### Configuration

**ClusterReplicatedImageSet** `prometheus` :
```yaml
spec:
  upstreams:
    - registry: docker.io
      path: /prom/
      imageFilter:
        include: ["/prom/.+"]
    - registry: quay.io
      path: /prometheus/
      imageFilter:
        include: ["/prometheus/.+"]
    - registry: ghcr.io
      path: /prmths/
      imageFilter:
        include: ["/prmths/.+"]
```

**ImageSetMirror** `prom-mirror` :
```yaml
spec:
  imageFilter:
    include:
      - "docker.io/prom/prom2json:.+"
  mirrors:
    - registry: harbor.enix.io
      path: /mirroir
```

### Comportement attendu

Peu importe l'image source parmi les 6 suivantes :
- `docker.io/prom/prom2json:latest`
- `quay.io/prometheus/prom2json:latest`
- `ghcr.io/prmths/prom2json:latest`
- `harbor.enix.io/mirroir/prom/prom2json:latest`
- `harbor.enix.io/mirroir/prometheus/prom2json:latest`
- `harbor.enix.io/mirroir/prmths/prom2json:latest`

Le controller doit mirror vers les **3 destinations** :
- `harbor.enix.io/mirroir/prom/prom2json:latest`
- `harbor.enix.io/mirroir/prometheus/prom2json:latest`
- `harbor.enix.io/mirroir/prmths/prom2json:latest`

L'image source utilisee pour la copie peut etre n'importe laquelle des 6 images ci-dessus. L'ordre de priorisation pour le choix de la source suit le meme systeme de priorite que le webhook (crPriority, typeOrder, intraPriority, declarationOrder).

## Analyse de l'existant

### Webhook (`pod_webhook.go:378-475`)

Le webhook genere les alternatives en deux passes independantes :
1. **ISM pass** (lignes 391-422) : pour chaque ISM dont le filtre matche l'image, genere `path.Join(mirror.Registry, mirror.Path, imgPath)` ou `imgPath` est le path de l'image d'origine
2. **RIS pass** (lignes 424-455) : pour chaque RIS dont un upstream matche, genere les alternatives en substituant le prefix (registry+path) de l'upstream matche par celui de chaque autre upstream

Les deux passes ne se croisent pas : les alternatives RIS ne generent pas de variantes mirror, et les alternatives ISM ne prennent pas en compte les paths RIS.

### Controller ISM (`commonimagesetmirror.go:140-167`)

La fonction `mergePreviousAndCurrentMatchingImages` construit les `MirrorStatus` pour chaque image matchee :
```go
matchingImageWithoutRegistry := strings.SplitN(matchingImage, "/", 2)[1]
mirrors = append(mirrors, kuikv1alpha1.MirrorStatus{
    Image: path.Join(mirror.Registry, mirror.Path, matchingImageWithoutRegistry),
})
```

Chaque image source ne produit qu'une seule destination mirror par mirror spec, basee sur son propre path. Le controller n'a aucune connaissance des RIS.

### Mirroring effectif (`commonimagesetmirror.go:78-118`)

`mirrorImage(from, to)` copie depuis `from` (= `matchingImage.Image`, l'image source) vers `to.Image` (la destination mirror). La source est toujours l'image originale matchee par le filtre ISM.

## Modifications proposees

### 1. Le controller ISM doit connaitre les RIS

Le controller ISM/CISM doit lister les `ReplicatedImageSet` et `ClusterReplicatedImageSet` pour enrichir les destinations mirror.

**Fichier** : `commonimagesetmirror.go`

Ajouter une fonction qui, a partir d'une image matchee par un ISM, retrouve le RIS correspondant et calcule tous les paths alternatifs :

```
Entree : image matchee "docker.io/prom/prom2json:latest"
  1. Chercher un RIS/CRIS dont un upstream matche cette image
  2. Trouver l'upstream qui matche : {registry: docker.io, path: /prom/}
  3. Extraire le suffix : "/prom2json:latest"
  4. Pour chaque upstream du RIS, calculer le path alternatif :
     - docker.io/prom/ -> /prom/prom2json:latest
     - quay.io/prometheus/ -> /prometheus/prom2json:latest
     - ghcr.io/prmths/ -> /prmths/prom2json:latest
  5. Pour chaque mirror spec de l'ISM, generer les destinations :
     - harbor.enix.io/mirroir/prom/prom2json:latest
     - harbor.enix.io/mirroir/prometheus/prom2json:latest
     - harbor.enix.io/mirroir/prmths/prom2json:latest
```

### 2. Modifier `mergePreviousAndCurrentMatchingImages`

**Fichier** : `commonimagesetmirror.go`, fonction `mergePreviousAndCurrentMatchingImages` (ligne 140)

Actuellement, les destinations mirror sont calculees ainsi (ligne 151) :
```go
matchingImageWithoutRegistry := strings.SplitN(matchingImage, "/", 2)[1]
```

La modification consiste a :
1. Lister tous les RIS/CRIS
2. Pour chaque `matchingImage`, verifier si elle matche un upstream d'un RIS
3. Si oui, calculer les paths alternatifs (un par upstream du RIS)
4. Pour chaque mirror de l'ISM, generer une destination par path alternatif (au lieu d'une seule)

La structure `MatchingImage.Mirrors` (de type `[]MirrorStatus`) contiendra alors plusieurs entrees par mirror spec (une par variante de path RIS), au lieu d'une seule.

### 3. Modifier la source de mirroring

**Fichier** : `commonimagesetmirror.go`, fonction `mirrorImage` (ligne 78) et boucle d'appel dans `clusterimagesetmirror_controller.go:183-199` / `imagesetmirror_controller.go:169-185`

Actuellement, `mirrorImage` utilise `matchingImage.Image` comme source unique. La modification doit :

1. Construire la liste des sources candidates pour un groupe d'images equivalentes (l'image originale + toutes les variantes RIS + toutes les variantes mirror deja mirrorees)
2. Prioriser ces sources selon le meme ordre que le webhook :
   - `crPriority` (priorite du CR)
   - `typeOrder` (original > CISM > ISM > CRIS > RIS)
   - `intraPriority` (priorite intra-CR)
   - `declarationOrder` (ordre YAML)
3. Tenter le mirroring avec la premiere source disponible (premier `GetDescriptor` qui reussit)
4. Copier cette source vers chaque destination mirror qui n'a pas encore ete mirroree (`MirroredAt == nil`)

Cela permet par exemple, si `docker.io` et `quay.io` sont down mais que `harbor.enix.io/mirroir/prom/prom2json:latest` existe deja, de l'utiliser comme source pour copier vers `harbor.enix.io/mirroir/prometheus/prom2json:latest`.

### 4. Elargir le filtre ISM pour matcher les variantes RIS

**Fichier** : `commonimagesetmirror.go`, fonction `podsByNormalizedMatchingImages` (ligne 169)

Le filtre ISM actuel ne matche que les images qui correspondent directement au `ImageFilter` de l'ISM. Par exemple, si le filtre est `docker.io/prom/prom2json:.+`, l'image `quay.io/prometheus/prom2json:latest` ne sera pas matchee, meme si un RIS declare qu'elles sont equivalentes.

La modification consiste a enrichir le matching : pour chaque image d'un Pod, si elle matche un upstream d'un RIS qui a une autre variante matchant le filtre ISM, alors l'image doit etre consideree comme matchee. Idem pour les images deja presentes sur un mirror.

Cela garantit que si un Pod utilise `quay.io/prometheus/prom2json:latest` (variante RIS), le controller ISM la prend quand meme en charge et genere les destinations mirror appropriees.

### 5. Enrichir les alternatives du webhook

**Fichier** : `internal/webhook/core/v1/pod_webhook.go`, fonction `buildAlternativesList` (ligne 378)

Ajouter une troisieme passe apres les passes ISM et RIS : pour chaque alternative generee par un RIS, verifier si elle matche un ISM, et si oui, generer les destinations mirror correspondantes. Ces alternatives supplementaires sont ajoutees avec un `typeOrder` et une priorite coherente (meme `crPriority` que l'ISM dont elles derivent, mais avec un `typeOrder` ou `intraPriority` qui les place apres les alternatives directes).

Il y a deja un FIXME a la ligne 397 qui anticipe cette problematique :
```go
// FIXME: if it doesn't match the filter, also check if it matches one of the mirrored images
```

## Structure du status apres correction

Pour l'ISM `prom-mirror` avec l'image `docker.io/prom/prom2json:latest` matchee dans un Pod :

```yaml
status:
  matchingImages:
    - image: docker.io/prom/prom2json:latest
      mirrors:
        - image: harbor.enix.io/mirroir/prom/prom2json:latest
          mirroredAt: "2026-03-24T10:00:00Z"
        - image: harbor.enix.io/mirroir/prometheus/prom2json:latest
          mirroredAt: "2026-03-24T10:00:01Z"
        - image: harbor.enix.io/mirroir/prmths/prom2json:latest
          mirroredAt: "2026-03-24T10:00:02Z"
```

Les 3 destinations sont mirrorees, toutes depuis la meme source (la premiere disponible selon l'ordre de priorite).

## Fichiers impactes

| Fichier | Modification |
|---|---|
| `internal/controller/kuik/commonimagesetmirror.go` | Logique principale : lister les RIS, calculer les paths alternatifs, enrichir les destinations mirror, modifier la selection de source |
| `internal/controller/kuik/clusterimagesetmirror_controller.go` | Passer les RIS au reconciler, potentiellement ajuster la boucle de mirroring |
| `internal/controller/kuik/imagesetmirror_controller.go` | Idem |
| `internal/webhook/core/v1/pod_webhook.go` | Passe supplementaire pour generer les alternatives mirror des variantes RIS (resout le FIXME ligne 397) |
| Tests associes | Couvrir les cas : images avec paths differents selon le registry, mirroring multi-destination, fallback de source |

## Risques et points d'attention

- **Boucles de mirroring** : le mecanisme existant `getAllMirrorPrefixes` filtre deja les images dont le prefix correspond a un mirror. Les nouvelles destinations generees auront le meme prefix mirror, donc elles seront correctement filtrees. A verifier.
- **Volumetrie** : si un RIS a N upstreams et un ISM a M mirrors, chaque image matchee genere N * M destinations au lieu de M. Acceptable tant que N reste petit (en pratique 2-3 variantes).
- **Cleanup** : la suppression des images expirees doit supprimer toutes les variantes de destinations. Le code actuel itere sur `matchingImage.Mirrors`, donc ca devrait fonctionner sans changement si toutes les variantes sont dans la liste.
- **Architecture hardcodee** : le `CopyImage` actuel filtre pour `amd64` uniquement. C'est une limitation preexistante, orthogonale a cette proposition.
- **FIXME `mergeMirrors`** : la fonction ne supprime pas les mirrors obsoletes (presents dans le status mais pas dans la spec). Ce probleme preexistant devient plus visible avec N * M destinations. A traiter en meme temps ou separement.
