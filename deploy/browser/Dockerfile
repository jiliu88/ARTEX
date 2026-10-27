FROM mcr.microsoft.com/playwright/mcp@sha256:690763cbb437ee7233f538a953bb5390bf03791eb5c965b471e59d17a6132fcb

USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    xvfb x11vnc novnc websockify fluxbox busybox xauth \
    && rm -rf /var/lib/apt/lists/*
COPY start.sh /opt/browser/start.sh
COPY demo /opt/browser/demo
RUN chmod 755 /opt/browser/start.sh && mkdir -p /data/profile /data/artifacts \
    && install -d -m 1777 /tmp/.X11-unix \
    && chown -R node:node /data
USER node
ENV DISPLAY=:99
WORKDIR /data
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/opt/browser/start.sh"]
