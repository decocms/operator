# Application Integration Guide

This guide explains how to integrate your Deno/TypeScript application with the Deco CMS Operator to support configuration reloading.

## Overview

The operator will:
1. Mount `decofile.json` and `timestamp.txt` to `/app/decofile/` (configurable)
2. Set `DECO_RELEASE` environment variable pointing to the file
3. Call `/.decofile/reload?timestamp=<ts>&tsFile=<path>` when config changes
4. Long-poll until your app confirms it has the updated timestamp

## Required Endpoint Implementation

### Endpoint: `POST /.decofile/reload`

**Request Body (JSON):**
```json
{
  "timestamp": "1731598481",
  "source": "operator",
  "decofile": {
    "config": {"environment": "production"},
    "data": {"message": "hello"}
  }
}
```

**Behavior:**
- Receive POST notification with decofile content
- Apply configuration to your application
- Return 200 OK on success
- Return 500 on error

**Note:** The decofile content is sent in the payload, so you don't need to read from disk. However, the files are still mounted for initial load and fallback.

### TypeScript/Deno Implementation

```typescript
// config.ts - Configuration loader with compression support
export interface DecofileConfig {
  [key: string]: unknown;
}

export async function loadConfig(basePath?: string): Promise<DecofileConfig> {
  const configDir = basePath || Deno.env.get("DECO_RELEASE")?.replace("file://", "").replace("/decofile.json", "");
  if (!configDir) {
    throw new Error("No config path specified and DECO_RELEASE not set");
  }
  
  // Check if compressed (.bin file exists)
  try {
    const binContent = await Deno.readTextFile(`${configDir}/decofile.bin`);
    // File exists and is compressed - decompress it
    const compressed = Uint8Array.from(atob(binContent), c => c.charCodeAt(0));
    const decompressed = await decompressBrotli(compressed);
    return JSON.parse(decompressed);
  } catch (error) {
    // .bin doesn't exist or error reading - try .json
    if (error.name !== "NotFound") {
      console.warn("Error reading compressed config, falling back to json:", error);
    }
  }
  
  // Read uncompressed .json
  const content = await Deno.readTextFile(`${configDir}/decofile.json`);
  return JSON.parse(content);
}

// Brotli decompression using DecompressionStream
async function decompressBrotli(data: Uint8Array): Promise<string> {
  const stream = new DecompressionStream("deflate-raw"); // Note: Browser API, or use npm:brotli-wasm
  const blob = new Blob([data]);
  const decompressedStream = blob.stream().pipeThrough(stream);
  const decompressedBlob = await new Response(decompressedStream).blob();
  return await decompressedBlob.text();
  
  // Alternative: Use brotli-wasm for better compatibility
  // import { decompress } from "https://deno.land/x/brotli/mod.ts";
  // return new TextDecoder().decode(decompress(data));
}

// reload.ts - Reload endpoint handler
async function handleReload(req: Request): Promise<Response> {
  console.log("=== RELOAD REQUEST ===");
  console.log(`Timestamp: ${new Date().toISOString()}`);
  
  // Parse JSON payload
  try {
    const payload = await req.json();
    console.log(`📦 Received notification:`, payload);
    console.log(`   Timestamp: ${payload.timestamp}`);
  } catch {
    console.log("   No payload (optional)");
  }
  
  try {
    // Reload configuration from mounted files
    const config = await loadConfig();
    const fileCount = Object.keys(config).length;
    
    console.log(`✓ Loaded ${fileCount} config files`);
    
    // TODO: Apply configuration to your application
    // - Update in-memory state
    // - Refresh caches
    // - Reload components
    // etc.
    
    console.log("=== RELOAD COMPLETE ===\n");
    
    return new Response(`Reloaded ${fileCount} files\n`, { status: 200 });
  } catch (error) {
    console.error(`Error reloading: ${error.message}`);
    return new Response(`Error: ${error.message}\n`, { status: 500 });
  }
}

// server.ts - HTTP server
Deno.serve({ port: 8000 }, async (req) => {
  const url = new URL(req.url);
  
  if (url.pathname === "/.decofile/reload") {
    return await handleReload(req);
  }
  
  if (url.pathname === "/health") {
    return new Response("OK\n", { status: 200 });
  }
  
  // ... your other routes
});
```

## Environment Variables

Your application receives:

```typescript
// DECO_RELEASE points to the config file
const configPath = Deno.env.get("DECO_RELEASE");
// Example: "file:///app/decofile/decofile.json"

// Parse the file path
const filePath = configPath?.replace("file://", "");
// Example: "/app/decofile/decofile.json"
```

## File Structure

Mounted at `/app/decofile/` (or custom path):

```
/app/decofile/
├── decofile.json    # Your configuration
└── timestamp.txt    # Update timestamp
```

### decofile.json Format

For configs < 2.5MB (uncompressed):

```json
{
  "config": {
    "environment": "production",
    "apiUrl": "https://api.example.com"
  },
  "data": {
    "message": "Hello",
    "version": "1.0"
  },
  "Campaign Timer - 01": {
    "link": {"href": "...", "text": "..."}
  }
}
```

**For large configs >= 2.5MB:**

The operator automatically compresses with Brotli and stores as `decofile.bin` (base64-encoded).

Your app should check for `_compressed` flag and decompress if needed (see example below).

**Notes:**
- Keys have `.json` extension stripped
- Filenames are URL-decoded (spaces, not `%20`)
- HTML characters not escaped (`&`, `<`, `>`)
- Large configs auto-compressed with Brotli

### timestamp.txt Format

```
1731598481
```

Unix timestamp in seconds since epoch (UTC)

## Complete Example

```typescript
// main.ts
import { serve } from "https://deno.land/std@0.208.0/http/server.ts";

// Configuration state
let config: Record<string, unknown> = {};

// Load initial config
async function loadConfig(): Promise<void> {
  const configPath = Deno.env.get("DECO_RELEASE")?.replace("file://", "");
  if (!configPath) {
    throw new Error("DECO_RELEASE not set");
  }
  
  const content = await Deno.readTextFile(configPath);
  config = JSON.parse(content);
  console.log(`✓ Loaded ${Object.keys(config).length} config files`);
}

// Reload endpoint with long-polling
async function handleReload(req: Request): Promise<Response> {
  const url = new URL(req.url);
  const expectedTimestamp = url.searchParams.get("timestamp");
  const tsFilePath = url.searchParams.get("tsFile");
  
  console.log("=== RELOAD REQUEST ===");
  
  // Long-poll if timestamp provided
  if (expectedTimestamp && tsFilePath) {
    console.log(`Waiting for timestamp: ${expectedTimestamp}`);
    
    const maxWait = 120_000; // 120 seconds
    const pollInterval = 2000; // 2 seconds
    const start = Date.now();
    
  while (Date.now() - start < maxWait) {
    try {
      const fileTsStr = (await Deno.readTextFile(tsFilePath)).trim();
      const fileTs = parseInt(fileTsStr, 10);
      const expectedTs = parseInt(expectedTimestamp, 10);
      
      if (fileTs >= expectedTs) {
        console.log(`✓ Timestamp satisfied: ${fileTs} >= ${expectedTs}`);
        break;
      }
        
        await new Promise(r => setTimeout(r, pollInterval));
      } catch {
        await new Promise(r => setTimeout(r, pollInterval));
      }
    }
  }
  
  // Reload config
  await loadConfig();
  
  // Apply changes to your app
  // - Clear caches
  // - Update state
  // - Refresh components
  
  console.log("=== RELOAD COMPLETE ===");
  
  return new Response("OK\n", { status: 200 });
}

// Server
serve(async (req) => {
  const url = new URL(req.url);
  
  if (url.pathname === "/.decofile/reload") {
    return handleReload(req);
  }
  
  if (url.pathname === "/health") {
    return new Response("OK\n", { status: 200 });
  }
  
  // Your app logic here
  return new Response("Not Found\n", { status: 404 });
}, { port: 8000 });

// Load initial config on startup
await loadConfig();
console.log("✅ Application started");
```

## Best Practices

### 1. Graceful Reload

```typescript
async function applyConfig(newConfig: Record<string, unknown>): Promise<void> {
  // Validate config first
  validateConfig(newConfig);
  
  // Apply atomically
  const oldConfig = config;
  try {
    config = newConfig;
    // Refresh dependent systems
  } catch (error) {
    // Rollback on failure
    config = oldConfig;
    throw error;
  }
}
```

### 2. Health Check

```typescript
function handleHealth(): Response {
  // Check if config is loaded
  if (Object.keys(config).length === 0) {
    return new Response("Config not loaded\n", { status: 503 });
  }
  
  return new Response("OK\n", { status: 200 });
}
```

### 3. Error Handling

```typescript
try {
  await loadConfig();
} catch (error) {
  console.error("Failed to load config:", error);
  // Use default config or exit
  Deno.exit(1);
}
```

## Testing Locally

```typescript
// Set env var
Deno.env.set("DECO_RELEASE", "file:///app/decofile/decofile.json");

// Create test files
await Deno.writeTextFile("/app/decofile/decofile.json", JSON.stringify({
  config: { environment: "test" },
  data: { message: "hello" }
}));

await Deno.writeTextFile("/app/decofile/timestamp.txt", new Date().toISOString());

// Test reload
const response = await fetch("http://localhost:8000/.decofile/reload?timestamp=" + 
  encodeURIComponent(new Date().toISOString()) + 
  "&tsFile=/app/decofile/timestamp.txt"
);

console.log(await response.text()); // "OK"
```

## Deployment

Your Knative Service needs the annotation:

```yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  annotations:
    deco.sites/decofile-inject: "default"  # or specific decofile name
spec:
  template:
    spec:
      containers:
        - name: app
          image: your-app:latest
          ports:
            - containerPort: 8000  # Operator detects this port
```

The operator automatically:
- ✅ Mounts `/app/decofile/decofile.json`
- ✅ Mounts `/app/decofile/timestamp.txt`
- ✅ Sets `DECO_RELEASE=file:///app/decofile/decofile.json`
- ✅ Labels pod with `deco.sites/decofile: <name>`
- ✅ Calls `/.decofile/reload` on config changes

## Troubleshooting

### Reload endpoint not being called

```bash
# Check pod labels
kubectl get pods -n your-namespace -l deco.sites/decofile=your-decofile

# Check operator logs
kubectl logs -n operator-system -l control-plane=controller-manager -f
```

### Timestamp not updating

```bash
# Check ConfigMap
kubectl get configmap decofile-your-decofile -n your-namespace -o yaml

# Check mounted files in pod
kubectl exec -n your-namespace your-pod -- cat /app/decofile/timestamp.txt
```

### Long-poll timeout

- Increase max wait time in your app
- Check kubelet sync interval
- Verify file system permissions

## TypeScript Types

See [types/decofile.ts](../types/decofile.ts) for complete type definitions:

```typescript
import type { DecofileJSON, DecofileEnv } from "https://raw.githubusercontent.com/decocms/operator/main/types/decofile.ts";

const config: DecofileJSON = await loadConfig();
```

## Support

- GitHub Issues: https://github.com/decocms/operator/issues
- Documentation: https://github.com/decocms/operator
- Examples: See `test/kind/app/main.ts` for reference implementation

