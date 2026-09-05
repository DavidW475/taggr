# syntax=docker/dockerfile:1

# Self-hosted Azure Pipelines agent.
#
#   docker build -t taggr-agent docker/agent
#   docker run -d --name taggr-agent \
#     -e AZP_URL=https://dev.azure.com/contoso \
#     -e AZP_TOKEN=<pat> \
#     -e AZP_POOL=Default \
#     taggr-agent

FROM ubuntu:26.04

# Agent version to bake into the image. Empty resolves the latest release at
# build time; pin it (e.g. 4.255.0) for reproducible builds.
ARG AGENT_VERSION=""
ARG TARGETARCH=amd64

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        jq \
        openssh-client \
        unzip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /azp

# Download the agent and let it install its own runtime dependencies, so the
# package names stay correct across base image updates.
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) agent_arch=x64 ;; \
        arm64) agent_arch=arm64 ;; \
        *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    version="${AGENT_VERSION}"; \
    if [ -z "${version}" ]; then \
        version="$(curl -fsSL https://api.github.com/repos/microsoft/azure-pipelines-agent/releases/latest \
            | jq -r .tag_name | sed 's/^v//')"; \
    fi; \
    echo "installing azure pipelines agent ${version} (linux-${agent_arch})"; \
    curl -fsSL -o /tmp/agent.tar.gz \
        "https://download.agent.dev.azure.com/agent/${version}/vsts-agent-linux-${agent_arch}-${version}.tar.gz"; \
    tar -xzf /tmp/agent.tar.gz -C /azp; \
    rm /tmp/agent.tar.gz; \
    ./bin/installdependencies.sh; \
    rm -rf /var/lib/apt/lists/*

COPY --chmod=0755 start.sh /azp/start.sh

# The agent refuses to run as root, so give it an unprivileged home.
RUN useradd --create-home --shell /bin/bash agent \
    && mkdir -p /azp/_work \
    && chown -R agent:agent /azp /home/agent

USER agent
ENV AZP_WORK=/azp/_work

ENTRYPOINT ["/azp/start.sh"]
