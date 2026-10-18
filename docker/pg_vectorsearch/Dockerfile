FROM postgres:18-trixie AS build

ARG PGVECTOR_VERSION=0.8.2
ARG PG_VECTORSEARCH_REF=4899ffb57e7752a766b714621844d0abb4e6cad1

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates curl libopenblas-dev meson ninja-build \
    pkg-config postgresql-server-dev-18 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
RUN curl -fsSL "https://github.com/pgvector/pgvector/archive/refs/tags/v${PGVECTOR_VERSION}.tar.gz" \
    | tar xz --strip-components=1 \
    && make OPTFLAGS="" \
    && make install DESTDIR=/install

WORKDIR /build/pg_vectorsearch
RUN curl -fsSL "https://github.com/timescale/pg_vectorsearch/archive/${PG_VECTORSEARCH_REF}.tar.gz" \
    | tar xz --strip-components=1 \
    && meson setup builddir --buildtype=release -Dpostgresql=enabled -Dtools=disabled -Dblas=enabled \
    && DESTDIR=/install meson install -C builddir

FROM postgres:18-trixie
RUN apt-get update && apt-get install -y --no-install-recommends libopenblas0-pthread \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /install/usr/lib/postgresql/18/lib/ /usr/lib/postgresql/18/lib/
COPY --from=build /install/usr/share/postgresql/18/extension/ /usr/share/postgresql/18/extension/
