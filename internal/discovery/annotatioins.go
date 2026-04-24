package discovery

import (
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

type EndpointMeta struct {
	Upstream string
	Port     string
	Weight   int
}

func parseMeta(svc *corev1.Service, prefix string) (EndpointMeta, bool) {
	ann := svc.Annotations
	if ann == nil {
		return EndpointMeta{}, false
	}
	upstream := ann[prefix+"/upstream"]
	if upstream == "" {
		return EndpointMeta{}, false
	}
	port := ann[prefix+"/port"]
	if port == "" && len(svc.Spec.Ports) > 0 {
		port = fmt.Sprintf("%d", svc.Spec.Ports[0].Port)
	}
	weight := 1
	if w, err := strconv.Atoi(ann[prefix+"/weight"]); err == nil && w > 0 {
		weight = w
	}
	return EndpointMeta{Upstream: upstream, Port: port, Weight: weight}, true
}

func endpointAddrs(ep *corev1.Endpoints, portOverride string) []string {
	var addrs []string
	for _, subset := range ep.Subsets {
		port := portOverride
		if port == "" && len(subset.Ports) > 0 {
			port = fmt.Sprintf("%d", subset.Ports[0].Port)
		}
		for _, addr := range subset.Addresses {
			addrs = append(addrs, addr.IP+":"+port)
		}
	}
	return addrs
}
