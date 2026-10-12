BIN_DIR:=bin
BINARY_PATH:=${BIN_DIR}/caretta
DOCKER_BIN:=docker
REPODIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
BUILD_SCRIPTS_DIRECTORY=scripts/build
BPF_CLANG := clang
BPF_STRIP := llvm-strip
INCLUDE_C_FLAGS := -I/tmp/caretta_extra/libbpf_headers
BPF_CFLAGS := -O2 -g -Wall -Werror -fdebug-prefix-map=/ebpf=. ${INCLUDE_C_FLAGS}
BUILDER_IMAGE := caretta-ebpf-builder

ARCH ?= $(shell go env GOARCH)

.PHONY: build
build: ${BIN_DIR} pkg/tracing/bpf_x86_bpfel.go pkg/tracing/bpf_arm64_bpfel.go
	GOOS=linux GOARCH=${ARCH} CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ${BINARY_PATH} ./cmd/caretta

${BIN_DIR}:
	mkdir -p ${BIN_DIR}

.PHONY: download_libbpf_headers
download_libbpf_headers: 
	${REPODIR}/${BUILD_SCRIPTS_DIRECTORY}/download_libbpf_headers.sh

.PHONY: generate_ebpf
generate_ebpf: download_libbpf_headers
	cd ${REPODIR}/pkg/tracing && \
		go tool bpf2go -go-package tracing \
		-cc "${BPF_CLANG}" -strip "${BPF_STRIP}" -cflags "${BPF_CFLAGS}" \
		-target amd64,arm64 bpf ebpf/caretta.bpf.c

.PHONY: generate_ebpf_in_docker
generate_ebpf_in_docker:
	${DOCKER_BIN} build --target ebpf-builder -t ${BUILDER_IMAGE} ${REPODIR}
	${DOCKER_BIN} run --rm \
		--user $(shell id -u):$(shell id -g) \
		--env HOME=/tmp \
		-v ${REPODIR}:/src \
		-w /src \
		${BUILDER_IMAGE} \
		${MAKE} generate_ebpf

pkg/tracing/bpf_%_bpfel.go: pkg/tracing/ebpf/caretta.bpf.c
	$(MAKE) generate_ebpf