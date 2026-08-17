# Local development image. Expects a prebuilt Linux binary at
# build/bin/kubeloop-control-plane so Minikube does not need registry access.
FROM scratch
COPY build/bin/kubeloop-control-plane /kubeloop-control-plane
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kubeloop-control-plane"]
