FROM golang:1.27-alpine AS build
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
RUN apk add --no-cache ca-certificates
COPY cmd/kubeloop-control-plane ./cmd/kubeloop-control-plane
COPY api ./api
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X github.com/fengqi-dev/kube-loop/internal/buildinfo.version=${VERSION} -X github.com/fengqi-dev/kube-loop/internal/buildinfo.commit=${COMMIT}" \
  -o /out/kubeloop-control-plane ./cmd/kubeloop-control-plane

FROM scratch
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="KubeLoop Control Plane" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/kubeloop-control-plane /kubeloop-control-plane
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kubeloop-control-plane"]
