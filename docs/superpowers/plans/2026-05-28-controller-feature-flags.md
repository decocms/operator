# Controller Feature Flags — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `--controllers` flag to the operator and a `controllers.enabled` Helm value so each cluster deployment can opt-in to only the controllers it needs.

**Architecture:** Each controller defines a `ControllerName` string constant. A `parseControllers` helper in `cmd/controllers.go` parses the comma-separated flag (supporting `"*"` for all), validates names against a registry of known controllers, and returns an `enabled(name string) bool` function. `main.go` wraps each controller's setup with this check. The Helm chart injects `--controllers=<list>` into the deployment args.

**Tech Stack:** Go 1.24, controller-runtime, Helm 3, Sprig template functions (`join`).

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `internal/controller/decoredirect_controller.go` | Modify | Add `ControllerName = "decoredirect"` |
| `internal/controller/decofile_controller.go` | Modify | Add `ControllerName = "decofile"` |
| `internal/controller/deco_controller.go` | Modify | Add `ControllerName = "deco"` |
| `internal/controller/namespace_controller.go` | Modify | Add `ControllerName = "namespace"` |
| `internal/api/server.go` | Modify | Add `ControllerName = "operator-api"` |
| `cmd/controllers.go` | Create | `parseControllers` function + `knownControllers` registry |
| `cmd/controllers_test.go` | Create | Unit tests for `parseControllers` |
| `cmd/main.go` | Modify | Add `--controllers` flag, call `parseControllers`, gate each controller |
| `chart/values.yaml` | Modify | Add `controllers.enabled: ["*"]` |
| `hack/helm-generator/main.go` | Modify | Inject `--controllers` arg into deployment |

---

## Task 1: Add ControllerName constants

**Files:**
- Modify: `internal/controller/decoredirect_controller.go`
- Modify: `internal/controller/decofile_controller.go`
- Modify: `internal/controller/deco_controller.go`
- Modify: `internal/controller/namespace_controller.go`
- Modify: `internal/api/server.go`

All four controllers live in `package controller` — each constant needs a unique name to avoid conflicts.

- [ ] **Step 1: Add constant to `decoredirect_controller.go`**

After the existing `const dummyBackendName = "redirect-dummy-backend"` line, add:

```go
const DecoRedirectControllerName = "decoredirect"
```

- [ ] **Step 2: Add constant to `decofile_controller.go`**

Add to the existing `const` block at the top of the file:

```go
const DecofileControllerName = "decofile"
```

- [ ] **Step 3: Add constant to `deco_controller.go`**

Add to the existing `const` block:

```go
const DecoControllerName = "deco"
```

- [ ] **Step 4: Add constant to `namespace_controller.go`**

Add to the existing `const` block:

```go
const NamespaceControllerName = "namespace"
```

- [ ] **Step 5: Add constant to `internal/api/server.go`**

After the package declaration, add:

```go
const ControllerName = "operator-api"
```

- [ ] **Step 6: Verify the code compiles**

```bash
cd /Users/igoramf/projects/deco/operator
go build ./...
```

Expected: exits 0, no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/decoredirect_controller.go \
        internal/controller/decofile_controller.go \
        internal/controller/deco_controller.go \
        internal/controller/namespace_controller.go \
        internal/api/server.go
git commit -m "feat(controllers): add ControllerName constants to each controller"
```

---

## Task 2: `parseControllers` function with tests

**Files:**
- Create: `cmd/controllers.go`
- Create: `cmd/controllers_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/controllers_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/deco-sites/decofile-operator/internal/api"
	"github.com/deco-sites/decofile-operator/internal/controller"
)

func TestParseControllers_Star(t *testing.T) {
	enabled, err := parseControllers("*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{
		controller.NamespaceControllerName,
		controller.DecofileControllerName,
		controller.DecoControllerName,
		controller.DecoRedirectControllerName,
		api.ControllerName,
	} {
		if !enabled(name) {
			t.Errorf("expected %q to be enabled with *, but it wasn't", name)
		}
	}
}

func TestParseControllers_Subset(t *testing.T) {
	enabled, err := parseControllers("decoredirect,operator-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled(controller.DecoRedirectControllerName) {
		t.Error("expected decoredirect to be enabled")
	}
	if !enabled(api.ControllerName) {
		t.Error("expected operator-api to be enabled")
	}
	if enabled(controller.DecofileControllerName) {
		t.Error("expected decofile to be disabled")
	}
	if enabled(controller.NamespaceControllerName) {
		t.Error("expected namespace to be disabled")
	}
	if enabled(controller.DecoControllerName) {
		t.Error("expected deco to be disabled")
	}
}

func TestParseControllers_UnknownName(t *testing.T) {
	_, err := parseControllers("decoredirect,xpto")
	if err == nil {
		t.Fatal("expected error for unknown controller name, got nil")
	}
	if !strings.Contains(err.Error(), "xpto") {
		t.Errorf("expected error to mention 'xpto', got: %v", err)
	}
}

func TestParseControllers_WhitespaceHandled(t *testing.T) {
	enabled, err := parseControllers("decoredirect, operator-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled(controller.DecoRedirectControllerName) {
		t.Error("expected decoredirect to be enabled")
	}
	if !enabled(api.ControllerName) {
		t.Error("expected operator-api to be enabled")
	}
}
```

- [ ] **Step 2: Run tests — expect compile error** (function not defined yet)

```bash
cd /Users/igoramf/projects/deco/operator
go test ./cmd/... 2>&1 | head -10
```

Expected: compile error `undefined: parseControllers`

- [ ] **Step 3: Create `cmd/controllers.go`**

```go
package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/deco-sites/decofile-operator/internal/api"
	"github.com/deco-sites/decofile-operator/internal/controller"
)

var knownControllers = []string{
	controller.NamespaceControllerName,
	controller.DecofileControllerName,
	controller.DecoControllerName,
	controller.DecoRedirectControllerName,
	api.ControllerName,
}

// parseControllers parses a comma-separated list of controller names.
// "*" enables all known controllers.
// Returns an error if any name is not in knownControllers.
func parseControllers(flag string) (func(string) bool, error) {
	if strings.TrimSpace(flag) == "*" {
		return func(string) bool { return true }, nil
	}
	parts := strings.Split(flag, ",")
	set := make(map[string]bool, len(parts))
	for _, name := range parts {
		name = strings.TrimSpace(name)
		if !slices.Contains(knownControllers, name) {
			return nil, fmt.Errorf("unknown controller %q; valid values: %s",
				name, strings.Join(knownControllers, ", "))
		}
		set[name] = true
	}
	return func(name string) bool { return set[name] }, nil
}
```

- [ ] **Step 4: Run tests — expect all to pass**

```bash
cd /Users/igoramf/projects/deco/operator
go test ./cmd/... -v 2>&1 | tail -15
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/controllers.go cmd/controllers_test.go
git commit -m "feat(controllers): add parseControllers with knownControllers registry"
```

---

## Task 3: Wire `--controllers` flag in `main.go`

**Files:**
- Modify: `cmd/main.go`

Note: `main.go` is restructured to scope each controller's variables inside its own `if enabled(...)` block.

- [ ] **Step 1: Add the `--controllers` flag after `flag.StringVar(&redirectClusterIssuer, ...)` (around line 137)**

Add this variable declaration at the top of `main()` with the other vars:

```go
var controllersFlag string
```

Add this flag definition after the `redirectClusterIssuer` flag (before `opts.BindFlags`):

```go
flag.StringVar(&controllersFlag, "controllers", "*",
    `Comma-separated list of controllers to enable. Use "*" to enable all.
Valid values: `+strings.Join(knownControllers, ", "))
```

- [ ] **Step 2: Parse the flag and gate controllers**

After `flag.Parse()` and `ctrl.SetLogger(...)`, add:

```go
enabled, err := parseControllers(controllersFlag)
if err != nil {
    setupLog.Error(err, "invalid --controllers flag")
    os.Exit(1)
}
```

- [ ] **Step 3: Gate the namespace controller block**

Replace lines 281–329 (from `nsReconciler := ...` to the end of `InitMetrics` runnable) with:

```go
if enabled(controller.NamespaceControllerName) {
    var valkeyClient valkey.Client
    switch {
    case valkeyURL != "":
        valkeyClient = valkey.NewDirectClient(valkeyURL, valkeyAdminPassword)
        defer func() { _ = valkeyClient.Close() }()
        setupLog.Info("Valkey ACL provisioning enabled (direct)", "addr", valkeyURL)
    case valkeySentinelURLs != "":
        valkeyClient = valkey.NewSentinelClient(valkey.Config{
            SentinelAddrs: strings.Split(valkeySentinelURLs, ","),
            MasterName:    valkeySentinelMaster,
            AdminPassword: valkeyAdminPassword,
        })
        defer func() { _ = valkeyClient.Close() }()
        setupLog.Info("Valkey ACL provisioning enabled (sentinel)",
            "sentinel", valkeySentinelURLs, "master", valkeySentinelMaster)
    default:
        valkeyClient = valkey.NoopClient{}
        setupLog.Info("Valkey ACL provisioning disabled (set VALKEY_URL or VALKEY_SENTINEL_URLS)")
    }

    nsReconciler := &controller.NamespaceReconciler{
        Client:       mgr.GetClient(),
        Scheme:       mgr.GetScheme(),
        ValkeyClient: valkeyClient,
        ResyncPeriod: valkeyResyncPeriod,
    }
    if err := nsReconciler.SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "unable to create controller", "controller", "Namespace")
        os.Exit(1)
    }
    if valkeyWatchFailover && valkeySentinelURLs != "" {
        if err := mgr.Add(&leaderElectedRunnable{fn: func(ctx context.Context) error {
            return valkeyClient.WatchFailover(ctx, func() {
                controller.RecordSentinelFailover()
                nsReconciler.TriggerResyncAll(ctx)
            })
        }}); err != nil {
            setupLog.Error(err, "unable to add Sentinel failover watcher (non-fatal)")
        } else {
            setupLog.Info("Sentinel failover watcher enabled")
        }
        if err := mgr.Add(&leaderElectedRunnable{fn: func(ctx context.Context) error {
            return valkeyClient.WatchNodeRestart(ctx, func(addr string) {
                nsReconciler.ProvisionSingleNode(ctx, addr)
            })
        }}); err != nil {
            setupLog.Error(err, "unable to add node-restart watcher (non-fatal)")
        } else {
            setupLog.Info("Sentinel node-restart watcher enabled")
        }
    }
    if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
        if !mgr.GetCache().WaitForCacheSync(ctx) {
            return fmt.Errorf("cache never synced")
        }
        return nsReconciler.InitMetrics(ctx)
    })); err != nil {
        setupLog.Error(err, "unable to add metrics init runnable")
        os.Exit(1)
    }
}
```

- [ ] **Step 4: Gate the decofile controller block**

Replace lines 331–352 (from `httpClient := ...` to end of webhooks block) with:

```go
if enabled(controller.DecofileControllerName) {
    httpClient := controller.NewHTTPClient()
    if err := (&controller.DecofileReconciler{
        Client:     mgr.GetClient(),
        Scheme:     mgr.GetScheme(),
        HTTPClient: httpClient,
    }).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "unable to create controller", "controller", "Decofile")
        os.Exit(1)
    }
    // nolint:goconst
    if os.Getenv("ENABLE_WEBHOOKS") != "false" {
        if err := webhookv1.SetupServiceWebhookWithManager(mgr); err != nil {
            setupLog.Error(err, "unable to create webhook", "webhook", "Service")
            os.Exit(1)
        }
        if err := webhookv1.SetupDecofileWebhookWithManager(mgr); err != nil {
            setupLog.Error(err, "unable to create webhook", "webhook", "Decofile")
            os.Exit(1)
        }
    }
}
```

- [ ] **Step 5: Gate the deco controller block**

Replace lines 353–368 (from `registry := ...` to DecoReconciler `SetupWithManager`) with:

```go
if enabled(controller.DecoControllerName) {
    registry := build.NewBuilderRegistry()
    registry.Register("cloudflare-worker", build.NewCloudflareFactory(build.CfWorkersConfigFromEnv()))
    builderSAAnnotations := map[string]string{}
    if roleArn := os.Getenv("BUILD_ROLE_ARN"); roleArn != "" {
        builderSAAnnotations["eks.amazonaws.com/role-arn"] = roleArn
    }
    if err := (&controller.DecoReconciler{
        Client:               mgr.GetClient(),
        Scheme:               mgr.GetScheme(),
        Builder:              registry,
        BuilderSAAnnotations: builderSAAnnotations,
    }).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "unable to create controller", "controller", "Deco")
        os.Exit(1)
    }
}
```

- [ ] **Step 6: Gate the decoredirect controller block**

Replace lines 369–377 with:

```go
if enabled(controller.DecoRedirectControllerName) {
    if err := (&controller.DecoRedirectReconciler{
        Client:        mgr.GetClient(),
        Scheme:        mgr.GetScheme(),
        IngressClass:  redirectIngressClass,
        ClusterIssuer: redirectClusterIssuer,
    }).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "unable to create controller", "controller", "DecoRedirect")
        os.Exit(1)
    }
}
```

- [ ] **Step 7: Gate the operator-api block**

Replace lines 378–387 with:

```go
if enabled(api.ControllerName) {
    apiUser := os.Getenv("OPERATOR_API_USER")
    apiPass := os.Getenv("OPERATOR_API_PASSWORD")
    if apiUser != "" && apiPass != "" {
        h := api.NewHandlers(mgr.GetClient(), redirectNamespace)
        if err := mgr.Add(api.NewServer(operatorAPIAddr, apiUser, apiPass, h)); err != nil {
            setupLog.Error(err, "unable to add operator API server")
            os.Exit(1)
        }
        setupLog.Info("Operator API enabled", "addr", operatorAPIAddr)
    }
}
```

- [ ] **Step 8: Remove the old `valkeyClient` block (now moved inside namespace gate)**

Delete lines 259–279 (the original `var valkeyClient valkey.Client` switch block) since it's now inside the namespace gate.

- [ ] **Step 9: Build and run tests**

```bash
cd /Users/igoramf/projects/deco/operator
make test 2>&1 | tail -10
```

Expected: all tests pass.

- [ ] **Step 10: Commit**

```bash
git add cmd/main.go
git commit -m "feat(controllers): gate each controller behind --controllers flag"
```

---

## Task 4: Helm chart — `controllers.enabled` value + deployment arg

**Files:**
- Modify: `chart/values.yaml`
- Modify: `hack/helm-generator/main.go`

- [ ] **Step 1: Add `controllers` block to `chart/values.yaml`**

Add after the `podLabels: {}` block (around line 98), before the `cfworkers:` block:

```yaml
# Controllers to enable at startup.
# Use ["*"] to enable all (default — preserves existing behavior).
# Valid values: namespace, decofile, deco, decoredirect, operator-api
controllers:
  enabled:
    - "*"
```

- [ ] **Step 2: Add `addControllersArg` to `hack/helm-generator/main.go`**

Add this function after `addRedirectControllerArgs`:

```go
func addControllersArg(templatesDir string) error {
	files, err := filepath.Glob(filepath.Join(templatesDir, "deployment-*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no deployment file found")
	}
	deploymentFile := files[0]
	content, err := os.ReadFile(deploymentFile)
	if err != nil {
		return err
	}
	arg := `        {{- if not (has "*" .Values.controllers.enabled) }}
        - --controllers={{ join "," .Values.controllers.enabled }}
        {{- end }}`
	anchor := `        - --webhook-cert-path=/tmp/k8s-webhook-server/serving-certs`
	if !strings.Contains(string(content), anchor) {
		return fmt.Errorf("anchor %q not found in %s", anchor, deploymentFile)
	}
	contentStr := strings.Replace(string(content), anchor, anchor+"\n"+arg, 1)
	return os.WriteFile(deploymentFile, []byte(contentStr), 0644)
}
```

- [ ] **Step 3: Call `addControllersArg` in `main()`**

In `hack/helm-generator/main.go`, after the `addRedirectControllerArgs` call, add:

```go
if err := addControllersArg(templatesDir); err != nil {
    fmt.Fprintf(os.Stderr, "Warning: Could not add controllers arg: %v\n", err)
}
```

- [ ] **Step 4: Regenerate chart**

```bash
cd /Users/igoramf/projects/deco/operator
make generate 2>&1 | tail -5
```

Expected: exits 0.

- [ ] **Step 5: Verify default renders no `--controllers` flag**

```bash
helm template test chart/ 2>&1 | grep "\-\-controllers"
```

Expected: no output (default `["*"]` → flag not injected).

- [ ] **Step 6: Verify explicit list injects the flag**

```bash
helm template test chart/ \
  --set 'controllers.enabled[0]=decoredirect' \
  --set 'controllers.enabled[1]=operator-api' \
  2>&1 | grep "\-\-controllers"
```

Expected:
```
        - --controllers=decoredirect,operator-api
```

- [ ] **Step 7: Lint**

```bash
helm lint chart/
```

Expected: `1 chart(s) linted, 0 chart(s) failed`

- [ ] **Step 8: Commit**

```bash
git add chart/values.yaml hack/helm-generator/main.go chart/templates/deployment-operator-controller-manager.yaml
git commit -m "feat(chart): add controllers.enabled value and --controllers arg injection"
```

---

## Final check

- [ ] **Run full test suite**

```bash
cd /Users/igoramf/projects/deco/operator
make test 2>&1 | tail -10
```

Expected: all tests pass.

- [ ] **Verify branch log**

```bash
git log --oneline main..HEAD
```

Expected (4 commits):
```
<hash> feat(chart): add controllers.enabled value and --controllers arg injection
<hash> feat(controllers): gate each controller behind --controllers flag
<hash> feat(controllers): add parseControllers with knownControllers registry
<hash> feat(controllers): add ControllerName constants to each controller
```
