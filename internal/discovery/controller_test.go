package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/discovery"
	"github.com/gulmix/apigateway/internal/loadbalancer"
	"github.com/gulmix/apigateway/internal/loadbalancer/algorithms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func setup(t *testing.T) (*fake.Clientset, *loadbalancer.Registry, *discovery.Controller) {
	t.Helper()
	client := fake.NewClientset()
	reg := loadbalancer.NewRegistry()
	cfg := config.DiscoveryConfig{
		Enabled:           true,
		Namespace:         "default",
		AnnotationsPrefix: "gateway.io",
	}
	ctrl := discovery.NewController(client, reg, cfg, zap.NewNop())
	return client, reg, ctrl
}
func svc(upstream, port string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "search-service",
			Namespace: "default",
			Annotations: map[string]string{
				"gateway.io/upstream": upstream,
				"gateway.io/port":     port,
				"gateway.io/weight":   "1",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8081}},
		},
	}
}

func ep(ips ...string) *corev1.Endpoints {
	var addrs []corev1.EndpointAddress
	for _, ip := range ips {
		addrs = append(addrs, corev1.EndpointAddress{IP: ip})
	}

	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "search-service", Namespace: "default"},
		Subsets:    []corev1.EndpointSubset{{Addresses: addrs, Ports: []corev1.EndpointPort{{Port: 8081}}}},
	}
}

func TestControllerAddsBackendsOnEndpointCreate(t *testing.T) {
	client, reg, ctrl := setup(t)

	_, err := client.CoreV1().Services("default").Create(context.Background(), svc("search-service", "8081"), metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = client.CoreV1().Endpoints("default").Create(context.Background(), ep("10.0.0.1", "10.0.0.2"), metav1.CreateOptions{})
	require.NoError(t, err)

	pool := loadbalancer.NewPool("search-service", algorithms.NewRoundRobin(), nil)
	reg.Register("search-service", pool)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ctrl.Run(ctx)

	assert.Eventually(t, func() bool {
		return len(pool.Backends()) == 2
	}, 2*time.Second, 50*time.Millisecond)
}

func TestControllerRemovesBackendOnEndpointDelete(t *testing.T) {
	client, reg, ctrl := setup(t)

	_, err := client.CoreV1().Services("default").Create(context.Background(), svc("search-service", "8081"), metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = client.CoreV1().Endpoints("default").Create(context.Background(), ep("10.0.0.1"), metav1.CreateOptions{})
	require.NoError(t, err)

	pool := loadbalancer.NewPool("search-service", algorithms.NewRoundRobin(), nil)
	reg.Register("search-service", pool)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ctrl.Run(ctx)

	require.Eventually(t, func() bool { return len(pool.Backends()) == 1 }, 2*time.Second, 50*time.Millisecond)

	err = client.CoreV1().Endpoints("default").Delete(context.Background(), "search-service", metav1.DeleteOptions{})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(pool.Backends()) == 0
	}, 2*time.Second, 50*time.Millisecond)
}

func TestControllerIgnoresUnannotatedService(t *testing.T) {
	client, reg, ctrl := setup(t)

	plain := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "search-service", Namespace: "default"}}
	_, err := client.CoreV1().Services("default").Create(context.Background(), plain, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = client.CoreV1().Endpoints("default").Create(context.Background(), ep("10.0.0.1"), metav1.CreateOptions{})
	require.NoError(t, err)

	pool := loadbalancer.NewPool("search-service", algorithms.NewRoundRobin(), nil)
	reg.Register("search-service", pool)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go ctrl.Run(ctx)

	<-ctx.Done()
	assert.Empty(t, pool.Backends())
}
