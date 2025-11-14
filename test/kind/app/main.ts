// Test application for Deco CMS Operator
// Responds to reload requests with long-polling timestamp verification
// Supports both compressed and uncompressed configs

const CONFIG_DIR = "/app/decofile";

// Load config with automatic decompression
async function loadConfig(): Promise<Record<string, unknown>> {
  // Check if compressed (.bin file exists)
  try {
    const binContent = await Deno.readTextFile(`${CONFIG_DIR}/decofile.bin`);
    console.log("📦 Detected compressed config (decofile.bin)");
    // Note: Deno doesn't have native brotli support yet
    // In production, use: import { decompress } from "https://deno.land/x/brotli@v0.1.7/mod.ts";
    throw new Error("Brotli decompression not implemented in test app - use brotli library in production");
  } catch (error) {
    if (error.message.includes("not implemented")) {
      throw error;
    }
    // .bin doesn't exist, continue to .json
  }
  
  // Read uncompressed .json
  const content = await Deno.readTextFile(`${CONFIG_DIR}/decofile.json`);
  return JSON.parse(content);
}

async function handleReload(expectedTimestamp?: string, tsFilePath?: string): Promise<Response> {
  console.log("=== RELOAD REQUEST RECEIVED ===");
  console.log(`Reading from: ${CONFIG_DIR}`);
  console.log(`Current time: ${new Date().toISOString()}`);
  console.log(`DECO_RELEASE env: ${Deno.env.get("DECO_RELEASE") || "not set"}`);
  
  // Long-polling: wait for timestamp file to be >= expected timestamp
  if (expectedTimestamp && tsFilePath) {
    console.log(`⏳ Long-polling for timestamp >= ${expectedTimestamp} (Unix seconds)`);
    console.log(`   Timestamp file: ${tsFilePath}`);
    
    const expectedTs = parseInt(expectedTimestamp, 10);
    const maxWaitSeconds = 120;
    const pollIntervalMs = 2000;
    const startTime = Date.now();
    
    while ((Date.now() - startTime) < maxWaitSeconds * 1000) {
      try {
        const fileTimestamp = await Deno.readTextFile(tsFilePath);
        const fileTs = parseInt(fileTimestamp.trim(), 10);
        
        console.log(`   Current file timestamp: ${fileTs} (${new Date(fileTs * 1000).toISOString()})`);
        
        // Compare Unix timestamps
        if (fileTs >= expectedTs) {
          console.log(`✓ Timestamp satisfied! (${fileTs} >= ${expectedTs})`);
          break;
        }
        
        console.log(`   Waiting... (${Math.round((Date.now() - startTime) / 1000)}s elapsed)`);
        await new Promise(resolve => setTimeout(resolve, pollIntervalMs));
      } catch (error) {
        console.error(`   Error reading timestamp file: ${error.message}`);
        await new Promise(resolve => setTimeout(resolve, pollIntervalMs));
      }
    }
  }
  
  try {
    // Load config (handles compression automatically)
    const filesMap = await loadConfig();
    
    const fileNames = Object.keys(filesMap);
    console.log(`\n📦 Found ${fileNames.length} file(s) in decofile.json`);
    
    // Log first 5 files for debugging
    const filesToShow = fileNames.slice(0, 5);
    for (const filename of filesToShow) {
      console.log(`\nFile: ${filename}`);
      const content = JSON.stringify(filesMap[filename], null, 2);
      console.log(`Content preview (first 200 chars):\n${content.substring(0, 200)}...`);
      console.log("---");
    }
    
    if (fileNames.length > 5) {
      console.log(`\n... and ${fileNames.length - 5} more files`);
    }
    
    const message = `✓ Reloaded decofile.json with ${fileNames.length} file(s)`;
    console.log(message);
    console.log("=== RELOAD COMPLETE ===\n");
    
    return new Response(message + "\n", { 
      status: 200,
      headers: { "Content-Type": "text/plain" }
    });
  } catch (error) {
    const errorMsg = `Error reading decofile: ${error.message}`;
    console.error(errorMsg);
    console.error(error);
    return new Response(errorMsg + "\n", { 
      status: 500,
      headers: { "Content-Type": "text/plain" }
    });
  }
}

function handleHealth(): Response {
  return new Response("OK\n", { 
    status: 200,
    headers: { "Content-Type": "text/plain" }
  });
}

function handleRoot(): Response {
  return new Response(
    "Deco CMS Test App\n" +
    "Endpoints:\n" +
    "  GET /health - Health check\n" +
    "  GET /.decofile/reload?timestamp=<ts>&tsFile=<path> - Reload with long-polling\n",
    { 
      status: 200,
      headers: { "Content-Type": "text/plain" }
    }
  );
}

// Main server
Deno.serve({ 
  port: 8080,
  onListen: ({ hostname, port }) => {
    console.log(`🚀 Deco CMS Test App listening on http://${hostname}:${port}`);
    console.log(`📁 Config directory: ${CONFIG_DIR}`);
    console.log(`🌍 DECO_RELEASE: ${Deno.env.get("DECO_RELEASE") || "not set"}`);
  }
}, async (req) => {
  const url = new URL(req.url);
  const { pathname } = url;
  
  console.log(`${req.method} ${pathname}${url.search}`);
  
  if (pathname === "/health") {
    return handleHealth();
  }
  
  if (pathname === "/.decofile/reload") {
    // Parse long-polling parameters
    const expectedTimestamp = url.searchParams.get("timestamp") || undefined;
    const tsFilePath = url.searchParams.get("tsFile") || undefined;
    return await handleReload(expectedTimestamp, tsFilePath);
  }
  
  if (pathname === "/") {
    return handleRoot();
  }
  
  return new Response("Not Found\n", { 
    status: 404,
    headers: { "Content-Type": "text/plain" }
  });
});
