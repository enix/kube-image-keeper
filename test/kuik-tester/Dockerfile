# The Go release is pinned to a patch version on purpose: the sleep binaries
# below are a layer of every synthetic image, so changing the Go release changes
# every synthetic digest the tester serves.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
# The sleep binaries of the synthetic images, as `task tester:bins` builds them (the
# image has no task). The synthetic images exist for linux/amd64 and linux/arm64
# only (internal/synth), so the image is built for those two platforms, not for
# every platform kuik supports.
RUN --mount=type=cache,target=/root/.cache/go-build for a in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$a go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" \
        -o internal/synth/bin/sleep-$a ./cmd/sleep; \
    done
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags sleepbins -trimpath -ldflags="-s -w" -o /out/kuik-tester ./cmd/kuik-tester

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/kuik-tester /kuik-tester
USER 65532:65532
EXPOSE 8080 8081 5000
ENTRYPOINT ["/kuik-tester","-config","/etc/kuik-tester/config.yaml"]
