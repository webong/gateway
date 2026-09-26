package provision

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type routeInstance struct {
	id     string
	target *url.URL
}

type routePool struct {
	spec      ServerSpec
	instances []routeInstance
	next      uint64
}

// Router sends traffic to a healthy provisioned instance selected by logical
// hostname and longest path prefix.
type Router struct {
	mu    sync.Mutex
	pools map[string]*routePool
}

func NewRouter() *Router {
	return &Router{pools: make(map[string]*routePool)}
}

func (r *Router) Add(spec ServerSpec, instanceID, host string, port int) {
	target := &url.URL{Scheme: "http", Host: host + ":" + strconv.Itoa(port)}

	r.mu.Lock()
	defer r.mu.Unlock()
	pool := r.pools[spec.ID]
	if pool == nil {
		pool = &routePool{}
		r.pools[spec.ID] = pool
	}
	pool.spec = spec
	for index := range pool.instances {
		if pool.instances[index].id == instanceID {
			pool.instances[index].target = target
			return
		}
	}
	pool.instances = append(pool.instances, routeInstance{id: instanceID, target: target})
}

func (r *Router) Remove(serverID, instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pool := r.pools[serverID]
	if pool == nil {
		return
	}
	for index := range pool.instances {
		if pool.instances[index].id == instanceID {
			pool.instances = append(pool.instances[:index], pool.instances[index+1:]...)
			break
		}
	}
	if len(pool.instances) == 0 {
		delete(r.pools, serverID)
	}
}

func (r *Router) ServeIfMatched(writer http.ResponseWriter, request *http.Request) bool {
	target := r.match(request)
	if target == nil {
		return false
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "Provisioned instance unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(writer, request)

	return true
}

func (r *Router) match(request *http.Request) *url.URL {
	hostname := request.Host
	if host, _, err := strings.Cut(hostname, ":"); err {
		hostname = host
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	var selected *routePool
	for _, pool := range r.pools {
		if len(pool.instances) == 0 || !strings.EqualFold(pool.spec.Hostname, hostname) {
			continue
		}
		if !matchesPath(pool.spec.Path, request.URL.Path) {
			continue
		}
		if selected == nil || len(pool.spec.Path) > len(selected.spec.Path) {
			selected = pool
		}
	}
	if selected == nil {
		return nil
	}

	instance := selected.instances[selected.next%uint64(len(selected.instances))]
	selected.next++
	target := *instance.target

	return &target
}

func matchesPath(prefix, requestPath string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	if requestPath == prefix {
		return true
	}

	return strings.HasPrefix(requestPath, strings.TrimSuffix(prefix, "/")+"/")
}
