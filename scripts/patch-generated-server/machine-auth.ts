/**
 * Machine auth for the audiofile API (added by scripts/patch-generated-server.sh).
 *
 * The MCP server calls the API as itself: a Cognito client_credentials token
 * for the machine client. The caller's own token is checked at the gateway in
 * front of this server and is never forwarded. Tokens and the secret are
 * never logged.
 *
 *   MACHINE_CLIENT_ID      machine app client id
 *   TOKEN_URL              <CognitoDomain>/oauth2/token
 *   MACHINE_CLIENT_SECRET  local runs only; otherwise read once from SSM
 */
import { SSMClient, GetParameterCommand } from '@aws-sdk/client-ssm';

const SECRET_PARAMETER = '/audiofile/machine-client-secret';
const SCOPES = 'audiofile/read audiofile/write';
const REFRESH_BEFORE_EXPIRY_MS = 5 * 60 * 1000;

let secret: Promise<string> | undefined;
let cached: { token: string; expiresAt: number } | undefined;
let pending: Promise<string> | undefined;

/** A valid machine access token, cached until ~5 minutes before it expires. */
export async function getMachineToken(): Promise<string> {
  if (cached && Date.now() < cached.expiresAt - REFRESH_BEFORE_EXPIRY_MS) return cached.token;
  pending ??= requestToken().finally(() => (pending = undefined));
  return pending;
}

async function requestToken(): Promise<string> {
  const basic = Buffer.from(`${env('MACHINE_CLIENT_ID')}:${await clientSecret()}`).toString('base64');
  const res = await fetch(env('TOKEN_URL'), {
    method: 'POST',
    headers: { authorization: `Basic ${basic}`, 'content-type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ grant_type: 'client_credentials', scope: SCOPES }),
  });
  if (!res.ok) throw new Error(`machine token request failed: HTTP ${res.status}`);
  const body = (await res.json()) as { access_token?: string; expires_in?: number };
  if (!body.access_token) throw new Error('machine token response has no access_token');
  cached = { token: body.access_token, expiresAt: Date.now() + (body.expires_in ?? 3600) * 1000 };
  return cached.token;
}

function clientSecret(): Promise<string> {
  if (process.env.MACHINE_CLIENT_SECRET) return Promise.resolve(process.env.MACHINE_CLIENT_SECRET);
  secret ??= new SSMClient({})
    .send(new GetParameterCommand({ Name: SECRET_PARAMETER, WithDecryption: true }))
    .then((out) => {
      if (!out.Parameter?.Value) throw new Error(`SSM parameter ${SECRET_PARAMETER} is empty`);
      return out.Parameter.Value;
    })
    .catch((err) => {
      secret = undefined; // retry on the next call
      // err.name only: AWS error messages can carry the role ARN (account id).
      throw new Error(`could not read SSM parameter ${SECRET_PARAMETER} (${err?.name ?? 'Error'})`);
    });
  return secret;
}

function env(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is not set`);
  return value;
}
