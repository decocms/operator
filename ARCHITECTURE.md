# Deco CMS Operator Architecture

This document provides visual diagrams and architectural overview of the Deco CMS Operator.

## Complete System Flow

```mermaid
flowchart TB
    subgraph "1. Configuration Source"
        User[User/GitOps] -->|kubectl apply| Decofile[Decofile CRD]
        Decofile -->|spec.source=inline| Inline[Inline JSON]
        Decofile -->|spec.source=github| GitHub[GitHub Repository]
    end

    subgraph "2. Decofile Controller"
        Decofile --> Controller[Decofile Controller]
        Controller -->|Fetch| Inline
        Controller -->|Download ZIP| GitHub
        Controller --> Cache{Commit Changed?}
        Cache -->|No| Skip[Skip Download]
        Cache -->|Yes| Retrieve[Retrieve Content]
        Retrieve --> Size{Size > 2.5MB?}
        Size -->|Yes| Compress[Brotli Compress<br/>~5-10% ratio]
        Size -->|No| NoCompress[Keep JSON]
        Compress --> BinCM[ConfigMap<br/>decofile.bin<br/>timestamp.txt]
        NoCompress --> JsonCM[ConfigMap<br/>decofile.json<br/>timestamp.txt]
    end

    subgraph "3. Webhook Injection"
        KService[Knative Service<br/>deco.sites/decofile-inject] --> Webhook[Mutating Webhook]
        Webhook -->|Get| Decofile
        Webhook -->|Check Format| BinCM
        Webhook -->|Check Format| JsonCM
        Webhook -->|Mount Volume| PodSpec[Pod Spec]
        Webhook -->|Set DECO_RELEASE| EnvVar[file:///app/decofile/decofile.bin<br/>or .json]
        Webhook -->|Add Label| PodLabel[deco.sites/decofile: name]
    end

    subgraph "4. Change Detection & Notification"
        BinCM -.->|Watch| Controller
        JsonCM -.->|Watch| Controller
        Controller --> Changed{Content<br/>Changed?}
        Changed -->|No| Done1[Done]
        Changed -->|Yes| Silent{spec.silent?}
        Silent -->|true| SkipNotify[Skip Notifications<br/>Update Status Only]
        Silent -->|false| SetInProgress[Set Condition:<br/>PodsNotified=Unknown]
        SetInProgress --> FindPods[Find Pods by Label]
        FindPods --> Parallel[Parallel Notifications<br/>30 pods at once<br/>5 min timeout]
        Parallel -->|HTTP GET| Pod1[Pod 1<br/>/.decofile/reload]
        Parallel -->|HTTP GET| Pod2[Pod 2<br/>/.decofile/reload]
        Parallel -->|HTTP GET| PodN[Pod N<br/>/.decofile/reload]
    end

    subgraph "5. Pod Long-Polling"
        Pod1 --> LongPoll1[Poll timestamp.txt<br/>every 2s, max 120s]
        LongPoll1 --> Check1{timestamp >= expected?}
        Check1 -->|Yes| Reload1[Reload Config<br/>Return 200 OK]
        Check1 -->|No| Wait1[Wait 2s] --> LongPoll1
        
        Reload1 --> Success[All Pods Confirmed]
        Success --> UpdateCondition[Set Condition:<br/>PodsNotified=True<br/>commit:abc123]
    end

    subgraph "6. Watch Status"
        UpdateCondition -.->|Event| Watcher[Client Watcher]
        Watcher --> CheckCondition{PodsNotified<br/>== True?}
        CheckCondition -->|Yes + Commit Match| ClientSuccess[✅ Rollout Complete]
        CheckCondition -->|False| ClientFail[❌ Notification Failed]
        CheckCondition -->|Unknown| ClientWait[⏳ In Progress]
    end

    style Compress fill:#ffebcd
    style BinCM fill:#ffebcd
    style Silent fill:#e6f3ff
    style SkipNotify fill:#e6f3ff
    style UpdateCondition fill:#d4edda
    style ClientSuccess fill:#d4edda
```

## Detailed Component Flows

### Flow 1: Inline Source

```mermaid
sequenceDiagram
    participant User
    participant K8s as Kubernetes API
    participant Ctrl as Decofile Controller
    participant CM as ConfigMap

    User->>K8s: Create Decofile (inline)
    K8s->>Ctrl: Reconcile Event
    Ctrl->>Ctrl: Parse inline.value JSON
    Ctrl->>Ctrl: Strip .json extensions
    Ctrl->>Ctrl: Size > 2.5MB?
    alt Large Config
        Ctrl->>Ctrl: Brotli Compress
        Ctrl->>CM: Create decofile.bin + timestamp.txt
    else Small Config
        Ctrl->>CM: Create decofile.json + timestamp.txt
    end
    Ctrl->>K8s: Update Status (Ready=True)
```

### Flow 2: GitHub Source with Caching

```mermaid
sequenceDiagram
    participant User
    participant K8s as Kubernetes API
    participant Ctrl as Decofile Controller
    participant GH as GitHub
    participant CM as ConfigMap

    User->>K8s: Create/Update Decofile (github)
    K8s->>Ctrl: Reconcile Event
    Ctrl->>Ctrl: Check Status.GitHubCommit
    
    alt Commit Changed
        Ctrl->>GH: Download ZIP (codeload.github.com)
        GH->>Ctrl: Return ZIP
        Ctrl->>Ctrl: Extract files from path
        Ctrl->>Ctrl: URL decode filenames
        Ctrl->>Ctrl: Strip .json extensions
        Ctrl->>Ctrl: Disable HTML escaping
        Ctrl->>Ctrl: Size > 2.5MB?
        alt Large
            Ctrl->>Ctrl: Brotli Compress (5-10% ratio)
            Ctrl->>CM: Update decofile.bin + new timestamp
        else Small
            Ctrl->>CM: Update decofile.json + new timestamp
        end
        Ctrl->>K8s: Update Status (GitHubCommit=abc123)
    else Commit Unchanged
        Ctrl->>Ctrl: Skip download (cached)
        Ctrl->>K8s: Status unchanged
    end
```

### Flow 3: Webhook Injection

```mermaid
sequenceDiagram
    participant User
    participant K8s as Kubernetes API
    participant Webhook as Mutating Webhook
    participant Ctrl as Decofile Controller
    participant CM as ConfigMap

    User->>K8s: Create Knative Service<br/>(deco.sites/decofile-inject)
    K8s->>Webhook: Intercept CREATE
    Webhook->>Webhook: Resolve Decofile name
    Webhook->>K8s: Get Decofile resource
    Webhook->>K8s: Get ConfigMap
    Webhook->>Webhook: Check if .bin or .json
    Webhook->>Webhook: Inject Volume (ConfigMap)
    Webhook->>Webhook: Inject VolumeMount (/app/decofile/)
    Webhook->>Webhook: Inject Env (DECO_RELEASE=file:///.../decofile.{bin|json})
    Webhook->>Webhook: Add Label (deco.sites/decofile)
    Webhook->>K8s: Return Modified Service
    K8s->>K8s: Create Pod with injected config
```

### Flow 4: Change Notification (Non-Silent)

```mermaid
sequenceDiagram
    participant Ctrl as Controller
    participant K8s as Kubernetes API
    participant Pod1 as Pod 1
    participant Pod2 as Pod 2
    participant PodN as Pod N

    Ctrl->>Ctrl: Detect ConfigMap change
    Ctrl->>K8s: Update Condition<br/>(PodsNotified=Unknown)
    Ctrl->>K8s: List Pods (label selector)
    
    par Parallel Notifications (30 concurrent)
        Ctrl->>K8s: Get Pod 1 (fresh data)
        K8s->>Ctrl: Pod 1 details
        Ctrl->>Pod1: GET /.decofile/reload?<br/>timestamp=1731598481&<br/>tsFile=/app/decofile/timestamp.txt
        Pod1->>Pod1: Long-poll timestamp.txt<br/>(every 2s, max 120s)
        Pod1->>Pod1: timestamp >= 1731598481?
        Pod1->>Ctrl: 200 OK ✅
    and
        Ctrl->>K8s: Get Pod 2 (fresh data)
        Ctrl->>Pod2: GET /.decofile/reload?...
        Pod2->>Ctrl: 200 OK ✅
    and
        Ctrl->>K8s: Get Pod N (fresh data)
        Ctrl->>PodN: GET /.decofile/reload?...
        PodN->>Ctrl: 200 OK ✅
    end
    
    Ctrl->>K8s: Update Condition<br/>(PodsNotified=True, commit:abc123)
    
    Note over Ctrl,PodN: Maximum 5 minutes for all pods
```

### Flow 5: Silent Mode

```mermaid
flowchart LR
    A[ConfigMap Changed] --> B{spec.silent?}
    B -->|true| C[Skip Notifications]
    C --> D[Update Status Only]
    D --> E[Pods poll/restart<br/>to get updates]
    
    B -->|false| F[Notify Pods]
    F --> G[Set PodsNotified]
    G --> H[Clients can watch]
    
    style C fill:#e6f3ff
    style D fill:#e6f3ff
    style E fill:#e6f3ff
```

### Flow 6: Watch Pattern (Race-Free)

```mermaid
sequenceDiagram
    participant Client
    participant K8s as Kubernetes API
    participant Ctrl as Controller

    Client->>K8s: Update Decofile<br/>(commit: abc123)
    Client->>K8s: GET Decofile (current state)
    K8s->>Client: Return current status
    Client->>Client: Check PodsNotified<br/>for commit:abc123
    
    alt Already Complete
        Client->>Client: ✅ Rollout done
    else Not Complete
        Client->>K8s: Start Watch
        Ctrl->>Ctrl: Process change
        Ctrl->>K8s: Update PodsNotified=Unknown
        K8s->>Client: Watch Event (Unknown)
        Ctrl->>Ctrl: Notify pods...
        Ctrl->>K8s: Update PodsNotified=True<br/>(commit:abc123)
        K8s->>Client: Watch Event (True)
        Client->>Client: ✅ Rollout confirmed
    end
```

## Key Design Decisions

### 1. Compression Strategy
- **Threshold**: 2.5MB (ConfigMap limit is 3MB)
- **Algorithm**: Brotli (best compression for JSON)
- **Ratio**: Typically 5-10% of original size
- **Example**: 4.2MB → 231KB (5.5%)
- **Storage**: Base64-encoded in `decofile.bin`
- **Detection**: Presence of `.bin` file (no flag needed)

### 2. Notification Strategy
- **Parallel**: 30 pods concurrently
- **Timeout**: 5 minutes total, 2.5 minutes per pod
- **Retries**: 2 attempts per pod
- **Long-polling**: App waits for timestamp file
- **Fresh Data**: Fetches each pod individually before notification
- **Resilient**: Handles pod churn gracefully

### 3. Timestamp System
- **Format**: Unix timestamp (seconds since epoch)
- **Purpose**: Deterministic update verification
- **Flow**: Operator → ConfigMap → Pod polls → Confirms
- **Benefit**: No guessing delays, guaranteed sync

### 4. GitHub Caching
- **Cache Key**: Commit SHA
- **Logic**: Only download if commit changed
- **Benefit**: Eliminates redundant API calls
- **Status**: Stores `GitHubCommit` in status

### 5. Condition Tracking
- **Ready**: ConfigMap created successfully
- **PodsNotified**: All pods confirmed update
- **States**: Unknown (in progress) → True/False (complete)
- **Identifier**: Includes `commit:abc123` or `timestamp:123456`
- **Reset**: Set to Unknown on each new change

## Performance Characteristics

### Small Config (< 2.5MB)
- No compression overhead
- ~200-500ms to create ConfigMap
- ~2-3 minutes for 50 pods notification

### Large Config (4MB example)
- Compression: ~4-5 seconds
- Result: 231KB (5.5% of original)
- Fits in ConfigMap easily
- Same notification time

### Parallel Notification
- 30 pods: ~2.5 minutes (1 batch)
- 60 pods: ~2.5 minutes (2 concurrent batches)
- 100 pods: ~3-4 minutes
- 150 pods: ~4-5 minutes (within limit)

## Security Model

```mermaid
flowchart LR
    subgraph "GitHub Token"
        Secret[Kubernetes Secret]
        Env[GITHUB_TOKEN env var]
        Secret -.->|fallback| Env
    end
    
    subgraph "Controller"
        Ctrl[Controller Pod]
        Ctrl -->|Read| Secret
        Ctrl -->|Read| Env
    end
    
    subgraph "TLS"
        CertMgr[cert-manager]
        CertMgr -->|Issues| Cert[TLS Certificate]
        Cert -->|Mounts| Webhook[Webhook Pod]
    end
    
    subgraph "RBAC"
        SA[ServiceAccount]
        SA -->|Grants| Perms[Permissions:<br/>- Decofiles: CRUD<br/>- ConfigMaps: CRUD<br/>- Pods: Read<br/>- Secrets: Read]
    end
```

## Data Flow

```mermaid
flowchart LR
    subgraph "Sources"
        GH[GitHub Repo<br/>360 files<br/>4.2MB] -->|Download| Ctrl
        Inline[Inline JSON<br/>50KB] -->|Parse| Ctrl
    end
    
    subgraph "Operator Processing"
        Ctrl[Controller] -->|URL Decode| Clean["Campaign Timer - 01"<br/>not Campaign%20Timer]
        Clean -->|Strip Extension| Keys["config" not "config.json"]
        Keys -->|No HTML Escape| Chars["& not \u0026"]
        Chars -->|Compress if >2.5MB| Final
    end
    
    subgraph "Storage"
        Final -->|Small| CM1[decofile.json: 50KB]
        Final -->|Large| CM2[decofile.bin: 231KB]
        CM1 --> TS1[timestamp.txt]
        CM2 --> TS2[timestamp.txt]
    end
    
    subgraph "Application"
        TS1 -.->|Mount| App[Application Pod]
        TS2 -.->|Mount| App
        App -->|Read| Load{Check Extension}
        Load -->|.bin| Decompress[Decompress Brotli]
        Load -->|.json| Parse[Parse JSON]
        Decompress --> Config[Config Object]
        Parse --> Config
    end
```

## Notification Timeline

```mermaid
gantt
    title Pod Notification Timeline (50 pods)
    dateFormat X
    axisFormat %M:%S
    
    section Batch 1 (1-30)
    Pod 1-30 notification : 0, 150s
    
    section Batch 2 (31-50)
    Pod 31-50 notification : 0, 150s
    
    section Summary
    Total time : milestone, 150s
```

**With parallel batches:**
- Batches 1 & 2 run concurrently
- Total time: ~2.5 minutes for 50 pods
- All within 5-minute limit ✅

## State Machine: PodsNotified Condition

```mermaid
stateDiagram-v2
    [*] --> NotPresent: Initial state
    NotPresent --> Unknown: Config change detected
    Unknown --> True: All pods notified successfully
    Unknown --> False: Notification failed
    True --> Unknown: New config change
    False --> Unknown: Retry/New change
    
    note right of Unknown
        Message: "Notifying pods for commit:abc123"
        or "timestamp:1731598481"
    end note
    
    note right of True
        Message: "Successfully notified all pods for commit:abc123"
        lastTransitionTime updated
    end note
    
    note right of False
        Message: "Failed to notify pods for commit:abc123: timeout"
        Will retry on requeue
    end note
```

## Compression Decision Tree

```mermaid
flowchart TD
    Start[JSON Content] --> Check{Size > 2.5MB?}
    Check -->|No| Store1[Store as<br/>decofile.json]
    Check -->|Yes| Compress[Brotli Compress<br/>Level: Best]
    Compress --> Encode[Base64 Encode]
    Encode --> Store2[Store as<br/>decofile.bin]
    Store1 --> App1[App reads JSON directly]
    Store2 --> App2[App detects .bin<br/>Base64 decode<br/>Brotli decompress<br/>Parse JSON]
    App1 --> Config[Config Object]
    App2 --> Config
    
    style Compress fill:#ffebcd
    style Encode fill:#ffebcd
    style Store2 fill:#ffebcd
```

## Silent Mode vs Normal Mode

```mermaid
flowchart TB
    subgraph "Normal Mode (silent: false)"
        N1[Config Change] --> N2[Set PodsNotified=Unknown]
        N2 --> N3[Notify Pods<br/>30 parallel]
        N3 --> N4[Wait for confirmations]
        N4 --> N5[Set PodsNotified=True<br/>with commit/timestamp]
        N5 --> N6[Clients can watch]
    end
    
    subgraph "Silent Mode (silent: true)"
        S1[Config Change] --> S2[Update ConfigMap]
        S2 --> S3[Update Status]
        S3 --> S4[No notifications]
        S4 --> S5[Pods get updates via<br/>kubelet sync ~60s]
        S5 --> S6[No PodsNotified condition]
    end
    
    style N2 fill:#d4edda
    style N5 fill:#d4edda
    style S4 fill:#e6f3ff
    style S6 fill:#e6f3ff
```

## Technology Stack

```mermaid
graph TB
    subgraph "Operator"
        Go[Go 1.21+]
        SDK[Operator SDK]
        CR[controller-runtime v0.21]
        Brotli[andybalholm/brotli]
    end
    
    subgraph "Kubernetes"
        CRD[CustomResourceDefinition<br/>Decofile]
        Webhook[MutatingWebhook]
        CertMgr[cert-manager]
    end
    
    subgraph "Platform"
        Knative[Knative Serving]
        K8s[Kubernetes 1.16+]
    end
    
    subgraph "CI/CD"
        Actions[GitHub Actions]
        GHCR[GitHub Container Registry]
        Helm[Helm Chart]
    end
    
    Go --> SDK
    SDK --> CR
    CR --> CRD
    CRD --> K8s
    Webhook --> CertMgr
    CertMgr --> K8s
    Webhook --> Knative
    Actions --> GHCR
    Helm --> K8s
```

## Summary

The Deco CMS Operator provides:

1. **Flexible Configuration**: Inline or GitHub sources
2. **Intelligent Compression**: Automatic Brotli for large configs
3. **Smart Caching**: Avoids redundant GitHub downloads
4. **Reliable Notifications**: Parallel, time-bounded, with retries
5. **Deterministic Updates**: Timestamp-based verification
6. **Trackable Rollouts**: Watch conditions with commit/timestamp
7. **Production Ready**: Handles scale, failures, and edge cases

**All with zero configuration required from users!** 🚀

