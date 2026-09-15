# Operator configuration

The kuik manager reads a YAML configuration file at startup to tune routing, monitoring, and metrics behaviors. This page lists every supported field, its type, default value, and effect.

CRDs are documented separately in [`crds.md`](./crds.md). This page only covers the operator-wide configuration file.

## File location and loading

- Default path: `/etc/kube-image-keeper/config.yaml`
- Override with the `--config` flag on the manager binary (see `cmd/main.go`).
- Loaded with [koanf](https://github.com/knadh/koanf): YAML parsed into `internal/config/config.go`'s `Config` struct.
- The file is optional. When it is missing, the operator boots on its built-in defaults (`config.LoadDefault()` / `defaultConfig`).

### Precedence

1. Built-in defaults defined in `internal/config/config.go`.
2. Values from the YAML file (when present) are merged on top of (1).

The Helm chart does not ship a default `configuration:` block. Any field set under `configuration:` in `values.yaml` (or via `--set`) is rendered into a ConfigMap mounted at `/etc/kube-image-keeper/config.yaml`. Leaving `configuration` empty makes the chart skip the ConfigMap entirely so the operator runs purely on its defaults.

### Example (full)

The following file shows every supported key with its default value. You only need to set the keys you want to override; everything else falls back to the defaults below.

```yaml title="/etc/kube-image-keeper/config.yaml"
example: TODO
```
