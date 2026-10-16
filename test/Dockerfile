# Runs test/e2e.sh in a privileged container:
#   docker build -t neotunnel-e2e -f test/Dockerfile .
#   docker run --rm --privileged neotunnel-e2e
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
      iproute2 nftables iputils-ping netcat-openbsd curl ca-certificates kmod python3 bsdutils \
    && rm -rf /var/lib/apt/lists/*
COPY dist/neotunnel-linux-amd64 /usr/local/bin/neotunnel
COPY test/e2e.sh /e2e.sh
CMD ["bash", "/e2e.sh", "/usr/local/bin/neotunnel"]
