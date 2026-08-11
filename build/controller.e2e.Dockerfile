# Local development image. Expects a prebuilt Linux binary at
# build/bin/kubeloop-controller so Minikube does not need registry access.
FROM scratch
COPY build/bin/kubeloop-controller /kubeloop-controller
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kubeloop-controller"]
