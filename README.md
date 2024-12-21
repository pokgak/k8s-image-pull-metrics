# README

## Building

```
$ export REGISTRY="<registry url>"
$ export TAG=$(git rev-parse --short HEAD)
$ docker buildx build --push
```