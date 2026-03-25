package loadbalancer

type Balancer interface {
	Next() string
}
