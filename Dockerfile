# Runtime image for TokenLive all-in-one (Linux).
#
# Build context must contain a prebuilt stage/ directory (linux-amd64 or linux-arm64):
#   TARGET_GOOS=linux TARGET_GOARCH=amd64 \
#     DEFAULT_CONF=/etc/tokenlive/config.yml \
#     DEFAULT_DATA=/var/lib/tokenlive \
#     DEFAULT_ADMIN_DIR=/usr/share/tokenlive/admin \
#     DEFAULT_WEB_DIR=/usr/share/tokenlive/web \
#     CONFIG_FILE=config/linux.yml \
#     OUT_DIR=stage ./scripts/package-release.sh
#   docker build -t tokenlive .
#
# The binary has FHS paths baked in, so no CLI args are needed at runtime.
# Override config by mounting a volume at /etc/tokenlive.

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY stage/bin/tokenlive /usr/local/bin/tokenlive
COPY stage/share/tokenlive/admin /usr/share/tokenlive/admin
COPY stage/share/tokenlive/web /usr/share/tokenlive/web
COPY stage/etc/tokenlive/config.yml /etc/tokenlive/config.yml
COPY stage/etc/tokenlive/config.example.yml /etc/tokenlive/config.example.yml

RUN mkdir -p /var/lib/tokenlive/logs

VOLUME ["/var/lib/tokenlive", "/etc/tokenlive"]

EXPOSE 2525

WORKDIR /var/lib/tokenlive
ENTRYPOINT ["tokenlive"]
