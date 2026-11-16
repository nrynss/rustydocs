# GoReleaser supplies linux/<arch>/rustydocs in its temporary build context.
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

RUN apk add --no-cache git ca-certificates \
    && git config --system --add safe.directory /src

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/rustydocs /usr/local/bin/rustydocs
COPY LICENSE /usr/share/licenses/rustydocs/LICENSE

WORKDIR /src
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/rustydocs"]
