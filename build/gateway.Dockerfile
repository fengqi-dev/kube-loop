FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kubeloop-gateway ./cmd/kubeloop-gateway
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X github.com/fengqi-dev/kube-loop/internal/buildinfo.version=${VERSION}" \
  -o /out/kubeloop-gateway ./cmd/kubeloop-gateway

FROM golang:1.27-alpine AS singbox
RUN apk add --no-cache git patch
WORKDIR /src
RUN git clone --filter=blob:none --branch v1.14.0 https://github.com/SagerNet/sing-box.git . && \
    test "$(git rev-parse HEAD)" = "0b8995879f29a9b98ee027bc17b75e101445b238"
COPY third_party/patches/sing-box /patches
RUN for patch_file in /patches/*.patch; do patch -p1 < "${patch_file}"; done
RUN tags="$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" && \
    flags="$(cat release/LDFLAGS)" && \
    CGO_ENABLED=0 go build -buildvcs=false -trimpath -tags "${tags}" \
      -ldflags="-s -w -X github.com/sagernet/sing-box/constant.Version=1.14.0 ${flags}" \
      -o /out/sing-box ./cmd/sing-box

FROM scratch
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="KubeLoop Gateway" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/kubeloop-gateway /kubeloop-gateway
COPY --from=singbox /out/sing-box /sing-box
COPY --from=singbox /src/LICENSE /LICENSE.sing-box.txt
COPY --chown=65532:65532 build/runtime /tmp
USER 65532:65532
EXPOSE 1080 8080
ENTRYPOINT ["/kubeloop-gateway"]
