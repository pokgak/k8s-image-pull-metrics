# README

k8s-image-pull-metrics listens to k8s event produced by Pods from all namespaces and parses the event message for the time taken for pods to pull image from registry. The values are then pushed as OpenTelemetry Metrics to the metrics backend of your choice.

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

### Specifying where to send metrics

Use the env `OTEL_EXPORTER_OTLP_ENDPOINT` to specify where to send the metrics to.

## Exposed Metrics

name (unit)

- `k8s_image_pull_duration` (ms)
- `k8s_image_pull_wait_only_duration` (ms)
- `k8s_image_size` (bytes)