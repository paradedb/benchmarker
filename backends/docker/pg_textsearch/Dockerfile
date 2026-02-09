FROM postgres:18

# Install dependencies for downloading and extracting
RUN apt-get update && apt-get install -y \
    curl \
    ca-certificates \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# Download and install prebuilt pg_textsearch extension
# The zip contains a .deb package that we need to install
# Check https://github.com/timescale/pg_textsearch/releases for latest version
ARG PG_TEXTSEARCH_VERSION=0.4.2
ARG TARGETARCH
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64" || echo "amd64") && \
    curl -L -o /tmp/pg_textsearch.zip \
    "https://github.com/timescale/pg_textsearch/releases/download/v${PG_TEXTSEARCH_VERSION}/pg-textsearch-v${PG_TEXTSEARCH_VERSION}-pg17-${ARCH}.zip" && \
    unzip /tmp/pg_textsearch.zip -d /tmp/pg_textsearch && \
    dpkg -i /tmp/pg_textsearch/*.deb && \
    rm -rf /tmp/pg_textsearch.zip /tmp/pg_textsearch
