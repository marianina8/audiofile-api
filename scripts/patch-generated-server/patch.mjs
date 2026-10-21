// Source edits for scripts/patch-generated-server.sh (openapi-mcp-generator 4.1.0 output).
//
//   node patch.mjs <dir> check                 verify every anchor; change nothing
//   node patch.mjs <dir> apply [--json-response]
//
// Each edit has an anchor (`find`, which must match exactly once) and a
// `done` test (true once the edit is in). Edits already in are skipped, so
// the script is idempotent; a missing anchor fails the whole run before any
// file is written, so a generator change is caught instead of mis-patched.
// The tool definitions (names, descriptions, schemas) are never touched.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [dir, mode, ...flags] = process.argv.slice(2);
if (!dir || !['check', 'apply'].includes(mode)) {
  console.error('usage: node patch.mjs <generated-dir> check|apply [--json-response]');
  process.exit(2);
}
const jsonResponse = flags.includes('--json-response');
const here = path.dirname(fileURLToPath(import.meta.url));
const HTTP = 'src/streamable-http.ts';
const INDEX = 'src/index.ts';
const has = (s) => (text) => text.includes(s);

const statelessBlock = `      // patched: stateless - a new Server and transport for every request and no
      // session map, so any instance (any Lambda) can answer any request.
      const server = this.createServer();
      const transport = new StreamableHTTPServerTransport({
        sessionIdGenerator: undefined,${jsonResponse ? '\n        enableJsonResponse: true,' : ''}
      });
      transport.onerror = (err) => {
        console.error('StreamableHTTP transport error:', err);
      };
      res.on('close', () => {
        transport.close();
        server.close();
      });
      await server.connect(transport);
      await transport.handleRequest(req, res, body);
      return toFetchResponse(res);
`;

const patches = [
  {
    summary: 'fixed request-body double read (clone the request before c.req.json())',
    edits: [
      {
        file: HTTP,
        find: '      const body = await c.req.json();\n',
        replace: '      const rawReq = c.req.raw.clone(); // patched: the body is read twice\n      const body = await c.req.json();\n',
        done: has('const rawReq = c.req.raw.clone();'),
      },
      { file: HTTP, find: 'toReqRes(c.req.raw)', replace: 'toReqRes(rawReq)', done: has('toReqRes(rawReq)') },
    ],
  },
  {
    summary: `made streamable HTTP stateless (new Server + transport per request, no session map${jsonResponse ? ', JSON responses' : ''}; GET/DELETE /mcp -> 405)`,
    edits: [
      {
        file: INDEX,
        find: 'const server = new Server(\n    { name: SERVER_NAME, version: SERVER_VERSION },\n    { capabilities: { tools: {} } }\n);\n',
        replace: `// patched: stateless - one Server per request (handlers are defined below)
export function createServer(): Server {
  const server = new Server(
    { name: SERVER_NAME, version: SERVER_VERSION },
    { capabilities: { tools: {} } }
  );
  server.setRequestHandler(ListToolsRequestSchema, listToolsHandler);
  server.setRequestHandler(CallToolRequestSchema, callToolHandler);
  return server;
}
`,
        done: has('export function createServer(): Server {'),
      },
      {
        file: INDEX,
        find: /^server\.setRequestHandler\(ListToolsRequestSchema, (async \(\) => \{\n[\s\S]*?\n\})\);$/m,
        replace: 'const listToolsHandler = $1;',
        done: has('const listToolsHandler = async () => {'),
      },
      {
        file: INDEX,
        find: /^server\.setRequestHandler\(CallToolRequestSchema, (async \(request: CallToolRequest\): Promise<CallToolResult> => \{\n[\s\S]*?\n\})\);$/m,
        replace: 'const callToolHandler = $1;',
        done: has('const callToolHandler = async (request: CallToolRequest)'),
      },
      {
        file: INDEX,
        find: 'setupStreamableHttpServer(server, ',
        replace: 'setupStreamableHttpServer(createServer, ',
        done: has('setupStreamableHttpServer(createServer, '),
      },
      {
        file: HTTP,
        find: '  server: Server;\n  // Store active transports by session ID\n  transports: {[sessionId: string]: StreamableHTTPServerTransport} = {};\n',
        replace: '  createServer: () => Server; // patched: stateless, no session map\n',
        done: has('  createServer: () => Server;'),
      },
      {
        file: HTTP,
        find: '  constructor(server: Server) {\n    this.server = server;\n  }\n',
        replace: '  constructor(createServer: () => Server) {\n    this.createServer = createServer;\n  }\n',
        done: has('    this.createServer = createServer;'),
      },
      {
        file: HTTP,
        find: /^      \/\/ Reuse existing transport if we have a session ID\n[\s\S]*?"Bad Request: invalid session ID or method\."\),\n *400\n *\);\n/m,
        replace: statelessBlock,
        done: has('sessionIdGenerator: undefined,'),
      },
      {
        file: HTTP,
        find: /setupStreamableHttpServer\(server: Server, port = (\d+)\)/,
        replace: 'setupStreamableHttpServer(createServer: () => Server, port = $1)',
        done: has('setupStreamableHttpServer(createServer: () => Server, '),
      },
      {
        file: HTTP,
        find: 'new MCPStreamableHttpServer(server)',
        replace: 'new MCPStreamableHttpServer(createServer)',
        done: has('new MCPStreamableHttpServer(createServer)'),
      },
      {
        file: HTTP,
        find: '  app.post("/mcp", (c) => mcpHandler.handlePostRequest(c));\n',
        replace:
          '  app.post("/mcp", (c) => mcpHandler.handlePostRequest(c));\n' +
          "  app.delete(\"/mcp\", (c) => c.text('Method Not Allowed', 405, { 'Allow': 'POST' })); // patched: no sessions\n",
        done: has('app.delete("/mcp"'),
      },
    ],
  },
  {
    summary: 'PORT read from the environment (default: the generated port)',
    edits: [
      {
        file: INDEX,
        find: /setupStreamableHttpServer\(createServer, (\d+)\)/,
        replace: 'setupStreamableHttpServer(createServer, Number(process.env.PORT || $1))',
        done: has('Number(process.env.PORT || '),
      },
    ],
  },
  {
    summary: 'API auth via machine token (src/machine-auth.ts: Cognito client_credentials, cached; caller token never forwarded)',
    edits: [
      {
        file: INDEX,
        find: 'import { setupStreamableHttpServer } from "./streamable-http.js";\n',
        replace:
          'import { setupStreamableHttpServer } from "./streamable-http.js";\n' +
          'import { getMachineToken } from "./machine-auth.js"; // patched: machine auth\n',
        done: has('import { getMachineToken } from "./machine-auth.js";'),
      },
      {
        file: INDEX,
        find: "return !!process.env[`BEARER_TOKEN_${schemeName.replace(/[^a-zA-Z0-9]/g, '_').toUpperCase()}`];",
        replace: 'return true; // patched: machine-auth.ts supplies the bearer token',
        done: has('return true; // patched: machine-auth.ts supplies the bearer token'),
      },
      {
        file: INDEX,
        find: "const token = process.env[`BEARER_TOKEN_${schemeName.replace(/[^a-zA-Z0-9]/g, '_').toUpperCase()}`];",
        replace: "const token = await getMachineToken(); // patched: the server's own token, never the caller's",
        done: has('const token = await getMachineToken();'),
      },
      { copy: 'machine-auth.ts', to: 'src/machine-auth.ts' },
    ],
  },
];

const files = new Map();
const read = (f) => {
  if (!files.has(f)) {
    const p = path.join(dir, f);
    if (!fs.existsSync(p)) fail(`${f} not found in ${dir} (is this openapi-mcp-generator output?)`);
    files.set(f, fs.readFileSync(p, 'utf8'));
  }
  return files.get(f);
};
const count = (text, find) =>
  typeof find === 'string'
    ? text.split(find).length - 1
    : (text.match(new RegExp(find.source, find.flags.includes('g') ? find.flags : find.flags + 'g')) ?? []).length;
function fail(msg) {
  console.error(`✗ ${msg}`);
  process.exit(1);
}

const errors = [];
const lines = [];
for (const p of patches) {
  let changed = false;
  for (const e of p.edits) {
    if (e.copy) {
      const src = fs.readFileSync(path.join(here, e.copy), 'utf8');
      const dest = path.join(dir, e.to);
      if (!fs.existsSync(dest) || fs.readFileSync(dest, 'utf8') !== src) {
        changed = true;
        if (mode === 'apply') files.set(e.to, src);
      }
      continue;
    }
    const text = read(e.file);
    if (e.done(text)) continue;
    const n = count(text, e.find);
    if (n !== 1) {
      const anchor = typeof e.find === 'string' ? e.find : e.find.source;
      errors.push(`anchor ${n ? `matched ${n} times` : 'not found'} in ${e.file}: ${JSON.stringify(anchor.split('\n')[0])}`);
      continue;
    }
    // split/join for strings: no $-pattern surprises in the replacement
    files.set(e.file, typeof e.find === "string" ? text.split(e.find).join(e.replace) : text.replace(e.find, e.replace));
    changed = true;
  }
  lines.push(changed ? `✓ ${p.summary}` : `✓ already patched: ${p.summary}`);
}
if (errors.length) {
  for (const e of errors) console.error(`✗ ${e}`);
  fail('generated code differs from what this patch expects (openapi-mcp-generator 4.1.0); nothing was changed');
}
if (mode === 'apply') {
  for (const [f, text] of files) {
    const p = path.join(dir, f);
    if (!fs.existsSync(p) || fs.readFileSync(p, 'utf8') !== text) fs.writeFileSync(p, text);
  }
  for (const l of lines) console.log(l);
}
