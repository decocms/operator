// Test application for Decofile Operator
// Responds to reload requests and reads mounted decofile.json

const CONFIG_FILE = "/app/decofile/decofile.json";

async function handleReload(delayMs?: number): Promise<Response> {
  console.log("=== RELOAD REQUEST RECEIVED ===");
  console.log(`Reading from: ${CONFIG_FILE}`);
  console.log(`Timestamp: ${new Date().toISOString()}`);
  console.log(`DECO_RELEASE env: ${Deno.env.get("DECO_RELEASE") || "not set"}`);
  
  // Wait for ConfigMap to propagate if delay is specified
  if (delayMs && delayMs > 0) {
    console.log(`⏳ Waiting ${delayMs}ms for ConfigMap to sync...`);
    await new Promise(resolve => setTimeout(resolve, delayMs));
    console.log(`✓ Wait complete, reading file now`);
  }
  
  try {
    // Read the decofile.json which contains all files
    const fileContent = await Deno.readTextFile(CONFIG_FILE);
    const filesMap = JSON.parse(fileContent);
    
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
    const errorMsg = `Error reading decofile.json: ${error.message}`;
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
    "Decofile Test App\n" +
    "Endpoints:\n" +
    "  GET /health - Health check\n" +
    "  GET /deco/.decofile/reload?delay=<ms> - Reload configuration\n",
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
    console.log(`🚀 Decofile Test App listening on http://${hostname}:${port}`);
    console.log(`📄 Config file: ${CONFIG_FILE}`);
    console.log(`🌍 DECO_RELEASE: ${Deno.env.get("DECO_RELEASE") || "not set"}`);
  }
}, async (req) => {
  const url = new URL(req.url);
  const { pathname } = url;
  
  console.log(`${req.method} ${pathname}${url.search}`);
  
  if (pathname === "/health") {
    return handleHealth();
  }
  
  if (pathname === "/deco/.decofile/reload") {
    // Parse delay query parameter
    const delayParam = url.searchParams.get("delay");
    const delayMs = delayParam ? parseInt(delayParam, 10) : undefined;
    return await handleReload(delayMs);
  }
  
  if (pathname === "/") {
    return handleRoot();
  }
  
  return new Response("Not Found\n", { 
    status: 404,
    headers: { "Content-Type": "text/plain" }
  });
});
