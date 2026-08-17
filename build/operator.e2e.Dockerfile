# Local development image. Expects a prebuilt Linux binary at
# build/bin/kubeloop-operator so local clusters do not need registry access.
FROM scratch
COPY build/bin/kubeloop-operator /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
