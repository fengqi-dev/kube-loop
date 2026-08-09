FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kubeloop-gateway ./cmd/kubeloop-gateway
COPY internal/gateway ./internal/gateway
COPY internal/tunnel ./internal/tunnel
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o /out/kube-loop-gateway ./cmd/kubeloop-gateway

FROM scratch
ARG VERSION=dev
LABEL org.opencontainers.image.title="KubeLoop Gateway" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/kube-loop-gateway /kube-loop-gateway
USER 65532:65532
EXPOSE 1080 8080
ENTRYPOINT ["/kube-loop-gateway"]
