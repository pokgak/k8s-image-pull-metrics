package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var (
	config                        *rest.Config
	durationPullHistogram         metric.Int64Histogram
	durationPullWaitOnlyHistogram metric.Int64Histogram
	imageSizeGauge                metric.Int64Gauge
)

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	ctx := flag.String("context", "", "The name of the kubeconfig context to use")
	flag.Parse()

	var err error
	// Use in-cluster config if kubeconfig is not provided
	if *kubeconfig == "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}

	// Override the context if specified
	if *ctx != "" {
		configOverrides := &clientcmd.ConfigOverrides{CurrentContext: *ctx}
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
			configOverrides,
		).ClientConfig()
		if err != nil {
			panic(err.Error())
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// OpenTelemetry metrics initialization
	res, err := newResource()
	if err != nil {
		panic(err)
	}

	// Create a meter provider.
	// You can pass this instance directly to your instrumented code if it
	// accepts a MeterProvider instance.
	meterProvider, err := newMeterProvider(context.Background(), res)
	if err != nil {
		panic(err)
	}

	// Handle shutdown properly so nothing leaks.
	defer func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			log.Println(err)
		}
	}()

	// Register as global meter provider so that it can be used via otel.Meter
	// and accessed using otel.GetMeterProvider.
	// Most instrumentation libraries use the global meter provider as default.
	// If the global meter provider is not set then a no-op implementation
	// is used, which fails to generate data.
	otel.SetMeterProvider(meterProvider)

	var meter = otel.Meter("pokgak.xyz/k8s-image-pull-metrics")
	durationPullHistogram, _ = meter.Int64Histogram(
		"k8s.image.pull.duration",
		metric.WithDescription("The duration of image pull."),
		metric.WithUnit("ms"),
		metric.WithExplicitBucketBoundaries([]float64{15000, 30000, 45000, 60000, 120000, 180000, 240000, 300000}...),
	)
	durationPullWaitOnlyHistogram, _ = meter.Int64Histogram(
		"k8s.image.pull_wait_only.duration",
		metric.WithDescription("The duration of image pull including waiting time."),
		metric.WithUnit("ms"),
		metric.WithExplicitBucketBoundaries([]float64{15000, 30000, 45000, 60000, 120000, 180000, 240000, 300000}...),
	)
	imageSizeGauge, _ = meter.Int64Gauge(
		"k8s.image.size",
		metric.WithDescription("The size of the image in bytes."),
		metric.WithUnit("bytes"),
	)

	// setup informer to watch for events
	factory := informers.NewSharedInformerFactory(clientset, 0)
	informer := factory.Core().V1().Events().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: handleAddFunc,
	})

	stopCh := make(chan struct{})
	defer close(stopCh)

	// Start the informer
	go informer.Run(stopCh)

	// Wait for the informer to sync
	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		log.Fatalf("Error syncing cache")
	}

	// Block forever
	select {}
}

func handleAddFunc(obj interface{}) {
	event, ok := obj.(*v1.Event)
	if !ok {
		return
	}

	if event.Source.Component != "kubelet" || event.InvolvedObject.Kind != "Pod" || event.Reason != "Pulled" {
		return
	}

	msg := event.Message
	// skip if the message starts with "Container image" as it is not the message we are interested in
	if len(msg) >= 15 && msg[:15] == "Container image" {
		log.Println("Skipping event message:", msg)
		return
	}

	log.Println("Pod event added: ", event.Message)

	// input: "Successfully pulled image \"<account-id>.dkr.ecr.ap-southeast-1.amazonaws.com/example-service:99cd3b4\" in 1m44.643s (1m44.643s including waiting). Image size: 1169083618 bytes.",
	// extract the image name, tag, duration pull, duration wait, and image size
	var imageName, durationPullStr, durationWaitStr, imageSize string
	n, err := fmt.Sscanf(msg, "Successfully pulled image %q in %s (%s including waiting). Image size: %s bytes.", &imageName, &durationPullStr, &durationWaitStr, &imageSize)
	if err == nil && n == 4 {
		durationPull, err := time.ParseDuration(durationPullStr)
		if err != nil {
			log.Println("Failed to parse durationPull:", err)
			return
		}
		durationWithWait, err := time.ParseDuration(durationWaitStr)
		if err != nil {
			log.Println("Failed to parse durationWait:", err)
			return
		}
		imageSizeInt, err := strconv.ParseInt(imageSize, 10, 64)
		if err != nil {
			log.Println("Failed to parse imageSize:", err)
			return
		}

		commonAttributes := []attribute.KeyValue{
			attribute.Int64("observed.timestamp", event.LastTimestamp.UnixMilli()),
			attribute.String("exported.namespace", event.Namespace),
			attribute.String("exported.pod.name", event.InvolvedObject.Name),
			attribute.String("exported.container.name", event.InvolvedObject.Name),
			attribute.String("exported.pod.image", imageName),
			attribute.Int64("exported.pod.image.size", imageSizeInt),
			attribute.String("exported.host", event.Source.Host),
		}

		// extract the prefix of the pod name
		// given: k8s-image-pull-metrics-5f588dd8cf-8lnm4
		// extract: k8s-image-pull-metrics
		podName := event.InvolvedObject.Name
		re := regexp.MustCompile(`^(.*)-.*-.*$`)
		matches := re.FindStringSubmatch(podName)
		if len(matches) > 1 {
			commonAttributes = append(commonAttributes, attribute.String("exported.pod.prefix", matches[1]))
		}
		
		imageSizeGauge.Record(context.Background(), imageSizeInt, metric.WithAttributes(commonAttributes...))
		durationPullHistogram.Record(context.Background(), durationPull.Milliseconds(), metric.WithAttributes(commonAttributes...))
		durationPullWaitOnlyHistogram.Record(context.Background(), time.Duration(durationWithWait - durationPull).Milliseconds(), metric.WithAttributes(commonAttributes...))

		log.Println("Recorded metrics: durationPull:", durationPull.Seconds(), "durationWait:", time.Duration(durationWithWait - durationPull).Seconds(), "imageSize:", imageSizeInt)
	}
	if err != nil || n != 4 {
		log.Println("Failed to parse event message:", err)
		return
	}
}

func newResource() (*resource.Resource, error) {
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("k8s-image-pull-metrics"),
			// semconv.ServiceVersion("0.1.0"),
		))
}

func newMeterProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	opts := []otlpmetrichttp.Option{}
	opts = append(opts, otlpmetrichttp.WithInsecure())
	// opts = append(opts, otlpmetrichttp.WithEndpoint("http://collector.monitoring.svc.cluster.local:4318"))
	// opts = append(opts, otlpmetrichttp.WithURLPath("/v1/metrics"))
	opts = append(opts, otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))

	metricExporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(30*time.Second),
		)),
	)

	return meterProvider, nil
}
