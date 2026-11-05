#!/usr/bin/env bash
# Run only against an independently created local compose project.
# Required variable and exact project guard prevent accidentally pausing the
# user's main convo stack. Ctrl-C always resumes the broker.
set -Eeuo pipefail
cd "$(dirname "$0")/../.."
: "${CONVO_TEST_PROJECT:?Set CONVO_TEST_PROJECT=convo-phase1-accept for the isolated test stack}"
if [[ "$CONVO_TEST_PROJECT" != 'convo-phase1-accept' || "${CONVO_TEST_ALLOW_FAULT:-}" != 'YES' ]]; then
  echo 'Only isolated project convo-phase1-accept with CONVO_TEST_ALLOW_FAULT=YES is allowed' >&2
  exit 2
fi
command -v docker >/dev/null || { echo 'docker compose required' >&2; exit 2; }
compose=(docker compose -p "$CONVO_TEST_PROJECT" -f docker-compose.yaml -f monitoring/compose.yaml)
broker_id="$("${compose[@]}" ps -q rabbitmq)"
if [[ -z "$broker_id" ]]; then echo 'Isolated broker not running' >&2; exit 2; fi
actual_project="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$broker_id")"
if [[ "$actual_project" != "$CONVO_TEST_PROJECT" ]]; then echo 'Project label mismatch; refusing fault' >&2; exit 2; fi
resume() { "${compose[@]}" unpause rabbitmq >/dev/null 2>&1 || true; }
trap resume EXIT INT TERM
"${compose[@]}" pause rabbitmq
echo 'Isolated RabbitMQ paused. Run scripts/phase1/vote_load.py in another terminal.'
echo 'Outbox should grow, then relay and DB converge after resume.'
read -r -p 'Press ENTER to resume RabbitMQ (also auto-resumed on exit): ' _ || true
resume
trap - EXIT INT TERM
