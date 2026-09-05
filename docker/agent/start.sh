#!/usr/bin/env bash
set -euo pipefail

# Configures the agent against the organisation on start and unregisters it
# again when the container stops.
#
#   AZP_URL         organisation url, e.g. https://dev.azure.com/contoso  (required)
#   AZP_TOKEN       personal access token with "Agent Pools (read, manage)"  (required)
#   AZP_TOKEN_FILE  file to read the token from instead, e.g. a docker secret
#   AZP_POOL        agent pool, default "Default"
#   AZP_AGENT_NAME  agent name, default the container hostname
#   AZP_WORK        work directory, default /azp/_work
#   AZP_AGENT_ONCE  set to run a single job and exit, for ephemeral agents

header() {
    echo
    echo -e "\e[1m>>> $1\e[0m"
    echo
}

if [ -z "${AZP_URL:-}" ]; then
    echo >&2 "error: AZP_URL is not set, e.g. https://dev.azure.com/contoso"
    exit 1
fi

if [ -z "${AZP_TOKEN:-}" ] && [ -n "${AZP_TOKEN_FILE:-}" ]; then
    if [ ! -r "${AZP_TOKEN_FILE}" ]; then
        echo >&2 "error: AZP_TOKEN_FILE ${AZP_TOKEN_FILE} is not readable"
        exit 1
    fi
    AZP_TOKEN="$(cat "${AZP_TOKEN_FILE}")"
fi

if [ -z "${AZP_TOKEN:-}" ]; then
    echo >&2 "error: neither AZP_TOKEN nor AZP_TOKEN_FILE is set"
    exit 1
fi

# Keep the token out of the environment the pipeline jobs inherit.
token="${AZP_TOKEN}"
unset AZP_TOKEN AZP_TOKEN_FILE

cd /azp

cleanup() {
    if [ -e ./config.sh ]; then
        header "Removing the agent"
        ./config.sh remove --unattended --auth PAT --token "${token}" || true
    fi
}

# The agent runs in the background so the shell stays free to receive signals.
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM

header "Configuring the agent"
./config.sh --unattended \
    --acceptTeeEula \
    --url "${AZP_URL}" \
    --auth PAT \
    --token "${token}" \
    --pool "${AZP_POOL:-Default}" \
    --agent "${AZP_AGENT_NAME:-$(hostname)}" \
    --work "${AZP_WORK:-_work}" \
    --replace

header "Running the agent"
if [ -n "${AZP_AGENT_ONCE:-}" ]; then
    set -- --once "$@"
fi

./run.sh "$@" &
agent=$!
wait $agent
