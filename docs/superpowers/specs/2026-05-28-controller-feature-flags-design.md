# Controller Feature Flags — Design

**Date:** 2026-05-28  
**Status:** Approved

---

## Problem

The operator bundles multiple independent controllers (Decofile, Deco builds, DecoRedirect, Namespace/Valkey, HTTP API). Not all clusters need all controllers — the hub cluster only needs `decoredirect` and `operator-api`, but today all controllers start unconditionally.

This causes two problems:
1. Controllers that watch CRDs not installed in the cluster (e.g., Knative `Revision` in `DecofileReconciler`) crash the operator on startup.
2. There is no way to scope a deployment to only the features a cluster actually uses.

---

## Goal

Add a `controllers.enabled` list to the Helm values that controls which controllers are registered at startup. Default is `["*"]` (all controllers — preserves existing behavior). Unknown names are fatal errors.

---

## Design

### 1. Controller Name Constants

Each controller defines a `ControllerName` string constant in its own file, co-located with the reconciler. This makes the name explicit, discoverable, and compiler-checked.

```go
// internal/controller/decoredirect_controller.go
const ControllerName = "decoredirect"

// internal/controller/decofile_controller.go
const ControllerName = "decofile"

// internal/controller/deco_controller.go
const ControllerName = "deco"

// internal/controller/namespace_controller.go
const ControllerName = "namespace"
```

For the HTTP API (not a controller-runtime reconciler):
```go
// internal/api/server.go
const ControllerName = "operator-api"
```

### 2. CLI Flag

In `cmd/main.go`, add a `--controllers` flag:

```go
var controllersFlag string
flag.StringVar(&controllersFlag, "controllers", "*",
    `Comma-separated list of controllers to enable. Use "*" to enable all.
Valid values: namespace, decofile, deco, decoredirect, operator-api`)
```

### 3. Controller Set Logic

A small helper in `cmd/main.go` (or a new `cmd/controllers.go`) parses the flag and returns an `enabled(name string) bool` function:

```go
var knownControllers = []string{
    controller.NamespaceControllerName,
    controller.DecofileControllerName,
    controller.DecoControllerName,
    controller.DecoRedirectControllerName,
    api.ControllerName,
}

func parseControllers(flag string) (func(string) bool, error) {
    if flag == "*" {
        return func(string) bool { return true }, nil
    }
    requested := strings.Split(flag, ",")
    set := make(map[string]bool, len(requested))
    for _, name := range requested {
        name = strings.TrimSpace(name)
        if !slices.Contains(knownControllers, name) {
            return nil, fmt.Errorf("unknown controller %q, valid values: %s",
                name, strings.Join(knownControllers, ", "))
        }
        set[name] = true
    }
    return func(name string) bool { return set[name] }, nil
}
```

Unknown name → returns error → `main` logs and calls `os.Exit(1)`.

### 4. Gating Each Controller in `main.go`

Each `SetupWithManager` / `mgr.Add` call is wrapped with the `enabled` check:

```go
enabled, err := parseControllers(controllersFlag)
if err != nil {
    setupLog.Error(err, "invalid --controllers flag")
    os.Exit(1)
}

if enabled(controller.NamespaceControllerName) {
    if err := nsReconciler.SetupWithManager(mgr); err != nil { ... }
}

if enabled(controller.DecofileControllerName) {
    if err := (&controller.DecofileReconciler{...}).SetupWithManager(mgr); err != nil { ... }
}

// etc.
```

### 5. Helm Chart

**`values.yaml`** — new top-level block:

```yaml
# Controllers to enable at startup.
# Use ["*"] to enable all (default — preserves existing behavior).
# Valid values: namespace, decofile, deco, decoredirect, operator-api
controllers:
  enabled:
    - "*"
```

**`deployment-operator-controller-manager.yaml`** (via helm-generator) — inject arg when not `["*"]`:

```yaml
{{- if not (eq (index .Values.controllers.enabled 0) "*") }}
- --controllers={{ join "," .Values.controllers.enabled }}
{{- end }}
```

When `enabled: ["*"]`, no flag is injected and the default (`"*"`) applies.

---

## Usage Examples

**Hub cluster (`infra_applications/hub/values.yaml`):**
```yaml
controllers:
  enabled:
    - decoredirect
    - operator-api
```

**Spoke cluster (all controllers):**
```yaml
controllers:
  enabled:
    - "*"   # or omit entirely — same effect
```

**Unknown name (fatal at startup):**
```
ERROR  invalid --controllers flag  {"error": "unknown controller \"xpto\", valid values: namespace, decofile, deco, decoredirect, operator-api"}
```

---

## Files to Change

| File | Change |
|------|--------|
| `internal/controller/decoredirect_controller.go` | Add `ControllerName = "decoredirect"` |
| `internal/controller/decofile_controller.go` | Add `ControllerName = "decofile"` |
| `internal/controller/deco_controller.go` | Add `ControllerName = "deco"` |
| `internal/controller/namespace_controller.go` | Add `ControllerName = "namespace"` |
| `internal/api/server.go` | Add `ControllerName = "operator-api"` |
| `cmd/main.go` | Add `--controllers` flag + `parseControllers` + gate each controller |
| `chart/values.yaml` | Add `controllers.enabled: ["*"]` |
| `hack/helm-generator/main.go` | Inject `--controllers` arg in deployment when not `*` |

---

## Out of Scope

- Disabling individual sub-features within a controller (e.g., Knative watch inside Decofile)
- Dynamic reconfiguration at runtime (restart required to change active controllers)
- `--controllers=*,-decofile` exclusion syntax (list-only for now; can be added later)
