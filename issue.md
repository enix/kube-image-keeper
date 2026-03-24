# Issue : le webhook ne genere pas les alternatives mirror pour les images reecrites par un ReplicatedImageSet

## Resume

Quand un `ImageSetMirror` (ISM) et un `ClusterReplicatedImageSet` (CRIS) sont utilises ensemble sur des images ayant des paths differents selon le registry (ex: `docker.io/prom/` vs `quay.io/prometheus/`), le webhook ne genere pas l'alternative mirror correspondant au path de l'image reecrite par le CRIS. Le fallback vers le mirror echoue alors que l'image y est presente (mais sous un path different).

## Contexte

### Configuration en place

**ClusterReplicatedImageSet** `prometheus` : declare l'equivalence entre les variantes docker.io et quay.io de prom2json :

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterReplicatedImageSet
metadata:
  name: prometheus
spec:
  upstreams:
    - registry: docker.io
      imageFilter:
        include: ["/prom/.+"]
      path: "/prom/"
    - registry: quay.io
      imageFilter:
        include: ["/prometheus/.*"]
      path: "/prometheus/"
```

**ImageSetMirror** `test-cleanup-prom` : mirror les images prom2json (les deux variantes) vers Harbor :

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ImageSetMirror
metadata:
  name: test-cleanup-prom
  namespace: spv-ika
spec:
  imageFilter:
    include:
      - "docker.io/prom/prom2json:.+"
      - "quay.io/prometheus/prom2json:.+"
  cleanup:
    enabled: true
    retention: 5m
  mirrors:
    - registry: harbor.enix.io
      path: "/adunand-mirror"
      credentialSecret:
        name: enix-harbor-mirror
```

### Scenario de reproduction

1. Un Pod utilise l'image `docker.io/prom/prom2json:v1.5.0`
2. `docker.io` est bloque (simule une panne)
3. Le webhook genere les alternatives :
   ```
   [docker.io/prom/prom2json:v1.5.0, harbor.enix.io/adunand-mirror/prom/prom2json:v1.5.0, quay.io/prometheus/prom2json:v1.5.0]
   ```
4. `docker.io` est down : 404 sur le mirror car pas encore mirroree sous ce path. `quay.io` est up : le Pod est reecrit vers `quay.io/prometheus/prom2json:v1.5.0`
5. Le controller ISM mirror l'image depuis quay.io vers `harbor.enix.io/adunand-mirror/prometheus/prom2json:v1.5.0` (path `/prometheus/`, pas `/prom/`)
6. Maintenant, `quay.io` est aussi bloque
7. Le Pod est delete/recree, le webhook genere les memes alternatives :
   ```
   [docker.io/prom/prom2json:v1.5.0, harbor.enix.io/adunand-mirror/prom/prom2json:v1.5.0, quay.io/prometheus/prom2json:v1.5.0]
   ```
8. **Resultat** : aucune alternative n'est disponible. Le mirror contient l'image sous `harbor.enix.io/adunand-mirror/prometheus/prom2json:v1.5.0` mais cette reference n'est jamais testee.

## Cause racine

Le webhook genere les alternatives mirror uniquement a partir du path de l'image d'origine du Pod. Il ne prend pas en compte le fait que l'image a pu etre mirroree sous un path different (celui d'une variante declaree dans un `ReplicatedImageSet`).

Concretement :
- L'image d'origine est `docker.io/prom/prom2json:v1.5.0` (path = `/prom/`)
- Le mirror genere est `harbor.enix.io/adunand-mirror/prom/prom2json:v1.5.0`
- Mais l'image reellement mirroree est `harbor.enix.io/adunand-mirror/prometheus/prom2json:v1.5.0` (path = `/prometheus/`, car sourcee depuis quay.io)

Le webhook ne fait pas la "passe supplementaire" qui consisterait a verifier si les alternatives generees par le CRIS (ici `quay.io/prometheus/prom2json:v1.5.0`) matchent egalement un ISM, et si oui, a generer l'alternative mirror correspondante.

## Alternatives attendues (corrigees)

```
[
  docker.io/prom/prom2json:v1.5.0,
  harbor.enix.io/adunand-mirror/prom/prom2json:v1.5.0,
  quay.io/prometheus/prom2json:v1.5.0,
  harbor.enix.io/adunand-mirror/prometheus/prom2json:v1.5.0   <-- manquante actuellement
]
```

## Solution proposee (issue de la discussion)

Apres la generation initiale de la liste d'alternatives (CRIS + ISM), effectuer une passe supplementaire :

1. Pour chaque alternative deja generee (ex: `quay.io/prometheus/prom2json:v1.5.0` venue du CRIS)
2. Verifier si cette alternative matche un ISM
3. Si oui, generer la reference mirror correspondante (ex: `harbor.enix.io/adunand-mirror/prometheus/prom2json:v1.5.0`)
4. Ajouter ces references supplementaires a la fin de la liste d'alternatives

Cette logique se situe dans le webhook (calcul des alternatives au moment de l'admission du Pod), pas dans les controllers.

## Composants impactes

- **Webhook** (`internal/webhook/core/v1/pod_webhook.go`) : logique de generation des alternatives, c'est la que la passe supplementaire doit etre ajoutee
- **Pas d'impact sur les controllers** : le controller ISM mirror correctement l'image sous le bon path ; c'est le webhook qui ne genere pas la bonne reference au moment du fallback

## Remarques de la discussion

- Ce cas est relativement minoritaire : il necessite que des images existent sous des paths differents selon le registry, ce qui arrive surtout avec les images Prometheus (`/prom/` sur docker.io vs `/prometheus/` sur quay.io)
- La duplication sur le mirror (meme image sous deux paths) n'est pas un vrai probleme car les layers sont reutilises au niveau du registry
- L'equipe envisage que ce cas soit potentiellement reporte apres la GA 2.0 si la solution s'avere trop complexe a cause des corner cases
- Une autre piste discutee (tester les alternatives en parallele, rate limiting par registry pour eviter les bans) est une evolution future orthogonale a ce probleme
