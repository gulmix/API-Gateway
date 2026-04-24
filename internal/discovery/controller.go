package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gulmix/apigateway/internal/config"
	"github.com/gulmix/apigateway/internal/loadbalancer"
	"github.com/gulmix/apigateway/internal/loadbalancer/algorithms"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type tracked struct {
	upstream string
	addrs    map[string]struct{}
}

type Controller struct {
	client kubernetes.Interface
	reg    *loadbalancer.Registry
	cfg    config.DiscoveryConfig
	log    *zap.Logger

	mu    sync.Mutex
	state map[string]*tracked
}

func NewController(client kubernetes.Interface, reg *loadbalancer.Registry, cfg config.DiscoveryConfig, log *zap.Logger) *Controller {
	return &Controller{
		client: client,
		reg:    reg,
		cfg:    cfg,
		log:    log,
		state:  make(map[string]*tracked),
	}
}

func (c *Controller) Run(ctx context.Context) {
	prefix := c.cfg.AnnotationsPrefix
	if prefix == "" {
		prefix = "gateway.io"
	}

	factory := informers.NewSharedInformerFactoryWithOptions(c.client, 30*time.Second, informers.WithNamespace(c.cfg.Namespace))

	svcInformer := factory.Core().V1().Services()
	epInformer := factory.Core().V1().Endpoints()
	svcLister := svcInformer.Lister()

	epInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if ep, ok := toEndpoints(obj); ok {
				c.reconcile(ep, svcLister, prefix)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if ep, ok := toEndpoints(oldObj); ok {
				c.reconcile(ep, svcLister, prefix)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if ep, ok := toEndpoints(obj); ok {
				c.remove(ep)
			}
		},
	})

	factory.Start(ctx.Done())

	c.log.Info("discovery: waiting for cache sync")
	if !cache.WaitForCacheSync(ctx.Done(), svcInformer.Informer().HasSynced, epInformer.Informer().HasSynced) {
		c.log.Error("discovery: cache sync timed out")
		return
	}
	c.log.Info("discovery: controller ready")

	if eps, err := epInformer.Lister().List(labels.Everything()); err == nil {
		for _, ep := range eps {
			c.reconcile(ep, svcLister, prefix)
		}
	}

	<-ctx.Done()
	c.log.Info("discovery: controller stopped")
}

func (c *Controller) reconcile(ep *corev1.Endpoints, svcLister listersv1.ServiceLister, prefix string) {
	key := fmt.Sprintf("%s/%s", ep.Namespace, ep.Name)

	svc, err := svcLister.Services(ep.Namespace).Get(ep.Name)
	if err != nil {
		c.remove(ep)
		return
	}

	meta, ok := parseMeta(svc, prefix)
	if !ok {
		c.remove(ep)
		return
	}

	newAddrs := endpointAddrs(ep, meta.Port)
	newSet := make(map[string]struct{}, len(newAddrs))
	for _, a := range newAddrs {
		newSet[a] = struct{}{}
	}

	pool := c.ensurePool(meta.Upstream)

	c.mu.Lock()
	prev := c.state[key]
	c.state[key] = &tracked{upstream: meta.Upstream, addrs: newSet}
	c.mu.Unlock()

	for addr := range newSet {
		if prev == nil || prev.upstream != meta.Upstream {
			pool.Add(loadbalancer.NewBackend(addr, meta.Weight))
			RecordRegistered(meta.Upstream, addr)
			c.log.Info("discovery: backend added",
				zap.String("upstream", meta.Upstream),
				zap.String("addr", addr),
			)
			continue
		}
		if _, existed := prev.addrs[addr]; !existed {
			pool.Add(loadbalancer.NewBackend(addr, meta.Weight))
			RecordRegistered(meta.Upstream, addr)
			c.log.Info("discovery: backend added",
				zap.String("upstream", meta.Upstream),
				zap.String("addr", addr),
			)
		}
	}

	if prev != nil {
		oldPool := c.reg.Pool(prev.upstream)
		for addr := range prev.addrs {
			if _, still := newSet[addr]; !still {
				if oldPool != nil {
					oldPool.Remove(addr)
				}
				RecordDeregistered(prev.upstream, addr)
				c.log.Info("discovery: backend removed",
					zap.String("upstream", prev.upstream),
					zap.String("addr", addr),
				)
			}
		}
	}
}

func (c *Controller) remove(ep *corev1.Endpoints) {
	key := fmt.Sprintf("%s/%s", ep.Namespace, ep.Name)

	c.mu.Lock()
	prev := c.state[key]
	delete(c.state, key)
	c.mu.Unlock()

	if prev == nil {
		return
	}
	pool := c.reg.Pool(prev.upstream)
	if pool == nil {
		return
	}
	for addr := range prev.addrs {
		pool.Remove(addr)
		RecordDeregistered(prev.upstream, addr)
		c.log.Info("discovery: backend removed (endpoint deleted)",
			zap.String("upstream", prev.upstream),
			zap.String("addr", addr),
		)
	}
}

func (c *Controller) ensurePool(upstream string) *loadbalancer.Pool {
	if p := c.reg.Pool(upstream); p != nil {
		return p
	}
	p := loadbalancer.NewPool(upstream, algorithms.NewRoundRobin(), nil)
	c.reg.Register(upstream, p)
	return p
}

func toEndpoints(obj interface{}) (*corev1.Endpoints, bool) {
	if ep, ok := obj.(*corev1.Endpoints); ok {
		return ep, true
	}
	if ts, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		ep, ok := ts.Obj.(*corev1.Endpoints)
		return ep, ok
	}
	return nil, false
}
