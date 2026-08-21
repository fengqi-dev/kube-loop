FROM alpine:3.22 AS perms
COPY build/bin/kubeloop-gateway-linux-arm64 /kubeloop-gateway
RUN chmod 755 /kubeloop-gateway

FROM scratch
COPY --from=perms /kubeloop-gateway /kubeloop-gateway
USER 65532:65532
EXPOSE 1080 8080
ENTRYPOINT ["/kubeloop-gateway"]
