# GoReleaser supplies linux/<arch>/rustydocs in its temporary build context.
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk add --no-cache git ca-certificates \
    && git config --system --add safe.directory /src

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/rustydocs /usr/local/bin/rustydocs
COPY LICENSE /usr/share/licenses/rustydocs/LICENSE

WORKDIR /src
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/rustydocs"]
