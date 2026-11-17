#!/bin/bash
# Source after entering the repository. Credentials remain in Compose env files.
COMPOSE=(docker compose)
OPTIONS_ENV_FILE="${OPTIONS_ENV_FILE:-/opt/weibospider-private/options/compose.env}"
if [ ! -s "$OPTIONS_ENV_FILE" ] && [ -s .env.options ]; then
    OPTIONS_ENV_FILE=.env.options
fi
if [ -s "$OPTIONS_ENV_FILE" ]; then
    COMPOSE+=(--env-file "$OPTIONS_ENV_FILE" -f docker-compose.yml -f docker-compose.options.yml)
fi
