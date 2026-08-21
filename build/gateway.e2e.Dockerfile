# Local/CI e2e image. Expects a prebuilt Linux binary at build/bin/kubeloop-gateway.
# Host must chmod 755 the binary before build so no base image pull is required
# (important when minikube cannot reach Docker Hub).
FROM scratch
COPY build/bin/kubeloop-gateway /kubeloop-gateway
USER 65532:65532
EXPOSE 1080 8080
ENTRYPOINT ["/kubeloop-gateway"]
