FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kubeloop-gateway ./cmd/kubeloop-gateway
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o /out/kubeloop-gateway ./cmd/kubeloop-gateway

FROM scratch
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="KubeLoop Gateway" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/kubeloop-gateway /kubeloop-gateway
USER 65532:65532
EXPOSE 1080 8080
ENTRYPOINT ["/kubeloop-gateway"]
