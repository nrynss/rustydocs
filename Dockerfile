# GoReleaser supplies linux/<arch>/rustydocs in its temporary build context.
FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

RUN apk add --no-cache git ca-certificates \
    && git config --system --add safe.directory /src

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/rustydocs /usr/local/bin/rustydocs
COPY LICENSE /usr/share/licenses/rustydocs/LICENSE

WORKDIR /src
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/rustydocs"]
