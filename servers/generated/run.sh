#!/bin/sh
# Lambda handler for the Lambda Web Adapter: start the generated server
# (its start:http command). It listens on $PORT (8080, = AWS_LWA_PORT).
cd "$(dirname "$0")" || exit 1
exec node build/index.js --transport=streamable-http
