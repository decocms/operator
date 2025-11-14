# Watch Examples

Examples for watching Decofile rollout status.

## Quick Start

### TypeScript/Deno (Recommended)

```typescript
import { watchDecofileStatus } from "./watch-decofile-status.ts";
import { k8s } from "../deps.ts";

const kc = new k8s.KubeConfig();
kc.loadFromDefault();

// Wait for specific commit to be rolled out
const decofile = await watchDecofileStatus(
  kc,
  "sites-aviator",
  "production",
  5 * 60 * 1000 // 5 min timeout
);

if (decofile) {
  console.log("✅ Rollout complete!");
  // Check which commit was deployed
  console.log(`Commit: ${decofile.status.githubCommit}`);
}
```

## Handling Race Conditions

To avoid missing events, **check current state first** before watching:

```typescript
// 1. Update Decofile
await kubectl.apply(decofile);  // commit: abc123

// 2. Get current status IMMEDIATELY
const current = await k8s.customApi.getNamespacedCustomObject(
  "deco.sites",
  "v1alpha1",
  namespace,
  "decofiles",
  decofileName
);

// 3. Check if already satisfied
const podsNotified = current.status?.conditions?.find(
  c => c.type === "PodsNotified" && 
       c.status === "True" &&
       c.message.includes("commit:abc123")  // ← Match your commit!
);

if (podsNotified) {
  console.log("Already complete!");
  return;
}

// 4. NOW start watching (if not already done)
await watchDecofileStatus(...);
```

## Condition Message Format

The operator includes identifiers in the message:

**For GitHub source:**
```
Successfully notified all pods for commit:abc123def456
```

**For inline source:**
```
Successfully notified all pods for timestamp:1731598481
```

## Safe Watch Pattern

```typescript
async function waitForCommitRollout(
  namespace: string,
  decofileName: string,
  expectedCommit: string
): Promise<boolean> {
  const kc = new k8s.KubeConfig();
  kc.loadFromDefault();
  const customApi = kc.makeApiClient(k8s.CustomObjectsApi);
  
  // 1. Check current state
  const current = await customApi.getNamespacedCustomObject(
    "deco.sites",
    "v1alpha1",
    namespace,
    "decofiles",
    decofileName
  );
  
  const condition = current.body.status?.conditions?.find(
    c => c.type === "PodsNotified" && 
         c.status === "True" &&
         c.message.includes(`commit:${expectedCommit}`)
  );
  
  if (condition) {
    console.log("✅ Already rolled out");
    return true;
  }
  
  // 2. Watch for updates
  const decofile = await watchDecofileStatus(
    kc,
    namespace,
    decofileName,
    5 * 60 * 1000
  );
  
  // 3. Verify it's the right commit
  if (decofile?.status?.githubCommit === expectedCommit) {
    return true;
  }
  
  throw new Error("Rollout completed but commit mismatch");
}

// Usage
await waitForCommitRollout("sites-aviator", "production", "abc123def456");
```

## Using kubectl

```bash
# Watch with commit verification
kubectl get decofile production -n sites-aviator -w -o json | \
  jq -r 'select(.status.conditions[]? | 
    select(.type=="PodsNotified" and .status=="True" and (.message | contains("commit:abc123"))))'
```

## Benefits

- ✅ No race conditions (check first, then watch)
- ✅ Verify correct update (via commit/timestamp)
- ✅ Works even if notification happened before watch starts
- ✅ Clear tracking of which change was deployed

