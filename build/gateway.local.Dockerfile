FROM alpine:3.22 AS perms
COPY build/bin/kube-loop-gateway-linux-arm64 /kube-loop-gateway
RUN chmod 755 /kube-loop-gateway

FROM scratch
COPY --from=perms /kube-loop-gateway /kube-loop-gateway
USER 65532:65532
EXPOSE 1080 8080
ENTRYPOINT ["/kube-loop-gateway"]
