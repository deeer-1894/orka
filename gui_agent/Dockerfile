# Sandboxed GUI executor: gui_agent + headful Chromium on a virtual X display,
# streamed live via x11vnc + noVNC. Playwright OWNS the on-screen browser, so the
# noVNC view shows exactly what the agent is doing (perfect live sync).
FROM python:3.12-slim

ENV DEBIAN_FRONTEND=noninteractive \
    PIP_NO_CACHE_DIR=1 \
    DISPLAY=:99 \
    HEADLESS=0 \
    GUI_PORT=8100

# Resilient apt: the Debian CDN intermittently 502s — retry + fix-missing.
RUN printf 'Acquire::Retries "8";\nAcquire::http::Timeout "30";\n' > /etc/apt/apt.conf.d/80retries \
 && for i in 1 2 3 4 5; do \
      apt-get update && apt-get install -y --no-install-recommends --fix-missing \
        xvfb x11vnc fluxbox novnc websockify \
        fonts-noto-cjk fonts-liberation ca-certificates curl procps \
      && break || { echo "apt attempt $i failed; retrying in 5s"; sleep 5; }; \
    done \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt \
 && python -m playwright install --with-deps chromium

COPY . .

EXPOSE 8100 6080
CMD ["bash", "docker/start.sh"]
