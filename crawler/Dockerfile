FROM python:3.13-slim

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    DISPLAY=:99

# novnc's package layout (e.g. where its static web assets land, e.g.
# /usr/share/novnc) can vary across Debian releases — if the entrypoint's
# websockify --web path doesn't exist on the base image in use, check
# `dpkg -L novnc` inside the built image and adjust docker/entrypoint.sh.
RUN apt-get update && apt-get install -y --no-install-recommends \
      xvfb x11vnc novnc websockify \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY pyproject.toml ./
COPY src ./src
# playwright is already a declared dependency (pyproject.toml), so `pip
# install -e .` pulls in the Python package; this only needs to additionally
# download the browser binary + its OS-level deps. scripts/ is copied AFTER
# this (not alongside src/) so editing scripts/login.py or run_worker.py
# doesn't bust this layer's cache and force a full chromium re-download.
RUN pip install -e . \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
COPY scripts ./scripts

COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 6080
VOLUME ["/data/profile", "/data/debug"]
ENTRYPOINT ["/entrypoint.sh"]
