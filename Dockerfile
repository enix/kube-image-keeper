# Build the manager binary
FROM --platform=${BUILDPLATFORM} golang:1.25-alpine3.22 AS builder

WORKDIR /workspace

RUN go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2

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
  -X 'github.com/enix/kube-image-keeper/internal/info.Version=${VERSION}' \
  -X 'github.com/enix/kube-image-keeper/internal/info.Revision=${REVISION}' \
  -X 'github.com/enix/kube-image-keeper/internal/info.BuildDateTime=BUILD_DATE_TIME'"

RUN --mount=type=cache,target="/root/.cache/go-build" \
  BUILD_DATE_TIME=$(date -u +"%Y-%m-%dT%H:%M:%S") && \
  LD_FLAGS=$(/bin/ash -c "set -o pipefail && echo $LD_FLAGS | sed -e \"s/BUILD_DATE_TIME/$BUILD_DATE_TIME/g\"") && \
  controller-gen object paths="./..." && \
  go build -ldflags="$LD_FLAGS" -o manager cmd/main.go

# For development/debug purposes, we can run the manager in an Alpine container in order to have access to a shell and other tools
FROM alpine:3.22 AS alpine

COPY --from=builder /workspace/manager /usr/local/bin/
USER 65532:65532

ENTRYPOINT ["manager"]

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager /usr/local/bin/
USER 65532:65532

ENTRYPOINT ["manager"]

