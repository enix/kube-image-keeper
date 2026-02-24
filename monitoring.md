Ajout au fichier de configuration :

```yaml
# /etc/kuik/config.yaml
registriesMonitoring:
  default:
    method: HEAD
    interval: 5m
    maxPerInterval: 1
    timeout: 5s
  items:
    docker.io:
      method: HEAD
      interval: 1h
      maxPerInterval: 3
      timeout: 5s
      fallbackCredentialSecret: # optionnel : si le pod n'existe plus
        name: registry-secret
        namespace: kuik-system
```

CRD ClusterImageSetAvailability :

```yaml
apiVersion: kuik.enix.io/v1alpha1
kind: ClusterImageSetAvailability # cluster wide only pour l'instant
metadata:
  name: monitor-critical-images
spec:
  unusedImageExpiry: 30d
  imageFilter:
    include:
    - library/nginx:.+
    - library/redis:.+
    exclude:
    - .*-rc\d+$
status:
  images:
    # unused
    - path: nginx:1.25
      unusedSince: 2025-02-25T09:45:58Z
      status: Available
      lastMonitor: 2025-02-25T09:45:58Z
    # with error
    - path: nginx:1.26
      status: Unreachable
      lastError: "HTTP 500: the registry is mucho broken"
      lastMonitor: 2025-02-25T09:45:58Z
    # in use and available
    - path: nginx:1.27
      status: Available
      lastMonitor: 2025-02-25T09:45:58Z
    # with orgname
    - path: enix/x509-certificate-exporter:v1.0.0
      status: Available
      lastMonitor: 2025-02-25T09:45:58Z
```

Status possibles :

- `Scheduled` : tâche de surveillance pas encore exécutée
- `Available` : image dispo
- `NotFound` : 404 => image n’existe pas
- `Unreachable` : registry non joignable
- `InvalidAuth` : identifiants incorrects 
- `UnavailableSecret` : secret n’existe pas
- `QuotaExceeded` : quota de la registry atteint
