# Proposal : tests de validation du correctif ISM + RIS cross-path mirroring

## Contexte

Le correctif vise a ce que, lorsqu'un ISM et un RIS couvrent la meme image avec des paths differents selon le registry, le systeme :
1. **Webhook** : genere les alternatives mirror pour toutes les variantes de path (pas seulement celle de l'image d'origine du Pod)
2. **Controller ISM** : mirror l'image vers toutes les destinations correspondant aux differentes variantes de path, en utilisant n'importe quelle source disponible selon l'ordre de priorite

## Configuration de reference pour les tests

Tous les tests ci-dessous utilisent la configuration suivante comme base :

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
      credentialSecret:
        name: harbor-secret
```

---

## Partie 1 : tests du webhook (`internal/webhook/core/v1/pod_webhook_test.go`)

Ces tests portent sur `buildAlternativesList`. Ils ne necessitent pas de mock des registries (pas de `findBestAlternative`), seulement la construction de la liste d'alternatives. Le pattern est le meme que les tests existants de `compareAlternatives` : tests unitaires Ginkgo sur des structs internes du package.

La fonction `buildAlternativesList` a besoin d'un `client.Client` (pour `loadAlternativesSecrets`). Pour les tests qui se concentrent sur la generation de references, il faudra soit :
- utiliser l'envtest existant et creer les Secrets necessaires
- soit introduire un refactoring minimal pour separer la generation des alternatives du chargement des secrets

### Test 1.1 : les alternatives mirror sont generees pour chaque variante RIS

**Scenario** : un Pod utilise `docker.io/prom/prom2json:latest`, il existe un CRIS avec 3 upstreams et un ISM avec un mirror.

**Entree** :
```go
imageSetMirrors := []kuikv1alpha1.ImageSetMirror{{
    Spec: kuikv1alpha1.ImageSetMirrorSpec{
        ImageFilter: kuikv1alpha1.ImageFilterDefinition{
            Include: []string{"docker.io/prom/prom2json:.+"},
        },
        Mirrors: kuikv1alpha1.Mirrors{{
            Registry: "harbor.enix.io",
            Path:     "/mirroir",
        }},
    },
}}

replicatedImageSets := []kuikv1alpha1.ReplicatedImageSet{{
    Spec: kuikv1alpha1.ReplicatedImageSetSpec{
        Upstreams: []kuikv1alpha1.ReplicatedUpstream{
            {ImageReference: *kuikv1alpha1.NewImageReference("docker.io", "/prom/"),
             ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{"/prom/.+"}}},
            {ImageReference: *kuikv1alpha1.NewImageReference("quay.io", "/prometheus/"),
             ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{"/prometheus/.+"}}},
            {ImageReference: *kuikv1alpha1.NewImageReference("ghcr.io", "/prmths/"),
             ImageFilter: kuikv1alpha1.ImageFilterDefinition{Include: []string{"/prmths/.+"}}},
        },
    },
}}

container := &Container{
    Container:    &corev1.Container{Image: "docker.io/prom/prom2json:latest"},
    Alternatives: map[string]struct{}{},
}
```

**Assertion** : apres `buildAlternativesList`, `container.Images` doit contenir exactement ces references (l'ordre respecte le systeme de priorite : original, ISM mirrors, CRIS upstreams) :
```go
Expect(refs(container)).To(Equal([]string{
    "docker.io/prom/prom2json:latest",               // original
    "harbor.enix.io/mirroir/prom/prom2json:latest",        // mirror depuis l'image originale (ISM)
    "harbor.enix.io/mirroir/prometheus/prom2json:latest",   // mirror depuis la variante quay (ISM + RIS)
    "harbor.enix.io/mirroir/prmths/prom2json:latest",      // mirror depuis la variante ghcr (ISM + RIS)
    "docker.io/prom/prom2json:latest",                      // RIS upstream docker (dedoublonne avec l'original)
    "quay.io/prometheus/prom2json:latest",                   // RIS upstream quay
    "ghcr.io/prmths/prom2json:latest",                      // RIS upstream ghcr
}))
```

Note : `docker.io/prom/prom2json:latest` apparait en double (original + RIS upstream docker.io). La fonction `addAlternative` dedoublonne via `c.Alternatives`, donc dans la liste finale il ne doit apparaitre qu'une fois. La liste attendue est donc :
```go
Expect(refs(container)).To(Equal([]string{
    "docker.io/prom/prom2json:latest",
    "harbor.enix.io/mirroir/prom/prom2json:latest",
    "harbor.enix.io/mirroir/prometheus/prom2json:latest",
    "harbor.enix.io/mirroir/prmths/prom2json:latest",
    "quay.io/prometheus/prom2json:latest",
    "ghcr.io/prmths/prom2json:latest",
}))
```

### Test 1.2 : meme resultat quand le Pod utilise une variante RIS comme image d'origine

**Scenario** : un Pod utilise `quay.io/prometheus/prom2json:latest` (pas l'image ciblee directement par l'ISM, mais une variante RIS de celle-ci).

**Entree** : memes ISM et RIS que 1.1, mais :
```go
container := &Container{
    Container:    &corev1.Container{Image: "quay.io/prometheus/prom2json:latest"},
    Alternatives: map[string]struct{}{},
}
```

**Assertion** : les memes alternatives mirror doivent etre generees. L'ISM ne matche pas directement `quay.io/prometheus/...`, mais via le RIS le lien doit etre fait. L'ordre change (l'original est maintenant quay.io) :
```go
Expect(refs(container)).To(Equal([]string{
    "quay.io/prometheus/prom2json:latest",
    "harbor.enix.io/mirroir/prom/prom2json:latest",
    "harbor.enix.io/mirroir/prometheus/prom2json:latest",
    "harbor.enix.io/mirroir/prmths/prom2json:latest",
    "docker.io/prom/prom2json:latest",
    "ghcr.io/prmths/prom2json:latest",
}))
```

### Test 1.3 : meme resultat quand le Pod utilise une image deja mirroree

**Scenario** : un Pod utilise `harbor.enix.io/mirroir/prometheus/prom2json:latest` (une image deja mirroree, sous le path quay.io).

**Entree** : memes ISM et RIS que 1.1, mais :
```go
container := &Container{
    Container:    &corev1.Container{Image: "harbor.enix.io/mirroir/prometheus/prom2json:latest"},
    Alternatives: map[string]struct{}{},
}
```

**Assertion** : les memes alternatives doivent etre generees. Le webhook doit reconnaitre cette image comme faisant partie de l'ensemble ISM+RIS :
```go
Expect(refs(container)).To(Equal([]string{
    "harbor.enix.io/mirroir/prometheus/prom2json:latest",
    "harbor.enix.io/mirroir/prom/prom2json:latest",
    "harbor.enix.io/mirroir/prmths/prom2json:latest",
    "docker.io/prom/prom2json:latest",
    "quay.io/prometheus/prom2json:latest",
    "ghcr.io/prmths/prom2json:latest",
}))
```

### Test 1.4 : pas d'alternatives mirror supplementaires sans RIS

**Scenario** : un ISM seul, sans RIS correspondant. Verifie que le comportement existant n'est pas casse.

**Entree** : meme ISM que 1.1, mais `replicatedImageSets` vide.

```go
container := &Container{
    Container:    &corev1.Container{Image: "docker.io/prom/prom2json:latest"},
    Alternatives: map[string]struct{}{},
}
```

**Assertion** : seule l'alternative mirror "classique" est generee :
```go
Expect(refs(container)).To(Equal([]string{
    "docker.io/prom/prom2json:latest",
    "harbor.enix.io/mirroir/prom/prom2json:latest",
}))
```

### Test 1.5 : pas de doublons dans les alternatives

**Scenario** : un Pod utilise `docker.io/prom/prom2json:latest`. L'ISM matche cette image et le RIS a un upstream docker.io/prom/ qui pointe vers le meme path.

**Assertion** : `harbor.enix.io/mirroir/prom/prom2json:latest` ne doit apparaitre qu'une seule fois (meme s'il est genere a la fois par la passe ISM directe et par la passe ISM+RIS pour l'upstream docker.io).

### Test 1.6 : ISM avec plusieurs mirrors et RIS avec plusieurs upstreams (produit cartesien)

**Scenario** : un ISM avec 2 mirrors et un CRIS avec 3 upstreams.

**Entree** :
```go
imageSetMirrors := []kuikv1alpha1.ImageSetMirror{{
    Spec: kuikv1alpha1.ImageSetMirrorSpec{
        ImageFilter: kuikv1alpha1.ImageFilterDefinition{
            Include: []string{"docker.io/prom/prom2json:.+"},
        },
        Mirrors: kuikv1alpha1.Mirrors{
            {Registry: "harbor.enix.io", Path: "/mirroir-a"},
            {Registry: "registry.local", Path: "/mirroir-b"},
        },
    },
}}
```

**Assertion** : 2 mirrors x 3 paths RIS = 6 alternatives mirror (plus les 3 upstreams RIS + l'original, moins les doublons) :
```go
Expect(refs(container)).To(Equal([]string{
    "docker.io/prom/prom2json:latest",
    "harbor.enix.io/mirroir-a/prom/prom2json:latest",
    "harbor.enix.io/mirroir-a/prometheus/prom2json:latest",
    "harbor.enix.io/mirroir-a/prmths/prom2json:latest",
    "registry.local/mirroir-b/prom/prom2json:latest",
    "registry.local/mirroir-b/prometheus/prom2json:latest",
    "registry.local/mirroir-b/prmths/prom2json:latest",
    "quay.io/prometheus/prom2json:latest",
    "ghcr.io/prmths/prom2json:latest",
}))
```

### Test 1.7 : priorites respectees pour les alternatives mirror croisees

**Scenario** : verifier que les alternatives mirror croisees (issues de la passe ISM+RIS) sont placees au bon endroit dans l'ordre de priorite par rapport aux mirrors directs et aux upstreams RIS.

**Assertion** : les mirrors (ISM, typeOrder=2) doivent tous arriver avant les upstreams (CRIS, typeOrder=3), y compris les mirrors generes via les variantes RIS. Les mirrors generes par le croisement ISM+RIS conservent le `typeOrder` et la `crPriority` de l'ISM d'origine.

---

## Partie 2 : tests du controller ISM (`internal/controller/kuik/commonimagesetmirror_test.go`)

Ces tests portent sur `mergePreviousAndCurrentMatchingImages` et les fonctions associees. Ils sont purement unitaires (pas de call registry) et testent la construction de la liste `MatchingImage.Mirrors` dans le status.

### Test 2.1 : les destinations mirror incluent toutes les variantes de path RIS

**Scenario** : un Pod utilise `docker.io/prom/prom2json:latest`, l'ISM le matche, le CRIS declare 3 upstreams.

**Entree** :
```go
pods := []corev1.Pod{{
    ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
    Spec: corev1.PodSpec{
        Containers: []corev1.Container{{Image: "docker.io/prom/prom2json:latest"}},
    },
}}

ismSpec := &kuikv1alpha1.ImageSetMirrorSpec{
    ImageFilter: kuikv1alpha1.ImageFilterDefinition{
        Include: []string{"docker.io/prom/prom2json:.+"},
    },
    Mirrors: kuikv1alpha1.Mirrors{{
        Registry: "harbor.enix.io",
        Path:     "/mirroir",
    }},
}
ismStatus := &kuikv1alpha1.ImageSetMirrorStatus{}
```

**Assertion** : le `MatchingImage` pour `docker.io/prom/prom2json:latest` doit avoir 3 `MirrorStatus` :
```go
Expect(matchingImages["docker.io/prom/prom2json:latest"].Mirrors).To(ConsistOf(
    HaveField("Image", "harbor.enix.io/mirroir/prom/prom2json:latest"),
    HaveField("Image", "harbor.enix.io/mirroir/prometheus/prom2json:latest"),
    HaveField("Image", "harbor.enix.io/mirroir/prmths/prom2json:latest"),
))
```

### Test 2.2 : un Pod avec une variante RIS produit les memes destinations mirror

**Scenario** : un Pod utilise `quay.io/prometheus/prom2json:latest` (variante RIS). L'ISM filtre `docker.io/prom/prom2json:.+`.

**Assertion** : le controller reconnait via le RIS que cette image est couverte par l'ISM, et genere les 3 memes destinations mirror.

### Test 2.3 : pas de destinations supplementaires sans RIS (non-regression)

**Scenario** : meme ISM, pas de RIS, Pod avec `docker.io/prom/prom2json:latest`.

**Assertion** : un seul `MirrorStatus` :
```go
Expect(matchingImages["docker.io/prom/prom2json:latest"].Mirrors).To(HaveLen(1))
Expect(matchingImages["docker.io/prom/prom2json:latest"].Mirrors[0].Image).To(
    Equal("harbor.enix.io/mirroir/prom/prom2json:latest"),
)
```

### Test 2.4 : pas de doublon dans les destinations mirror

**Scenario** : l'ISM a un mirror vers `harbor.enix.io/mirroir`, et le RIS a un upstream docker.io qui produit le meme path que l'image d'origine.

**Assertion** : `harbor.enix.io/mirroir/prom/prom2json:latest` n'apparait qu'une seule fois dans `Mirrors`.

### Test 2.5 : `UnusedSince` fonctionne correctement avec les variantes RIS

**Scenario** : un Pod utilisait `quay.io/prometheus/prom2json:latest` (matchant via RIS). Le Pod est supprime.

**Assertion** : l'image est marquee `UnusedSince != nil`. Si un nouveau Pod arrive avec `docker.io/prom/prom2json:latest` (meme image via RIS), l'`UnusedSince` est reset a nil car l'image est toujours couverte.

### Test 2.6 : cleanup supprime toutes les variantes de destinations mirror

**Scenario** : une image a ete mirroree vers 3 destinations (les 3 variantes). L'image est marquee unused et la retention est depassee.

**Assertion** : les 3 destinations sont supprimees (les 3 `MirrorStatus` disparaissent de la liste).

---

## Partie 3 : tests du mirroring effectif (`internal/controller/kuik/commonimagesetmirror_test.go`)

Ces tests portent sur `mirrorImage` et la boucle de mirroring dans les controllers. Ils necessitent un mock/stub de `registry.NewClient` pour simuler la disponibilite des images sans faire de vrais appels reseau.

### Test 3.1 : le mirroring utilise la premiere source disponible selon la priorite

**Scenario** : 3 sources possibles, la premiere (docker.io) echoue, la deuxieme (harbor mirror) echoue, la troisieme (quay.io) reussit.

**Assertion** : l'image est mirroree depuis `quay.io/prometheus/prom2json:latest` vers les 3 destinations. Le `MirroredAt` est renseigne sur les 3 `MirrorStatus`.

### Test 3.2 : une source deja mirroree peut servir de source pour les autres destinations

**Scenario** : `harbor.enix.io/mirroir/prom/prom2json:latest` est deja mirroree (`MirroredAt` renseigne). Les registries sources (`docker.io`, `quay.io`, `ghcr.io`) sont toutes injoignables.

**Assertion** : `harbor.enix.io/mirroir/prom/prom2json:latest` est utilisee comme source pour copier vers `harbor.enix.io/mirroir/prometheus/prom2json:latest` et `harbor.enix.io/mirroir/prmths/prom2json:latest`.

### Test 3.3 : le mirroring ne re-copie pas les destinations deja mirrorees

**Scenario** : sur les 3 destinations, 2 ont deja `MirroredAt` renseigne, 1 est a nil.

**Assertion** : seule la destination manquante est mirroree. Les 2 autres ne sont pas re-copiees.

### Test 3.4 : erreur partielle (certaines destinations echouent, d'autres reussissent)

**Scenario** : la source est disponible, 2 destinations sont copiees avec succes, 1 echoue.

**Assertion** : les 2 reussies ont `MirroredAt` renseigne et `LastError` vide. La 3eme a `MirroredAt` nil et `LastError` renseigne. Le controller retourne une erreur pour requeue.

---

## Partie 4 : tests d'integration webhook + controller (optionnels, plus lourds)

Ces tests simulent le scenario complet de bout en bout avec envtest. Ils sont plus proches des e2e mais sans cluster reel.

### Test 4.1 : scenario complet - panne en cascade

1. Creer le CRIS, l'ISM, et un Pod avec `docker.io/prom/prom2json:latest`
2. Simuler docker.io down : le webhook reroute vers `quay.io/prometheus/prom2json:latest`
3. Le controller mirror vers les 3 destinations
4. Simuler docker.io ET quay.io down : le webhook reroute vers `harbor.enix.io/mirroir/prometheus/prom2json:latest` (ou `/prom/` selon la priorite)
5. Verifier que le Pod demarre avec une image disponible

### Test 4.2 : scenario complet - ajout d'un upstream RIS apres le premier mirroring

1. Creer le CRIS avec 2 upstreams, l'ISM, et un Pod
2. Le controller mirror vers 2 destinations
3. Ajouter un 3eme upstream au CRIS
4. Verifier que le controller genere la 3eme destination mirror au prochain reconcile

---

## Strategie d'implementation des tests

### Priorite d'implementation

1. **Tests 1.1 a 1.7** (webhook `buildAlternativesList`) : les plus critiques car ils testent le coeur du probleme (la generation des alternatives). Pattern deja en place dans `pod_webhook_test.go`.
2. **Tests 2.1 a 2.6** (controller `mergePreviousAndCurrentMatchingImages`) : testent le calcul des destinations mirror. Necessite d'etendre les tests scaffold existants.
3. **Tests 3.1 a 3.4** (mirroring effectif) : necessitent un mecanisme de mock pour `registry.NewClient`, qui n'existe pas encore.
4. **Tests 4.1 et 4.2** (integration) : complexes, a considerer pour une phase ulterieure.

### Infrastructure requise

- **Pour la partie 1** : aucune infra supplementaire, le pattern de test de `compareAlternatives` fonctionne (tests unitaires sur structs internes dans le meme package). Un client envtest est necessaire si on teste `buildAlternativesList` directement (a cause de `loadAlternativesSecrets`), sinon on peut tester une sous-fonction qui ne genere que les `prioritizedAlternative` sans charger les secrets.
- **Pour la partie 2** : les tests sont unitaires sur `mergePreviousAndCurrentMatchingImages`. Il faudra passer les RIS en parametre (modification de la signature de la fonction).
- **Pour la partie 3** : necessite une interface pour `registry.NewClient` (ou l'injection d'un client registry mockable). C'est le changement le plus structurant.
