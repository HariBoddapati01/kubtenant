# Build the manager binary.
# --platform=$BUILDPLATFORM: the builder always runs natively on the build
# host and cross-compiles via GOARCH, so multi-arch builds don't run the Go
# toolchain under QEMU emulation (slow enough to time out release jobs).
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Cache dependencies before copying source so layers stay warm.
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

# Build a static, stripped binary.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -a -o manager cmd/main.go

# Use distroless as the minimal, non-root runtime base.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
