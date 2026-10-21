# servers/generated: Server A (generated)

`app/` is not in git. It is generated on camera from `openapi.yaml`:

```
scripts/generate-server.sh --patch          # generate into app/, then patch it
scripts/deploy-mcp.sh servers/generated     # build + deploy behind mcp-host
```

`--force` replaces an existing `app/`. Without `--patch`, run
`scripts/patch-generated-server.sh servers/generated/app` yourself; the build
refuses an unpatched `app/`.

What the patch changes (the tool list stays exactly as generated):

- request-body double read (the server crashes on the first request);
- stateless streamable HTTP: a new Server and transport per request, no
  session map, JSON responses; GET/DELETE `/mcp` return 405;
- `PORT` from the environment (Lambda uses 8080);
- API auth: `src/machine-auth.ts` gets a Cognito client_credentials token for
  the machine client (secret from SSM `/audiofile/machine-client-secret`) and
  caches it. The caller's token is checked by the mcp-host gateway and never
  forwarded.

Run it locally after patching:

```
cd servers/generated/app && npm run build
PORT=3000 API_BASE_URL=... MACHINE_CLIENT_ID=... TOKEN_URL=<CognitoDomain>/oauth2/token \
  MACHINE_CLIENT_SECRET=... npm run start:http
```

(Without `MACHINE_CLIENT_SECRET` the server reads the secret from SSM with
the default AWS credentials.)

Rehearsal stack without editing `samconfig.toml`:

```
STACK_NAME=mcp-generated-rehearsal SERVER_NAME=audiofile-generated-rehearsal \
  scripts/deploy-mcp.sh servers/generated
```

Offline test of the whole generate, patch, run path: `scripts/test/generated_server_test.sh`.
