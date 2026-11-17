FROM ubuntu:22.04 AS download
ARG OPEND_VERSION=10.12.7208
ARG OPEND_SHA256=fe4be7fc52529dfb5e868d13079287c159e448cb5c3f567ae6767fd2bb030f18
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL --retry 3 "https://softwaredownload.futunn.com/Futu_OpenD_${OPEND_VERSION}_Ubuntu18.04.tar.gz" -o /opend.tgz \
    && echo "${OPEND_SHA256}  /opend.tgz" | sha256sum -c - \
    && mkdir /opend \
    && tar -xzf /opend.tgz -C /opend --strip-components=2 \
       "Futu_OpenD_${OPEND_VERSION}_Ubuntu18.04/Futu_OpenD_${OPEND_VERSION}_Ubuntu18.04" \
    && rm /opend.tgz

FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libstdc++6 libgcc-s1 zlib1g \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 1000 --create-home app \
    && mkdir -p /home/app/.config /home/app/.local /home/app/.com.futu \
    && chown -R app:app /home/app
COPY --from=download /opend /opt/futu
COPY FutuOpenD.xml /opt/futu/FutuOpenD.xml
WORKDIR /opt/futu
ENV HOME=/home/app LD_LIBRARY_PATH=/opt/futu
USER app
EXPOSE 11111
# Initial interactive login uses this same entrypoint without a command.
ENTRYPOINT ["./FutuOpenD", "-cfg_file=/opt/futu/FutuOpenD.xml", "-no_monitor=1"]
