variable "REGISTRY" {
    default = "docker.io"
}

variable "TAG" {
    default = "latest"
}

target "default" {
    context    = "."
    dockerfile = "Dockerfile"
    platforms   = [
        "linux/arm64",
        "linux/amd64",
    ]
    tags       = [
        "${REGISTRY}/k8s-image-pull-metrics:${TAG}",
    ]
    cache-to   = ["type=local,dest=/tmp/.buildx-cache"]
    cache-from = ["type=local,src=/tmp/.buildx-cache"]
}
