FROM golang:1.26-alpine AS build
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kubeloop-control-plane ./cmd/kubeloop-control-plane
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
  -o /out/kubeloop-control-plane ./cmd/kubeloop-control-plane

FROM scratch
ARG VERSION=dev
LABEL org.opencontainers.image.title="KubeLoop Control Plane" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/kubeloop-control-plane /kubeloop-control-plane
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kubeloop-control-plane"]
