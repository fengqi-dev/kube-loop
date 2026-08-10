FROM golang:1.26-alpine AS build
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kubeloop-controller ./cmd/kubeloop-controller
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
  -o /out/kubeloop-controller ./cmd/kubeloop-controller

FROM scratch
ARG VERSION=dev
LABEL org.opencontainers.image.title="KubeLoop Controller" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/kubeloop-controller /kubeloop-controller
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kubeloop-controller"]
