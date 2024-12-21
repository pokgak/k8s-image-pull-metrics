# README

## Building

```
$ export REGISTRY="<registry url>"
$ export TAG=$(git rev-parse --short HEAD)
$ docker buildx build --push
```

## Deploy

```
kubectl apply -f k8s/
```

## Exposed Metrics

name (unit)

- `k8s_image_pull_duration` (ms)
- `k8s_image_pull_wait_only_duration` (ms)
- `k8s_image_size` (bytes)