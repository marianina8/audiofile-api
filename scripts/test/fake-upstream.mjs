// Fake Cognito token endpoint + fake audiofile API for
// scripts/test/generated_server_test.sh. Listens on a random port, prints
// the port on stdout, and appends one JSON line per request to $FAKE_LOG:
// {kind: "token"|"api", method, path, authorization, ok}.
//
//   POST /oauth2/token  client_credentials; checks Basic auth, grant and scope
//   GET  /tracks        one fake track (needs the fake machine token)
//   GET  /health        {"status":"ok"}
import http from 'node:http';
import fs from 'node:fs';

const CLIENT_ID = process.env.FAKE_CLIENT_ID;
const CLIENT_SECRET = process.env.FAKE_CLIENT_SECRET;
const TOKEN = process.env.FAKE_MACHINE_TOKEN;
const LOG = process.env.FAKE_LOG;

const log = (entry) => fs.appendFileSync(LOG, JSON.stringify(entry) + '\n');
const json = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json' });
  res.end(JSON.stringify(body));
};

http
  .createServer((req, res) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      const url = new URL(req.url, 'http://x');
      const authorization = req.headers.authorization ?? null;
      if (req.method === 'POST' && url.pathname === '/oauth2/token') {
        const form = new URLSearchParams(raw);
        const basic = 'Basic ' + Buffer.from(`${CLIENT_ID}:${CLIENT_SECRET}`).toString('base64');
        const ok =
          authorization === basic &&
          form.get('grant_type') === 'client_credentials' &&
          form.get('scope') === 'audiofile/read audiofile/write';
        log({ kind: 'token', method: req.method, path: url.pathname, ok });
        if (!ok) return json(res, 400, { error: 'invalid_client' });
        return json(res, 200, { access_token: TOKEN, expires_in: 3600, token_type: 'Bearer' });
      }
      const ok = authorization === `Bearer ${TOKEN}`;
      log({ kind: 'api', method: req.method, path: url.pathname, authorization, ok });
      if (url.pathname === '/health') return json(res, 200, { status: 'ok' });
      if (!ok) return json(res, 401, { error: { code: 'unauthorized', message: 'bad token' } });
      if (req.method === 'GET' && url.pathname === '/tracks') {
        return json(res, 200, { items: [{ id: 'trk_fake1', title: 'Fake Upbeat Song', bpm: 128 }] });
      }
      return json(res, 404, { error: { code: 'not_found', message: 'no route' } });
    });
  })
  .listen(0, '127.0.0.1', function () {
    console.log(this.address().port);
  });
