# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.2-trixie AS ebpf-builder
RUN apt-get update \
 && apt-get install -y --no-install-recommends clang llvm \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src

FROM ebpf-builder AS builder
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETARCH
# bpf2go emits objects for every target arch, so this cross-compiles natively instead of under QEMU
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    make generate_ebpf build ARCH=${TARGETARCH}

# gcr.io/distroless/static-debian13 (root variant: attaching kprobes requires uid 0)
FROM gcr.io/distroless/static-debian13@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0
COPY --from=builder /src/bin/caretta /caretta
ENTRYPOINT ["/caretta"]