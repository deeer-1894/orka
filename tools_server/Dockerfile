# Containerized tools gateway. This is the real isolation boundary for the shell
# tool: commands run inside this container's filesystem + the mounted workspace,
# never on the host. Build context is the repo ROOT (tools_server imports
# orka_core via a replace directive), e.g.:
#   docker build -f tools_server/Dockerfile -t orka-tools .

# ---- builder ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
# Only the two modules needed to build the gateway. tools_server/go.mod has
# `replace github.com/orka-oss/orka_core => ../orka_core`, so the relative layout
# must be preserved and go.work must be OFF (we build the single module).
COPY orka_core/ ./orka_core/
COPY tools_server/ ./tools_server/
WORKDIR /src/tools_server
# The module proxy is a build ARG because the default is unreachable from some
# networks (mainland China among them), where the build fails on a dial timeout
# with no hint that the proxy is the problem. Default stays upstream; override
# with --build-arg GOPROXY=... or GOPROXY in .env (compose forwards it).
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOWORK=off CGO_ENABLED=0 GOFLAGS=-trimpath GOPROXY=$GOPROXY
RUN go build -o /out/orka_tools .

# ---- runtime ----
# python:slim gives the agent's shell a usable "computer": python3 + pip out of
# the box, plus git/curl/jq below. Debian-based so apt is available.
FROM python:3.12-slim-bookworm
# git/curl/jq for the shell + engineer; pandoc/wkhtmltopdf back doc_export/doc_read
# (md ↔ html/docx/pdf); poppler-utils backs pdf_extract; fonts-noto-cjk so
# charts/exports render CJK instead of tofu. Python libs power the data + code
# tools: matplotlib (chart), pandas/openpyxl (csv/xlsx/sql/join), python-pptx
# (slides), numpy (the python runner's analysis staples).
# Same story as GOPROXY above: files.pythonhosted.org read-times-out from some
# networks and the build dies here, several minutes in, on an error that names
# the symptom and not the cause. Default stays upstream; override with
# PIP_INDEX_URL in .env (compose forwards it). The retries/timeout are for a
# merely slow link rather than a blocked one.
ARG PIP_INDEX_URL=https://pypi.org/simple
RUN apt-get update && apt-get install -y --no-install-recommends \
        git curl ca-certificates jq pandoc wkhtmltopdf poppler-utils fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir --index-url "$PIP_INDEX_URL" --retries 5 --timeout 60 \
        matplotlib pandas numpy openpyxl python-pptx
# Run as a non-root user; the container is the sandbox, not the in-container uid.
RUN useradd -m -u 10001 orka && mkdir -p /workspace && chown orka:orka /workspace
COPY --from=build /out/orka_tools /usr/local/bin/orka_tools
USER orka
ENV TOOLS_ADDR=:8090 \
    BASE_STORAGE_PATH=/workspace \
    SHELL_TOOL=1
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/orka_tools"]
