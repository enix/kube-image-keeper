# Build the manager binary
FROM --platform=${BUILDPLATFORM} golang:1.22-alpine3.19 AS builder

WORKDIR /workspace

RUN go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/

# Copy the makefile
COPY Makefile Makefile

ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}

ARG VERSION
ARG REVISION
ENV LD_FLAGS="\
    -X 'github.com/adisplayname/kube-image-keeper/internal/metrics.Version=${VERSION}' \
    -X 'github.com/adisplayname/kube-image-keeper/internal/metrics.Revision=${REVISION}' \
    -X 'github.com/adisplayname/kube-image-keeper/internal/metrics.BuildDateTime=BUILD_DATE_TIME'"

RUN --mount=type=cache,target="/root/.cache/go-build" \
    BUILD_DATE_TIME=$(date -u +"%Y-%m-%dT%H:%M:%S") && \
    LD_FLAGS=$(/bin/ash -c "set -o pipefail && echo $LD_FLAGS | sed -e \"s/BUILD_DATE_TIME/$BUILD_DATE_TIME/g\"") && \
    controller-gen object paths="./..." && \
    go build -ldflags="$LD_FLAGS" -o manager cmd/cache/main.go && \
    go build -ldflags="$LD_FLAGS" -o registry-proxy cmd/proxy/main.go

FROM alpine:3.19 AS alpine

COPY --from=builder /workspace/manager /usr/local/bin/
COPY --from=builder /workspace/registry-proxy /usr/local/bin/
USER 65532:65532

ENTRYPOINT ["manager"]

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager /usr/local/bin/
COPY --from=builder /workspace/registry-proxy /usr/local/bin/
USER 65532:65532

ENTRYPOINT ["manager"]
